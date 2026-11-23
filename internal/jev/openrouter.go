package jev

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
)

var openRouterRules = imageRules{inline: true, minimum: 256, minPixels: 78400}

func openRouterImage(data, format string) error {
	if len(data) > base64.StdEncoding.EncodedLen(MaxImageBytes) || strings.ContainsAny(data, "\r\n\t ") {
		return fault.New(fault.Usage, "OpenRouter images require standard padded base64, at most 16 MiB decoded")
	}
	b, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return fault.New(fault.Usage, "invalid OpenRouter image base64")
	}
	info, err := checkImage(b, openRouterRules)
	if err != nil {
		return err
	}
	if format != info.format {
		return fault.New(fault.Usage, "OpenRouter image data URL MIME type does not match its bytes")
	}
	return nil
}

func imagePart(format, data string) Object {
	return Object{"type": "image_url", "image_url": Object{"url": "data:image/" + format + ";base64," + data}}
}

// nativeImages validates OpenRouter image parts inside one content value.
// Remote URLs are never fetched, and parts cannot bypass image opt-in or limits.
func nativeImages(value any) (bool, error) {
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
				for _, candidate := range []string{"png", "jpeg"} {
					if data, ok := strings.CutPrefix(url, "data:image/"+candidate+";base64,"); ok {
						return openRouterImage(data, candidate)
					}
				}
				return fault.New(fault.Usage, "OpenRouter Jev image_url must be an inline PNG/JPEG data URL; remote URLs are not fetched")
			}
			for _, item := range v {
				if err := visit(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := visit(value)
	return found, err
}

// questionImages validates native parts in instructions and criterion values.
// Option names are keys, not media content or image-part attributes.
func questionImages(questions any) (bool, error) {
	found := false
	for _, value := range questionValues(questions) {
		var items []any
		switch criteria := value.(type) {
		case map[string]any:
			for _, item := range criteria {
				items = append(items, item)
			}
		case []any:
			items = criteria
		default:
			items = []any{value}
		}
		for _, item := range items {
			ok, err := nativeImages(item)
			if err != nil {
				return false, err
			}
			found = found || ok
		}
	}
	return found, nil
}

// OpenRouter ignores TypeSafe's assets map. Replace references with inline
// image_url parts at their native positions, including whole-token references
// inside strings. The request and its nested values are never mutated; changed
// containers are copied, so a shared recipe stays reusable.
func openRouterAssets(request Object, formats map[string]string) (Object, error) {
	assets, _ := request["assets"].(map[string]any)
	if len(assets) == 0 {
		return request, nil
	}
	parts := map[string]Object{}
	costs := map[string]int{}
	refs := make([]string, 0, len(assets))
	for reference, value := range assets {
		data, _ := value.(map[string]any)["data_base64"].(string)
		frame, _ := json.Marshal(imagePart(formats[reference], ""))
		parts[reference] = imagePart(formats[reference], data)
		// Base64 needs no JSON escaping, so the encoded size is exact.
		costs[reference] = len(frame) + len(data) + 8
		refs = append(refs, reference)
	}
	matcher := newReferenceMatcher(refs)
	request = maps.Clone(request)
	delete(request, "assets")
	base, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	budget := len(base)
	charge := func(reference string) error {
		budget += costs[reference]
		if budget > MaxBytes {
			return fault.New(fault.Usage, "OpenRouter image-reference expansion exceeds the 32 MiB safety budget")
		}
		return nil
	}
	var replace func(any) (any, bool, error)
	replace = func(value any) (any, bool, error) {
		switch v := value.(type) {
		case string:
			if part, ok := parts[v]; ok {
				return part, true, charge(v)
			}
			var result []any
			last := 0
			for at, ref := matcher.next(v, 0); at >= 0; at, ref = matcher.next(v, last) {
				if err := charge(ref); err != nil {
					return nil, false, err
				}
				if at > last {
					result = append(result, v[last:at])
				}
				result = append(result, parts[ref])
				last = at + len(ref)
			}
			if result == nil {
				return v, false, nil
			}
			if last < len(v) {
				result = append(result, v[last:])
			}
			return result, true, nil
		case []any:
			var out []any
			for i, item := range v {
				next, changed, err := replace(item)
				if err != nil {
					return nil, false, err
				}
				if changed && out == nil {
					out = append([]any(nil), v...)
				}
				if out != nil {
					out[i] = next
				}
			}
			if out == nil {
				return v, false, nil
			}
			return out, true, nil
		case map[string]any:
			// Native image parts were already validated; never rewrite base64 URLs.
			if v["type"] == "image_url" {
				return v, false, nil
			}
			return replaceMembers(v, replace)
		}
		return value, false, nil
	}
	if state, changed, err := replace(request["state"]); err != nil {
		return nil, err
	} else if changed {
		request["state"] = state
	}
	questions := map[string]any{}
	for name, value := range request["questions"].(map[string]any) {
		q := value.(map[string]any)
		updated, cloned := q, false
		set := func(key string, next any) {
			if !cloned {
				updated, cloned = maps.Clone(q), true
			}
			updated[key] = next
		}
		if item, ok := q["instructions"]; ok {
			next, changed, err := replace(item)
			if err != nil {
				return nil, err
			}
			if changed {
				set("instructions", next)
			}
		}
		var criteria any
		changed := false
		switch c := q["criteria"].(type) {
		case map[string]any:
			criteria, changed, err = replaceMembers(c, replace)
		case []any:
			criteria, changed, err = replace(c)
		}
		if err != nil {
			return nil, err
		}
		if changed {
			set("criteria", criteria)
		}
		questions[name] = updated
	}
	request["questions"] = questions
	return request, nil
}

func replaceMembers(v map[string]any, replace func(any) (any, bool, error)) (any, bool, error) {
	var out map[string]any
	for key, item := range v {
		next, changed, err := replace(item)
		if err != nil {
			return nil, false, err
		}
		if changed {
			if out == nil {
				out = maps.Clone(v)
			}
			out[key] = next
		}
	}
	if out == nil {
		return v, false, nil
	}
	return out, true, nil
}
