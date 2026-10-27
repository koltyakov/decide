package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
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
	// Key is an ephemeral connection-test credential, never a CLI argument.
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

// Prepare validates and encodes a request without loading credentials or accessing the network.
func Prepare(request Object, options Options) (*Prepared, error) {
	// Copy before applying defaults or translating provider-specific fields.
	b, err := json.Marshal(request)
	if err != nil || len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "request is not JSON or exceeds 32 MiB")
	}
	var payload Object
	if err = Decode(b, &payload); err != nil {
		return nil, err
	}
	if err = Validate(payload); err != nil {
		return nil, err
	}
	o, keyEnv, err := options.resolve(payload)
	if err != nil {
		return nil, err
	}
	payload["model"] = o.Model
	provider, _ := Lookup(o.Provider)
	capabilities := provider.CapabilitiesFor(o.Model)
	if err = validateProvider(payload, o, capabilities); err != nil {
		return nil, err
	}
	minimum := 280
	if o.Provider == "aimlapi" || o.Provider == "openrouter" {
		minimum = 256
	}
	if err = prepareAssets(payload, o.Attachments, capabilities.Images == "base64" || capabilities.Images == "parts", minimum); err != nil {
		return nil, err
	}
	questions := payload["questions"].(map[string]any)
	payload, headers, err := providerRequest(payload, o)
	if err != nil {
		return nil, err
	}
	b, err = json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	contentType := "application/json"
	if len(o.Attachments) > 0 && capabilities.Images == "multipart" {
		b, contentType, err = multipartBody(b, o.Attachments)
		if err != nil {
			return nil, err
		}
	}
	if len(b) > MaxBytes {
		return nil, fault.New(fault.Usage, "request exceeds 32 MiB")
	}
	headers.Set("Content-Type", contentType)
	uncompressedBytes := len(b)
	b, err = compressBody(b, o.Compression)
	if err != nil {
		return nil, err
	}
	if o.Compression != "none" {
		headers.Set("Content-Encoding", o.Compression)
	}
	return &Prepared{Questions: questions, payload: payload, options: o, keyEnv: keyEnv, headers: headers, body: b, uncompressedBytes: uncompressedBytes}, nil
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
			answer["noul"] = answer["probability"]
			delete(answer, "probability")
		}
		if p.options.Provider == "vercel" {
			delete(answer, "type")
		}
		if err := validateAnswer(answer, spec.(map[string]any)); err != nil {
			return nil, err
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
		options.Provider = os.Getenv("DECIDE_PROVIDER")
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
		switch options.Provider {
		case "custom":
			options.Endpoint = os.Getenv("DECIDE_ENDPOINT")
		case "official":
			options.Endpoint = os.Getenv("TYPESAFE_API_URL")
		}
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
		code := fault.Failure
		if res.StatusCode == 401 || res.StatusCode == 403 {
			code = fault.Auth
		} else if res.StatusCode == 408 || res.StatusCode == 429 || res.StatusCode >= 500 {
			code = fault.Remote
		}
		// Error bodies can echo credentials or private input. Do not print them.
		return nil, fault.New(code, fmt.Sprintf("API HTTP %d; not retried", res.StatusCode))
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
