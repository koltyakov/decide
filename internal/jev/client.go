package jev

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
)

type Client struct {
	Credentials credentials.Store
	HTTP        *http.Client
	Timeout     time.Duration
	Version     string
	// Key is an in-memory credential for connection tests and preloaded
	// batches, never a CLI argument.
	Key string
}

type Prepared struct {
	Questions         Object
	payload           Object
	options           Options
	keyEnv            string
	headers           http.Header
	body              []byte
	uncompressedBytes int
}

// Preview reports the exact wire payload and bounded upload metadata, never credentials.
func (p *Prepared) Preview() Object {
	images := []Object{}
	for _, image := range p.options.Attachments {
		images = append(images, Object{"reference": image.Reference, "format": image.Format, "bytes": len(image.Data)})
	}
	return Object{"provider": p.options.Provider, "endpoint": p.options.Endpoint, "model": p.options.Model,
		"request": p.payload, "headers": p.headers, "bodyBytes": len(p.body), "uncompressedBodyBytes": p.uncompressedBytes, "compression": p.options.Compression, "uploads": images}
}

// Summary is Preview with inline image bytes replaced by their decoded size.
// Sizes and every other request member are unchanged.
func (p *Prepared) Summary() Object {
	preview := p.Preview()
	preview["request"] = omitImageData(p.payload)
	return preview
}

// Recipe is a validated request whose state is supplied per call. Questions,
// assets, and provider checks are validated and images decoded once. A Recipe
// is immutable, so concurrent Prepare calls may share it.
type Recipe struct {
	base         Object
	options      Options
	keyEnv       string
	capabilities Capabilities
	formats      map[string]string
	pending      []string // asset references not found in questions
	images, box  bool
}

// NewRecipe validates and binds everything except state, without credentials or network access.
func NewRecipe(request Object, options Options) (*Recipe, error) {
	payload, err := copyRequest(request)
	if err != nil {
		return nil, err
	}
	return newRecipe(payload, options)
}

// Questions returns the recipe's validated questions; callers must not modify them.
func (r *Recipe) Questions() Object { return r.base["questions"].(map[string]any) }

// Prepare copies state and encodes one request from the recipe.
func (r *Recipe) Prepare(state any) (*Prepared, error) {
	b, err := json.Marshal(state)
	if err != nil || len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "state is not JSON or exceeds 32 MiB")
	}
	var owned any
	if err = Decode(b, &owned); err != nil {
		return nil, err
	}
	if err = validateState(owned); err != nil {
		return nil, err
	}
	return r.prepare(owned)
}

// PrepareJSON decodes one JSON state, which the recipe then owns, and encodes
// a request. It avoids a second copy of states read from JSONL.
func (r *Recipe) PrepareJSON(state []byte) (*Prepared, error) {
	var owned any
	if err := Decode(state, &owned); err != nil {
		return nil, err
	}
	if err := validateState(owned); err != nil {
		return nil, err
	}
	return r.prepare(owned)
}

func copyRequest(request Object) (Object, error) {
	// Copy before applying defaults or translating provider-specific fields.
	b, err := json.Marshal(request)
	if err != nil || len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "request is not JSON or exceeds 32 MiB")
	}
	var payload Object
	if err = Decode(b, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// Prepare validates and encodes a request without loading credentials or accessing the network.
func Prepare(request Object, options Options) (*Prepared, error) {
	payload, err := copyRequest(request)
	if err != nil {
		return nil, err
	}
	state := payload["state"]
	if err = validateState(state); err != nil {
		return nil, err
	}
	r, err := newRecipe(payload, options)
	if err != nil {
		return nil, err
	}
	return r.prepare(state)
}

// newRecipe takes ownership of payload.
func newRecipe(payload Object, options Options) (*Recipe, error) {
	if payload == nil {
		return nil, fault.New(fault.Usage, "request must be a JSON object with named questions")
	}
	if err := validateRecipe(payload); err != nil {
		return nil, err
	}
	o, keyEnv, err := options.resolve(payload)
	if err != nil {
		return nil, err
	}
	payload["model"] = o.Model
	provider, _ := Lookup(o.Provider)
	capabilities := provider.CapabilitiesFor(o.Model)
	questions := payload["questions"].(map[string]any)
	images := payload["assets"] != nil || len(o.Attachments) > 0
	if o.Provider == "openrouter" {
		native, err := questionImages(questions)
		if err != nil {
			return nil, err
		}
		images = images || native
	}
	if err = validateProvider(payload, o, capabilities, images); err != nil {
		return nil, err
	}
	rules := imageRules{inline: capabilities.Images == "base64" || capabilities.Images == "parts", minimum: 280}
	if o.Provider == "aimlapi" {
		rules.minimum = 256
	}
	if o.Provider == "openrouter" {
		rules = openRouterRules
	}
	formats, err := bindAssets(payload, o.Attachments, rules)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(formats))
	for ref := range formats {
		refs = append(refs, ref)
	}
	box := false
	for _, value := range questions {
		box = box || value.(map[string]any)["type"] == "box"
	}
	delete(payload, "state")
	return &Recipe{base: payload, options: o, keyEnv: keyEnv, capabilities: capabilities, formats: formats,
		pending: unreferenced(refs, questionValues(questions)...), images: images, box: box}, nil
}

