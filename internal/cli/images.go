package cli

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"golang.org/x/term"
)

func imageUsesStdin(images []string) bool {
	for _, value := range images {
		_, path, ok := strings.Cut(value, "=")
		if ok && path == "-" {
			return true
		}
	}
	return false
}

func (a *app) images(values []string, generated bool) ([]jev.Attachment, jev.Object, error) {
	if len(values) > 0 && !a.options.Images {
		return nil, nil, fault.New(fault.Usage, "images are experimental; pass --experimental-images to opt in")
	}
	state := jev.Object{}
	var attachments []jev.Attachment
	usedStdin := false
	total := 0
	for _, value := range values {
		name, path, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(name) == "" || path == "" {
			return nil, nil, fault.New(fault.Usage, "image must use NAME=PATH or NAME=-")
		}
		if _, exists := state[name]; exists {
			return nil, nil, fault.New(fault.Usage, "duplicate image name")
		}
		ref := name
		if generated {
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				return nil, nil, err
			}
			ref = "decide-image-" + hex.EncodeToString(id[:])
		}
		r := a.in
		var f *os.File
		if path == "-" {
			if f, ok := a.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				return nil, nil, fault.New(fault.Usage, "image stdin must be piped PNG/JPEG bytes")
			}
			if usedStdin {
				return nil, nil, fault.New(fault.Usage, "only one image can read stdin")
			}
			usedStdin = true
		} else {
			var err error
			f, err = os.Open(path)
			if err != nil {
				return nil, nil, fault.New(fault.Usage, "cannot open image file")
			}
			r = f
		}
		attachment, err := jev.LoadImage(ref, r)
		if f != nil {
			_ = f.Close()
		}
		if err != nil {
			return nil, nil, err
		}
		total += len(attachment.Data)
		if total > jev.MaxBytes {
			return nil, nil, fault.New(fault.Usage, "image files together exceed 32 MiB")
		}
		state[name] = ref
		attachments = append(attachments, attachment)
	}
	return attachments, state, nil
}
