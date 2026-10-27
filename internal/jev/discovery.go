package jev

// Describe reports local capabilities without loading a key or contacting an API.
// Published capabilities are not proof of live model availability or account access.
func (c Client) Describe(options Options) (Object, error) {
	o, err := c.Configuration(options)
	if err != nil {
		return nil, err
	}
	provider, _ := Lookup(o.Provider)
	capabilities := provider.CapabilitiesFor(o.Model)
	questions := []string{"noul", "choice", "score"}
	if capabilities.Box {
		questions = append(questions, "box")
	}
	return Object{
		"provider": o.Provider, "family": provider.Family, "model": o.Model,
		"capabilities": capabilities, "questionTypes": questions,
		"permissions": Object{"experimentalImages": o.Images, "longContext": o.LongContext, "compression": o.Compression},
		"available":   Object{"images": capabilities.Images != "none" && o.Images, "box": capabilities.Box && o.Images, "longContext": capabilities.LongContext && o.LongContext},
		"limits":      Object{"bodyBytes": MaxBytes, "imageBytes": MaxImageBytes, "contentDepth": 64},
		"verified":    false,
	}, nil
}