// prepare encodes state, which the caller has copied and validated.
func (r *Recipe) prepare(state any) (*Prepared, error) {
	images := r.images
	if r.options.Provider == "openrouter" {
		native, err := nativeImages(state)
		if err != nil {
			return nil, err
		}
		images = images || native
	}
	if err := validateImages(r.options, r.capabilities, images, r.box); err != nil {
		return nil, err
	}
	if len(unreferenced(r.pending, state)) > 0 {
		return nil, errUnreferencedAsset
	}
	payload := maps.Clone(r.base)
	payload["state"] = state
	payload, headers, err := providerRequest(payload, r.options, r.formats)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	contentType := "application/json"
	if len(r.options.Attachments) > 0 && r.capabilities.Images == "multipart" {
		b, contentType, err = multipartBody(b, r.options.Attachments)
		if err != nil {
			return nil, err
		}
	}
	if len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "request exceeds 32 MiB")
	}
	headers.Set("Content-Type", contentType)
	uncompressedBytes := len(b)
	b, err = compressBody(b, r.options.Compression)
	if err != nil {
		return nil, err
	}
	if r.options.Compression != "none" {
		headers.Set("Content-Encoding", r.options.Compression)
	}
	return &Prepared{Questions: r.Questions(), payload: payload, options: r.options, keyEnv: r.keyEnv, headers: headers, body: b, uncompressedBytes: uncompressedBytes}, nil
}

func (c Client) Evaluate(ctx context.Context, request Object, options Options) (Object, error) {
	options, err := c.Resolve(options)
	if err != nil {
		return nil, err
	}
	p, err := Prepare(request, options)
	if err != nil {
		return nil, err
	}
	return c.Execute(ctx, p)
}

// Execute sends a prepared request once. Prepared requests contain no credentials.
func (c Client) Execute(ctx context.Context, p *Prepared) (Object, error) {
	key, err := c.key(p.options.Provider, p.keyEnv)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(key, "\r\n") {
		return nil, fault.New(fault.Auth, "invalid API key")
	}
	headers := p.headers.Clone()
	headers.Set("Authorization", "Bearer "+key)
	result, err := c.send(ctx, http.MethodPost, p.options.Endpoint, headers, p.body)
	if err != nil {
		return nil, err
	}
	result, err = providerResponse(result, p.options.Provider)
	if err != nil {
		return nil, err
	}
	answers, ok := result["answers"].(map[string]any)
	if !ok {
		return nil, fault.New(fault.Failure, "API response has no answers object")
	}
	for name, spec := range p.Questions {
		answer, ok := answers[name].(map[string]any)
		if !ok {
			return nil, fault.New(fault.Failure, "API response is missing a requested answer")
		}
		if p.options.Provider == "vercel" && answer["type"] == "boolean" {
			answer["type"] = "noul"
			answer["noul"] = answer["probability"]
			delete(answer, "probability")
		}
		if err := validateAnswer(answer, spec.(map[string]any)); err != nil {
			return nil, err
		}
		if p.options.Provider == "vercel" {
			delete(answer, "type")
		}
	}
	return result, nil
}

