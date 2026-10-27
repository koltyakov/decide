// Package testutil provides local API fixtures. Tests never call live providers.
package testutil

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/jev"
)

func PNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func Client(t *testing.T) jev.Client {
	t.Helper()
	t.Setenv("DECIDE_PROVIDER", "")
	t.Setenv("DECIDE_MODEL", "")
	for _, p := range jev.Providers() {
		t.Setenv(p.KeyEnv, "test-secret")
	}
	path := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("DECIDE_CREDENTIALS_FILE", path)
	t.Setenv("DECIDE_ENDPOINT", "")
	t.Setenv("TYPESAFE_API_URL", "")
	return jev.Client{Credentials: credentials.Store{Path: path}, Version: "test"}
}

func API(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func Request(t *testing.T, r *http.Request) (jev.Object, [][]byte) {
	t.Helper()
	var data []byte
	var images [][]byte
	if len(r.Header.Get("Content-Type")) >= 10 && r.Header.Get("Content-Type")[:10] == "multipart/" {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return nil, nil
		}
		for i := 0; ; i++ {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return nil, nil
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
			}
			if i == 0 {
				if part.FormName() != "request" || part.Header.Get("Content-Type") != "application/json" {
					t.Error("first multipart part must be the JSON request")
				}
				data = b
			} else {
				images = append(images, b)
			}
		}
	} else {
		var err error
		data, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
	}
	var request jev.Object
	if err := jev.Decode(data, &request); err != nil {
		t.Error(err)
	}
	return request, images
}

func Reply(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
