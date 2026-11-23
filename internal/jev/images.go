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

const (
	MaxImageBytes = 16 << 20
	// MaxAssets bounds reference scanning and expansion work per request.
	MaxAssets = 256
)

type Attachment struct {
	Reference string
	Format    string
	Data      []byte
}

type imageInfo struct {
	format        string
	width, height int
}

// imageRules carries provider-specific image limits checked during preparation.
type imageRules struct {
	inline             bool
	minimum, minPixels int
}

// LoadImage reads bounded bytes and checks format, dimensions, and animation.
// Pixel decoding happens once, during request preparation.
func LoadImage(reference string, r io.Reader) (Attachment, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxImageBytes+1))
	if err != nil {
		return Attachment{}, fault.New(fault.Usage, "cannot read image")
	}
	// Provider-specific minimum dimensions are checked during request preparation.
	info, err := imageHeader(b, 256)
	if err != nil {
		return Attachment{}, err
	}
	return Attachment{Reference: reference, Format: info.format, Data: b}, nil
}

// imageHeader validates everything except pixel data.
func imageHeader(b []byte, minimum int) (imageInfo, error) {
	bad := func(message string) (imageInfo, error) { return imageInfo{}, fault.New(fault.Usage, message) }
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
	return imageInfo{format: format, width: config.Width, height: config.Height}, nil
}

// checkImage applies every image rule, including one full pixel decode.
func checkImage(b []byte, rules imageRules) (imageInfo, error) {
	info, err := imageHeader(b, rules.minimum)
	if err != nil {
		return info, err
	}
	if info.width*info.height < rules.minPixels {
		return info, fault.New(fault.Usage, "OpenRouter Jev images require at least 78,400 pixels in total")
	}
	if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
		return info, fault.New(fault.Usage, "image is damaged or incomplete; re-export as PNG or JPEG")
	}
	return info, nil
}

// bindAssets validates inline assets, binds explicit file attachments, and
// returns each reference's detected format. Every image is decoded exactly once.
// It never interprets asset strings as filesystem paths.
func bindAssets(request Object, attachments []Attachment, rules imageRules) (map[string]string, error) {
	assets, ok := request["assets"].(map[string]any)
	if _, exists := request["assets"]; exists && (!ok || len(assets) == 0) {
		return nil, fault.New(fault.Usage, "assets must be a non-empty object")
	}
	if len(assets)+len(attachments) > MaxAssets {
		return nil, fault.New(fault.Usage, "a request can contain at most "+strconv.Itoa(MaxAssets)+" image assets")
	}
	if assets == nil {
		assets = map[string]any{}
	}
	formats := map[string]string{}
	parts := map[string]bool{}
	for i, attachment := range attachments {
		if _, exists := assets[attachment.Reference]; exists {
			return nil, fault.New(fault.Usage, "attachment conflicts with an existing asset reference")
		}
		// Recheck so callers cannot bypass limits by constructing Attachment directly.
		info, err := checkImage(attachment.Data, rules)
		if err != nil {
			return nil, err
		}
		formats[attachment.Reference] = info.format
		if rules.inline {
			assets[attachment.Reference] = Object{"type": "image", "format": info.format, "data_base64": base64.StdEncoding.EncodeToString(attachment.Data)}
		} else {
			part := "image-" + strconv.Itoa(i)
			parts[part] = true
			assets[attachment.Reference] = Object{"type": "image", "part": part, "format": info.format}
		}
	}
	refBytes := 0
	for ref, value := range assets {
		refBytes += len(ref)
		if strings.TrimSpace(ref) == "" || len(ref) > 1024 || refBytes > 65536 {
			return nil, fault.New(fault.Usage, "asset references exceed length limits")
		}
		asset, ok := value.(map[string]any)
		if !ok || (asset["type"] != nil && asset["type"] != "image") {
			return nil, fault.New(fault.Usage, "asset must be an image object")
		}
		if part, exists := asset["part"]; exists {
			name, ok := part.(string)
			if !ok || !parts[name] {
				return nil, fault.New(fault.Usage, "multipart asset has no CLI image attachment; use inline data_base64 in JSON")
			}
			delete(parts, name)
			continue
		}
		if _, done := formats[ref]; done {
			continue // Built above from an attachment that was already decoded.
		}
		format, _ := asset["format"].(string)
		encoded, ok := asset["data_base64"].(string)
		if !ok || (format != "png" && format != "jpeg" && format != "jpg") || strings.ContainsAny(encoded, "\r\n\t ") || len(encoded) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
			return nil, fault.New(fault.Usage, "inline image requires png/jpg/jpeg format and standard padded data_base64")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, fault.New(fault.Usage, "invalid image base64")
		}
		info, err := checkImage(data, rules)
		if err != nil {
			return nil, err
		}
		formats[ref] = info.format
	}
	if len(assets) > 0 {
		request["assets"] = assets
	}
	return formats, nil
}

// multipartBody writes bounded uploads without an unowned pipe goroutine.
func multipartBody(request []byte, attachments []Attachment) ([]byte, string, error) {
	var b bytes.Buffer
	size := len(request) + 1024
	for _, attachment := range attachments {
		size += len(attachment.Data) + 1024
	}
	if size <= MaxBytes {
		b.Grow(size)
	}
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