// Models queries the selected provider's catalog without rewriting its response shape.
func (c Client) Models(ctx context.Context, options Options) (Object, error) {
	options, err := c.Resolve(options)
	if err != nil {
		return nil, err
	}
	o, keyEnv, err := options.resolve(Object{})
	if err != nil {
		return nil, err
	}
	provider, _ := Lookup(o.Provider)
	if !provider.Capabilities.Models {
		return nil, fault.New(fault.Usage, "model discovery is not supported by this provider")
	}
	catalogEndpoint := ""
	if o.Provider == "openrouter" {
		if !strings.HasSuffix(o.Endpoint, "/alpha/decisions") {
			return nil, fault.New(fault.Usage, "OpenRouter model discovery requires an endpoint ending in /alpha/decisions")
		}
		catalogEndpoint = strings.TrimSuffix(o.Endpoint, "/alpha/decisions") + "/v1/models?output_modalities=decisions"
	} else if !strings.HasSuffix(o.Endpoint, "/systemone") {
		return nil, fault.New(fault.Usage, "model discovery requires an endpoint ending in /systemone")
	} else {
		catalogEndpoint = strings.TrimSuffix(o.Endpoint, "/systemone") + "/models"
	}
	key, err := c.key(o.Provider, keyEnv)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+key)
	result, err := c.send(ctx, http.MethodGet, catalogEndpoint, headers, nil)
	if err != nil {
		return nil, err
	}
	listField, idField := "models", "name"
	if o.Provider == "digitalocean" || o.Provider == "openrouter" {
		listField, idField = "data", "id"
	}
	models, ok := result[listField].([]any)
	if !ok {
		return nil, fault.New(fault.Failure, "API response has no "+listField+" model array")
	}
	for _, value := range models {
		model, ok := value.(map[string]any)
		if !ok || !nonempty(model[idField]) {
			return nil, fault.New(fault.Failure, "API returned an invalid model entry")
		}
	}
	return result, nil
}

// Preload resolves the recipe's credential once, so a batch doesn't reread the
// store for every record. A lookup failure leaves the client unchanged and is
// reported by each Execute call as before.
func (c Client) Preload(r *Recipe) Client {
	if key, err := c.key(r.options.Provider, r.keyEnv); err == nil {
		c.Key = key
	}
	return c
}

func (c Client) key(provider, env string) (string, error) {
	if c.Key != "" {
		return c.Key, nil
	}
	key, _, err := c.Credentials.Get(provider, env)
	return key, err
}

// Resolve applies flag > environment > saved connection > built-in precedence.
// Saved model defaults remain below a model explicitly carried by a request.
func (c Client) Resolve(options Options) (Options, error) {
	saved, err := c.Credentials.Connections()
	if err != nil {
		return options, err
	}
	if options.Provider == "" {
		options.Provider = envProvider()
	}
	if options.Provider == "" {
		options.Provider = saved.Default
	}
	if options.Provider == "" {
		options.Provider = "official"
	}
	if _, err = Lookup(options.Provider); err != nil {
		return options, err
	}
	connection := saved.Providers[options.Provider]
	if options.Endpoint == "" {
		options.Endpoint = envEndpoint(options.Provider)
		if options.Endpoint == "" {
			options.Endpoint = connection.Endpoint
		}
	}
	if options.DefaultModel == "" {
		options.DefaultModel = connection.Model
	}
	return options, nil
}

// Configuration resolves effective connection settings without authenticating.
func (c Client) Configuration(options Options) (Options, error) {
	resolved, err := c.Resolve(options)
	if err != nil {
		return resolved, err
	}
	resolved, _, err = resolved.resolve(Object{})
	return resolved, err
}

func (c Client) send(ctx context.Context, method, endpoint string, headers http.Header, body []byte) (Object, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fault.New(fault.Usage, "invalid API request")
	}
	req.Header = headers
	req.Header.Set("User-Agent", "decide/"+c.Version)
	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// Never forward credentials or replay a POST through redirects.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fault.New(fault.Remote, "API request cancelled or timed out; not retried")
		}
		return nil, fault.New(fault.Remote, "API connection failed; not retried")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Read a bounded prefix so the connection can be reused. Error bodies can
		// echo credentials or private input, so only a fixed, documented error
		// code is ever matched; body text is never reported.
		prefix, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		code := fault.Failure
		if res.StatusCode == 401 || res.StatusCode == 403 {
			code = fault.Auth
		} else if res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500 {
			code = fault.Remote
		}
		// Error bodies can echo credentials or private input. Do not print them.
		return nil, fault.New(code, fmt.Sprintf("API HTTP %d%s; not retried", res.StatusCode, statusHint(res.StatusCode, prefix))+responseDetails(res.Header))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	if err != nil || len(data) > MaxBytes {
		return nil, fault.New(fault.Remote, "API response unreadable or exceeds 32 MiB")
	}
	var result Object
	if Decode(data, &result) != nil || result == nil {
		return nil, fault.New(fault.Remote, "API returned invalid JSON object")
	}
	return result, nil
}

