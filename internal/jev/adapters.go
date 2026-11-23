package jev

import (
	"maps"
	"net/http"

	"github.com/koltyakov/decide/internal/fault"
)

// validateProvider checks state-independent provider rules. hasImages covers
// assets, attachments, and native parts outside the state.
func validateProvider(request Object, o Options, capabilities Capabilities, hasImages bool) error {
	bad := func(message string) error { return fault.New(fault.Usage, o.Provider+": "+message) }
	for _, value := range request["questions"].(map[string]any) {
		q := value.(map[string]any)
		if q["type"] == "box" && !capabilities.Box {
			return bad("box is not supported by this provider/model")
		}
		if o.Provider == "aimlapi" {
			if !content(q["instructions"], false) {
				return bad("each question requires text or structured instructions")
			}
			if q["type"] == "score" && len(q["criteria"].([]any)) < 2 {
				return bad("score requires at least two criteria levels")
			}
		}
	}
	if hasImages {
		if err := validateImages(o, capabilities, true, false); err != nil {
			return err
		}
	}
	if o.LongContext && !capabilities.LongContext {
		return bad("the TypeSafe --long-context opt-in is not supported; this provider has its own context limit")
	}
	if o.Provider == "digitalocean" || o.Provider == "cloudflare" || o.Provider == "aimlapi" || o.Provider == "openrouter" {
		if value, exists := request["images"]; exists {
			images, ok := value.([]any)
			if o.Provider != "aimlapi" || !ok || len(images) > 0 {
				return bad("top-level images are not supported; use referenced assets where the provider supports them")
			}
		}
	}
	return nil
}

// validateImages checks image permissions once the state's images are known.
func validateImages(o Options, capabilities Capabilities, hasImages, hasBox bool) error {
	bad := func(message string) error { return fault.New(fault.Usage, o.Provider+": "+message) }
	if hasBox && !hasImages {
		return bad("box requires an image asset")
	}
	if hasImages {
		if capabilities.Images == "none" {
			return bad("TypeSafe image assets are not supported by this provider/model")
		}
		if !o.Images {
			return bad("images and box are experimental; pass --experimental-images to opt in")
		}
	}
	return nil
}

// providerRequest translates a shallow payload copy. Nested values are shared
// with the recipe, so translations copy what they change.
func providerRequest(payload Object, o Options, formats map[string]string) (Object, http.Header, error) {
	headers := http.Header{}
	// TypeSafe requires the images opt-in for assets and for Box questions.
	if (o.Provider == "official" || o.Provider == "custom") && (payload["assets"] != nil || hasBox(payload)) {
		headers.Set("TypeSafe-Danger-Zone-Images", "true")
	}
	if o.LongContext {
		headers.Set("TypeSafe-Danger-Zone-Long-Context", "true")
	}
	switch o.Provider {
	case "openrouter":
		var err error
		if payload, err = openRouterAssets(payload, formats); err != nil {
			return nil, nil, err
		}
	case "cloudflare":
		delete(payload, "model")
		payload = Object{"model": o.Model, "input": payload}
	case "vercel":
		delete(payload, "model")
		headers.Set("ai-model-id", o.Model)
		headers.Set("ai-gateway-protocol-version", "0.0.1")
		headers.Set("ai-gateway-auth-method", "api-key")
		headers.Set("ai-evaluation-model-specification-version", "4")
		translated := Object{}
		for name, value := range payload["questions"].(map[string]any) {
			q := maps.Clone(value.(map[string]any))
			if q["type"] == "noul" {
				q["type"] = "boolean"
			}
			translated[name] = q
		}
		payload["questions"] = translated
	}
	return payload, headers, nil
}

func providerResponse(result Object, provider string) (Object, error) {
	if provider != "cloudflare" && provider != "clef" {
		return result, nil
	}
	// Jev's catalog documents a native response; Workers AI also returns REST
	// envelopes. An explicit failure or malformed envelope never falls back to answers.
	_, hasSuccess := result["success"]
	_, hasResult := result["result"]
	_, hasErrors := result["errors"]
	if provider == "clef" || hasSuccess || hasResult || hasErrors {
		if result["success"] != true {
			return nil, fault.New(fault.Failure, "Cloudflare Workers AI reported failure or a malformed response")
		}
		inner, ok := result["result"].(map[string]any)
		if !ok {
			return nil, fault.New(fault.Failure, "Cloudflare Workers AI returned a malformed result")
		}
		return inner, nil
	}
	return result, nil
}

func hasBox(payload Object) bool {
	questions, _ := payload["questions"].(map[string]any)
	for _, value := range questions {
		if q, ok := value.(map[string]any); ok && q["type"] == "box" {
			return true
		}
	}
	return false
}
