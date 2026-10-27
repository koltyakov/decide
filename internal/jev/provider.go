package jev

import (
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
)

type Provider struct {
	Endpoint, KeyEnv, Model string
	Family                  string
	Capabilities            Capabilities
}

// Capabilities describes the native System One contract, not unrelated media APIs.
type Capabilities struct {
	Images      string   `json:"images"` // none, multipart, base64, or parts; multipart also accepts inline assets.
	Box         bool     `json:"box"`
	LongContext bool     `json:"longContext"`
	Models      bool     `json:"models"`
	Compression []string `json:"compression"`
}

func Providers() map[string]Provider {
	return map[string]Provider{
		"official":     {Endpoint: "https://api.typesafe.ai/v1/systemone", KeyEnv: "TYPESAFE_API_KEY", Model: "jev-latest", Family: "jev", Capabilities: Capabilities{Images: "multipart", Box: true, LongContext: true, Models: true, Compression: []string{"gzip", "x-gzip", "zstd"}}},
		"vercel":       {Endpoint: "https://ai-gateway.vercel.sh/v4/ai/evaluation-model", KeyEnv: "AI_GATEWAY_API_KEY", Model: "typesafe-ai/jev-1.14.0", Family: "jev", Capabilities: Capabilities{Images: "none"}},
		"openrouter":   {Endpoint: "https://openrouter.ai/api/alpha/decisions", KeyEnv: "OPENROUTER_API_KEY", Model: "~typesafe/jev-latest", Family: "jev", Capabilities: Capabilities{Images: "parts", Box: true, Models: true}},
		"digitalocean": {Endpoint: "https://inference.do-ai.run/v1/systemone", KeyEnv: "DIGITALOCEAN_MODEL_ACCESS_KEY", Model: "typesafe-jev-1.13.0", Family: "jev", Capabilities: Capabilities{Images: "none", Models: true}},
		"aimlapi":      {Endpoint: "https://api.aimlapi.com/v1/decisions", KeyEnv: "AIMLAPI_KEY", Model: "typesafe/jev", Family: "jev", Capabilities: Capabilities{Images: "base64", Box: true}},
		"custom":       {KeyEnv: "DECIDE_API_KEY", Model: "jev-latest", Family: "custom", Capabilities: Capabilities{Images: "multipart", Box: true, LongContext: true, Models: true, Compression: []string{"gzip", "x-gzip", "zstd"}}},
		"cloudflare":   {KeyEnv: "CLOUDFLARE_API_TOKEN", Model: "typesafe/jev", Family: "jev", Capabilities: Capabilities{Images: "none"}},
		"clef":         {KeyEnv: "CLOUDFLARE_API_TOKEN", Model: "clef-flash", Family: "clef", Capabilities: Capabilities{Images: "none"}},
	}
}

func ProviderNames() []string {
	names := make([]string, 0, len(Providers()))
	for name := range Providers() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Lookup(name string) (Provider, error) {
	p, ok := Providers()[name]
	if !ok {
		return p, fault.New(fault.Usage, "provider must be one of: "+strings.Join(ProviderNames(), ", "))
	}
	return p, nil
}

type Options struct {
	Provider     string
	Model        string
	DefaultModel string
	Endpoint     string
	Images       bool
	LongContext  bool
	Compression  string
	Attachments  []Attachment
}

func (o Options) resolve(request Object) (Options, string, error) {
	if o.Provider == "" {
		o.Provider = os.Getenv("DECIDE_PROVIDER")
	}
	if o.Provider == "" {
		o.Provider = "official"
	}
	p, err := Lookup(o.Provider)
	if err != nil {
		return o, "", err
	}
	if o.Model == "" {
		if value, exists := request["model"]; exists {
			if !nonempty(value) {
				return o, "", fault.New(fault.Usage, "model must be a non-empty string")
			}
			o.Model = value.(string)
		} else if o.Provider == "custom" {
			o.Model = os.Getenv("DECIDE_MODEL")
		}
	}
	if o.Model == "" {
		o.Model = o.DefaultModel
	}
	if o.Model == "" {
		o.Model = p.Model
	}
	if !nonempty(o.Model) {
		return o, "", fault.New(fault.Usage, "model must be a non-empty string")
	}
	if o.Provider == "cloudflare" && (o.Model != "typesafe/jev" || strings.Contains(o.Endpoint, "/ai/run/@cf/cloudflare/clef")) {
		return o, "", fault.New(fault.Usage, "cloudflare targets Jev as typesafe/jev; for clef or clef-flash use --provider clef")
	}
	if o.Provider == "clef" && o.Model != "clef" && o.Model != "clef-flash" {
		return o, "", fault.New(fault.Usage, "clef supports only clef and clef-flash; for Jev use --provider cloudflare")
	}
	if o.Endpoint == "" {
		o.Endpoint = p.Endpoint
		switch o.Provider {
		case "custom":
			o.Endpoint = os.Getenv("DECIDE_ENDPOINT")
		case "official":
			if value := os.Getenv("TYPESAFE_API_URL"); value != "" {
				o.Endpoint = value
			}
		case "cloudflare", "clef":
			account := os.Getenv("CLOUDFLARE_ACCOUNT_ID")
			if !regexp.MustCompile(`^[a-fA-F0-9]{32}$`).MatchString(account) {
				return o, "", fault.New(fault.Usage, o.Provider+" requires CLOUDFLARE_ACCOUNT_ID with 32 hexadecimal characters")
			}
			o.Endpoint = "https://api.cloudflare.com/client/v4/accounts/" + account + "/ai/run"
			if o.Provider == "clef" {
				o.Endpoint += "/@cf/cloudflare/" + o.Model
			}
		}
	}
	if err := validateEndpoint(o.Endpoint); err != nil {
		return o, "", err
	}
	if o.Compression == "" {
		o.Compression = "none"
	}
	if err := validateCompression(o.Compression, p.CapabilitiesFor(o.Model)); err != nil {
		return o, "", err
	}
	return o, p.KeyEnv, nil
}

// CapabilitiesFor keeps a pinned older model from inheriting newer model features.
func (p Provider) CapabilitiesFor(model string) Capabilities {
	c := p.Capabilities
	if c.Compression == nil {
		c.Compression = []string{}
	}
	switch model {
	case "jev-1.13", "jev-1.13.0", "typesafe/jev-1.13", "typesafe-ai/jev-1.13.0", "typesafe-jev-1.13.0":
		c.Box = false
	}
	return c
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fault.New(fault.Usage, "endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fault.New(fault.Usage, "HTTP endpoints are allowed only on loopback; use HTTPS")
		}
	}
	return nil
}
