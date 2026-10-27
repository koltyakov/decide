package jev

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
)

func openRouterImage(data, format string) (Object, error) {
	if len(data) > base64.StdEncoding.EncodedLen(MaxImageBytes) || strings.ContainsAny(data, "\r\n\t ") {
		return nil, fault.New(fault.Usage, "OpenRouter images require standard padded base64, at most 16 MiB decoded")
	}
	b, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return nil, fault.New(fault.Usage, "invalid OpenRouter image base64")
	}
	actual, err := imageFormat(b, 256)
	if err != nil {
		return nil, err
	}
	config, _, _ := image.DecodeConfig(bytes.NewReader(b))
	if config.Width*config.Height < 78400 {
		return nil, fault.New(fault.Usage, "OpenRouter Jev images require at least 78,400 pixels in total")
	}
	if format != "" && format != actual {
		return nil, fault.New(fault.Usage, "OpenRouter image data URL MIME type does not match its bytes")
	}
	return Object{"type": "image_url", "image_url": Object{"url": "data:image/" + actual + ";base64," + data}}, nil
}

// validateOpenRouterImages supports native image parts as well as CLI assets.
// Remote URLs are never fetched, and parts cannot bypass image opt-in or limits.
func validateOpenRouterImages(request Object) (bool, error) {
	found := false
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				if err := visit(item); err != nil {
					return err
				}
			}
		case map[string]any:
			if v["type"] == "image_url" {
				found = true
				part, ok := v["image_url"].(map[string]any)
				if !ok {
					return fault.New(fault.Usage, "OpenRouter image_url must contain an inline PNG/JPEG data URL")
				}
				url, _ := part["url"].(string)
				format := ""
				for _, candidate := range []string{"png", "jpeg"} {
					if strings.HasPrefix(url, "data:image/"+candidate+";base64,") {
						format = candidate
						url = strings.TrimPrefix(url, "data:image/"+candidate+";base64,")
						break
					}
				}
				if format == "" {
					return fault.New(fault.Usage, "OpenRouter Jev image_url must be an inline PNG/JPEG data URL; remote URLs are not fetched")
				}
				_, err := openRouterImage(url, format)
				return err
			}
			for _, item := range v {
				if err := visit(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(request["state"]); err != nil {
		return false, err
	}
	for _, value := range request["questions"].(map[string]any) {
		q := value.(map[string]any)
		if err := visit(q["instructions"]); err != nil {
			return false, err
		}
		switch criteria := q["criteria"].(type) {
		case map[string]any:
			// Option names are keys, not media content or image-part attributes.
			for _, item := range criteria {
				if err := visit(item); err != nil {
					return false, err
				}
			}
		case []any:
			for _, item := range criteria {
				if err := visit(item); err != nil {
					return false, err
				}
			}
		}
	}
	return found, nil
}

// OpenRouter ignores TypeSafe's assets map. Replace references with inline
// image_url parts at their native positions, including references inside strings.
func openRouterAssets(request Object) error {
	assets, _ := request["assets"].(map[string]any)
	if len(assets) == 0 {
		return nil
	}
	parts := map[string]Object{}
	costs := map[string]int{}
	for reference, value := range assets {
		asset := value.(map[string]any)
		data, _ := asset["data_base64"].(string)
		part, err := openRouterImage(data, "")
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(part)
		parts[reference] = part
		costs[reference] = len(encoded) + 8
	}
	delete(request, "assets")
	base, err := json.Marshal(request)
	if err != nil {
		return err
	}
	budget := len(base)
	charge := func(reference string) error {
		budget += costs[reference]
		if budget > MaxBytes {
			return fault.New(fault.Usage, "OpenRouter image-reference expansion exceeds the 32 MiB safety budget")
		}
		return nil
	}
	var replace func(any) (any, error)
	replace = func(value any) (any, error) {
		switch v := value.(type) {
		case string:
			if part, ok := parts[v]; ok {
				if err := charge(v); err != nil {
					return nil, err
				}
				return part, nil
			}
			remaining := v
			var result []any
			for {
				index, best := -1, ""
				for reference := range parts {
					position := strings.Index(remaining, reference)
					if position >= 0 && (index < 0 || position < index || (position == index && len(reference) > len(best))) {
						index, best = position, reference
					}
				}
				if index < 0 {
					break
				}
				if err := charge(best); err != nil {
					return nil, err
				}
				if index > 0 {
					result = append(result, remaining[:index])
				}
				result = append(result, parts[best])
				remaining = remaining[index+len(best):]
			}
			if len(result) == 0 {
				return v, nil
			}
			if remaining != "" {
				result = append(result, remaining)
			}
			return result, nil
		case []any:
			for i, item := range v {
				next, err := replace(item)
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
		case map[string]any:
			// Native image parts were already validated; never rewrite base64 URLs.
			if v["type"] == "image_url" {
				return v, nil
			}
			for key, item := range v {
				next, err := replace(item)
				if err != nil {
					return nil, err
				}
				v[key] = next
			}
		}
		return value, nil
	}
	state, err := replace(request["state"])
	if err != nil {
		return err
	}
	request["state"] = state
	for _, value := range request["questions"].(map[string]any) {
		q := value.(map[string]any)
		if item, ok := q["instructions"]; ok {
			next, err := replace(item)
			if err != nil {
				return err
			}
			q["instructions"] = next
		}
		switch criteria := q["criteria"].(type) {
		case map[string]any:
			for key, item := range criteria {
				next, err := replace(item)
				if err != nil {
					return err
				}
				criteria[key] = next
			}
		case []any:
			for i, item := range criteria {
				next, err := replace(item)
				if err != nil {
					return err
				}
				criteria[i] = next
			}
		}
	}
	return nil
}
