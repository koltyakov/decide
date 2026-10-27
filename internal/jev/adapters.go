package jev

import (
	"net/http"

	"github.com/koltyakov/decide/internal/fault"
)

func validateProvider(request Object, o Options, capabilities Capabilities) error {
	bad := func(message string) error { return fault.New(fault.Usage, o.Provider+": "+message) }
	hasImages := request["assets"] != nil || len(o.Attachments) > 0
	if o.Provider == "openrouter" {
		nativeImages, err := validateOpenRouterImages(request)
		if err != nil {
			return err
		}
		hasImages = hasImages || nativeImages
	}
	for _, value := range request["questions"].(map[string]any) {
		q := value.(map[string]any)
		if q["type"] == "box" {
			if !capabilities.Box {
				return bad("box is not supported by this provider/model")
			}
			if !hasImages {
				return bad("box requires an image asset")
			}
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
		if capabilities.Images == "none" {
			return bad("TypeSafe image assets are not supported by this provider/model")
		}
		if !o.Images {
			return bad("images and box are experimental; pass --experimental-images to opt in")
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

func providerRequest(payload Object, o Options) (Object, http.Header, error) {
	headers := http.Header{}
	if (o.Provider == "official" || o.Provider == "custom") && payload["assets"] != nil {
		headers.Set("TypeSafe-Danger-Zone-Images", "true")
	}
	if o.LongContext {
		headers.Set("TypeSafe-Danger-Zone-Long-Context", "true")
	}
	switch o.Provider {
	case "openrouter":
		if err := openRouterAssets(payload); err != nil {
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
			q := Object{}
			for key, field := range value.(map[string]any) {
				q[key] = field
			}
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