func validateAnswer(answer, spec Object) error {
	kind := spec["type"].(string)
	if actual, exists := answer["type"]; exists && actual != kind {
		return fault.New(fault.Failure, "API answer type does not match its question")
	}
	value := answer[kind]
	valid := false
	switch kind {
	case "box":
		box, ok := value.(map[string]any)
		if ok {
			coords := map[string]float64{}
			valid = true
			for _, edge := range []string{"top", "bottom", "left", "right"} {
				num, ok := box[edge].(json.Number)
				n, err := num.Float64()
				if !ok || err != nil || n < 0 || n > 1 || math.IsNaN(n) || math.IsInf(n, 0) {
					valid = false
				}
				coords[edge] = n
			}
			valid = valid && coords["left"] < coords["right"] && coords["top"] < coords["bottom"]
		}
	case "choice":
		key, ok := value.(string)
		_, exists := spec["criteria"].(map[string]any)[key]
		valid = ok && exists
	case "noul", "score":
		number, ok := value.(json.Number)
		n, err := number.Float64()
		max := 1.0
		if kind == "score" {
			max = float64(len(spec["criteria"].([]any)) - 1)
		}
		// Scores may be fractional expected levels, not just integer indices.
		valid = ok && err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= max
	}
	if !valid {
		return fault.New(fault.Failure, "API returned an invalid "+kind+" answer")
	}
	return nil
}

// statusHint names documented TypeSafe failure meanings without echoing the body.
func statusHint(status int, body []byte) string {
	switch status {
	case http.StatusBadRequest:
		if bytes.Contains(body, []byte("max_tokens_exceeded")) {
			return " (max_tokens_exceeded: request exceeds the model's context length)"
		}
		return " (request rejected by API validation)"
	case http.StatusRequestEntityTooLarge:
		return " (request body exceeds the API limit)"
	case http.StatusUnsupportedMediaType:
		return " (unsupported or layered Content-Encoding)"
	case http.StatusUnprocessableEntity:
		return " (request failed API schema validation)"
	case http.StatusTooManyRequests:
		return " (rate limited)"
	case 529:
		return " (API overloaded)"
	}
	return ""
}

var requestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// responseDetails reports validated scheduling and support headers. They are
// never bodies, and values outside strict formats are dropped.
func responseDetails(h http.Header) string {
	var details []string
	if value := strings.TrimSpace(h.Get("Retry-After")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 && seconds <= 7*24*3600 {
			details = append(details, "retry-after "+strconv.Itoa(seconds)+"s")
		} else if at, err := http.ParseTime(value); err == nil {
			details = append(details, "retry-after "+at.UTC().Format(time.RFC3339))
		}
	}
	for _, name := range []string{"X-Request-Id", "Request-Id", "Cf-Ray"} {
		if value := h.Get(name); requestID.MatchString(value) {
			details = append(details, "request id "+value)
			break
		}
	}
	if len(details) == 0 {
		return ""
	}
	return "; " + strings.Join(details, "; ")
}

// omitImageData copies the payload members that carry inline image bytes.
func omitImageData(payload Object) Object {
	out := maps.Clone(payload)
	if assets, ok := out["assets"].(map[string]any); ok {
		copied := map[string]any{}
		for ref, value := range assets {
			if asset, ok := value.(map[string]any); ok {
				if data, ok := asset["data_base64"].(string); ok {
					asset = maps.Clone(asset)
					asset["data_base64"] = omitted(data)
					value = asset
				}
			}
			copied[ref] = value
		}
		out["assets"] = copied
	}
	for _, key := range []string{"state", "questions"} {
		if value, ok := out[key]; ok {
			out[key] = omitParts(value)
		}
	}
	return out
}

func omitParts(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = omitParts(item)
		}
		return out
	case map[string]any:
		if part, ok := v["image_url"].(map[string]any); ok && v["type"] == "image_url" {
			if url, ok := part["url"].(string); ok && strings.HasPrefix(url, "data:image/") {
				if prefix, data, found := strings.Cut(url, ";base64,"); found {
					out := maps.Clone(v)
					out["image_url"] = Object{"url": prefix + ";base64," + omitted(data)}
					return out
				}
			}
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = omitParts(item)
		}
		return out
	}
	return value
}

func omitted(data string) string {
	size := base64.StdEncoding.DecodedLen(len(data)) - strings.Count(data[max(0, len(data)-2):], "=")
	return "[omitted: " + strconv.Itoa(size) + " image bytes]"
}
