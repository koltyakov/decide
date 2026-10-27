package jev

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	_ "image/jpeg" // Register the API's supported image formats.
	_ "image/png"
	"io"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
)

const MaxImageBytes = 16 << 20

type Attachment struct {
	Reference string
	Format    string
	Data      []byte
}

func LoadImage(reference string, r io.Reader) (Attachment, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return Attachment{}, fault.New(fault.Usage, "cannot read image")
	}
	// Provider-specific minimum dimensions are checked during request preparation.
	format, err := imageFormat(b, 256)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{Reference: reference, Format: format, Data: b}, nil
}

func imageFormat(b []byte, minimum int) (string, error) {
	bad := func(message string) (string, error) { return "", fault.New(fault.Usage, message) }
	if len(b) > MaxImageBytes {
		return bad("image exceeds 16 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && format != "png") {
		return bad("image must be PNG or JPEG")
	}
	if config.Width < minimum || config.Height < minimum || config.Width > 1568 || config.Height > 1568 {
		return bad("image dimensions must each be " + strconv.Itoa(minimum) + " to 1568 pixels; resize before sending")
	}
	if format == "png" {
		// Reject APNG by walking chunk boundaries, not by matching compressed bytes.
		for pos := 8; pos+12 <= len(b); {
			size := uint64(binary.BigEndian.Uint32(b[pos : pos+4]))
			if string(b[pos+4:pos+8]) == "acTL" {
				return bad("animated PNG is unsupported; export a still frame")
			}
			if size > uint64(len(b)-pos-12) {
				return bad("invalid PNG chunk")
			}
			pos += int(size) + 12
		}
	}
	if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
		return bad("image is damaged or incomplete; re-export as PNG or JPEG")
	}
	return format, nil
}

// prepareAssets validates inline assets and binds explicit file attachments.
// It never interprets asset strings as filesystem paths.
func prepareAssets(request Object, attachments []Attachment, inline bool, minimum int) error {
	assets, ok := request["assets"].(map[string]any)
	if _, exists := request["assets"]; exists && (!ok || len(assets) == 0) {
		return fault.New(fault.Usage, "assets must be a non-empty object")
	}
	if assets == nil {
		assets = map[string]any{}
	}
	parts := map[string]bool{}
	for i, attachment := range attachments {
		if _, exists := assets[attachment.Reference]; exists {
			return fault.New(fault.Usage, "attachment conflicts with an existing asset reference")
		}
		// Recheck so callers cannot bypass limits by constructing Attachment directly.
		format, err := imageFormat(attachment.Data, minimum)
		if err != nil {
			return err
		}
		if inline {
			assets[attachment.Reference] = Object{"type": "image", "format": format, "data_base64": base64.StdEncoding.EncodeToString(attachment.Data)}
		} else {
			part := "image-" + strconv.Itoa(i)
			parts[part] = true
			assets[attachment.Reference] = Object{"type": "image", "part": part, "format": format}
		}
	}
	refBytes := 0
	for ref, value := range assets {
		refBytes += len(ref)
		if strings.TrimSpace(ref) == "" || len(ref) > 1024 || refBytes > 65536 {
			return fault.New(fault.Usage, "asset references exceed length limits")
		}
		asset, ok := value.(map[string]any)
		if !ok || (asset["type"] != nil && asset["type"] != "image") {
			return fault.New(fault.Usage, "asset must be an image object")
		}
		if !referenced(request["state"], ref) && !questionReference(request["questions"], ref) {
			return fault.New(fault.Usage, "every asset must be referenced in state, instructions, or criteria")
		}
		if part, exists := asset["part"]; exists {
			name, ok := part.(string)
			if !ok || !parts[name] {
				return fault.New(fault.Usage, "multipart asset has no CLI image attachment; use inline data_base64 in JSON")
			}
			delete(parts, name)
			continue
		}
		format, _ := asset["format"].(string)
		encoded, ok := asset["data_base64"].(string)
		if !ok || (format != "png" && format != "jpeg" && format != "jpg") || strings.ContainsAny(encoded, "\r\n\t ") || len(encoded) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
			return fault.New(fault.Usage, "inline image requires png/jpg/jpeg format and standard padded data_base64")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return fault.New(fault.Usage, "invalid image base64")
		}
		if _, err = imageFormat(data, minimum); err != nil {
			return err
		}
	}
	if len(assets) > 0 {
		request["assets"] = assets
	}
	return nil
}

func questionReference(value any, ref string) bool {
	questions, _ := value.(map[string]any)
	for _, v := range questions {
		q, _ := v.(map[string]any)
		if referenced(q["instructions"], ref) || referenced(q["criteria"], ref) {
			return true
		}
	}
	return false
}

func referenced(value any, ref string) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, ref)
	case []any:
		for _, item := range v {
			if referenced(item, ref) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if referenced(item, ref) {
				return true
			}
		}
	}
	return false
}

// multipartBody writes bounded uploads without an unowned pipe goroutine.
func multipartBody(request []byte, attachments []Attachment) ([]byte, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="request"`)
	header.Set("Content-Type", "application/json")
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err = part.Write(request); err != nil {
		return nil, "", err
	}
	for i, attachment := range attachments {
		if b.Len()+len(attachment.Data)+1024 > MaxBytes {
			return nil, "", fault.New(fault.Usage, "multipart request exceeds 32 MiB")
		}
		header = textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="image-`+strconv.Itoa(i)+`"; filename="image.`+attachment.Format+`"`)
		header.Set("Content-Type", "image/"+attachment.Format)
		part, err = w.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(attachment.Data); err != nil {
			return nil, "", err
		}
	}
	if err = w.Close(); err != nil {
		return nil, "", err
	}
	if b.Len() > MaxBytes {
		return nil, "", fault.New(fault.Usage, "multipart request exceeds 32 MiB")
	}
	return b.Bytes(), w.FormDataContentType(), nil
}
