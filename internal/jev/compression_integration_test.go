package jev_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestCompressedRequestsUsePreflightEncoder(t *testing.T) {
	for _, provider := range []string{"official", "custom"} {
		for _, mode := range []string{"gzip", "x-gzip", "zstd"} {
			for _, multipart := range []bool{false, true} {
				t.Run(provider+"/"+mode+"/"+map[bool]string{false: "json", true: "multipart"}[multipart], func(t *testing.T) {
					client := testutil.Client(t)
					request := jev.Question("noul", strings.Repeat("evidence ", 1000), "Q?", nil)
					options := jev.Options{Provider: provider, Compression: mode}
					var png []byte
					if multipart {
						png = testutil.PNG(t, 280, 280)
						image, err := jev.LoadImage("photo-ref", bytes.NewReader(png))
						if err != nil {
							t.Fatal(err)
						}
						options.Images, options.Attachments = true, []jev.Attachment{image}
						request["state"] = "photo-ref"
					}
					var preview jev.Object
					s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Content-Encoding") != mode {
							t.Error("missing compression header")
						}
						encoded, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						if len(encoded) != preview["bodyBytes"] {
							t.Error("preflight body size differs from execution")
						}
						var decoded []byte
						if mode == "zstd" {
							reader, err := zstd.NewReader(bytes.NewReader(encoded), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(8<<20))
							if err != nil {
								t.Error(err)
								return
							}
							decoded, err = io.ReadAll(reader)
							reader.Close()
							if err != nil {
								t.Error(err)
								return
							}
						} else {
							reader, err := gzip.NewReader(bytes.NewReader(encoded))
							if err != nil {
								t.Error(err)
								return
							}
							decoded, err = io.ReadAll(reader)
							if closeErr := reader.Close(); closeErr != nil {
								t.Error(closeErr)
							}
							if err != nil {
								t.Error(err)
								return
							}
						}
						if len(decoded) != preview["uncompressedBodyBytes"] {
							t.Error("wrong uncompressed size")
						}
						r.Body = io.NopCloser(bytes.NewReader(decoded))
						payload, images := testutil.Request(t, r)
						if payload["state"] != request["state"] || (multipart && (len(images) != 1 || !bytes.Equal(images[0], png))) {
							t.Error("native payload changed")
						}
						testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"noul": 0.8}}})
					})
					options.Endpoint = s.URL
					prepared, err := jev.Prepare(request, options)
					if err != nil {
						t.Fatal(err)
					}
					preview = prepared.Preview()
					if _, err = client.Execute(context.Background(), prepared); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestCompressionRejectedLocallyAndNeverRetried(t *testing.T) {
	client := testutil.Client(t)
	var calls atomic.Int32
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(415)
	})
	for _, provider := range jev.ProviderNames() {
		if provider == "official" || provider == "custom" {
			continue
		}
		_, err := client.Evaluate(context.Background(), jev.Question("noul", "x", "Q?", nil), jev.Options{Provider: provider, Endpoint: s.URL, Compression: "gzip"})
		if fault.Code(err) != fault.Usage {
			t.Fatalf("%s compression accepted: %v", provider, err)
		}
	}
	for _, mode := range []string{"brotli", "gzip,zstd", "GZIP"} {
		_, err := client.Evaluate(context.Background(), jev.Question("noul", "x", "Q?", nil), jev.Options{Endpoint: s.URL, Compression: mode})
		if fault.Code(err) != fault.Usage {
			t.Fatal("invalid compression accepted", mode, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid compression reached the API")
	}
	_, err := client.Evaluate(context.Background(), jev.Question("noul", "x", "Q?", nil), jev.Options{Endpoint: s.URL, Compression: "zstd"})
	if fault.Code(err) != fault.Failure || calls.Load() != 1 {
		t.Fatal("compression failure retried or silently downgraded", err)
	}
}

func TestDescribeRespectsPinsAndPermissionsWithoutKeys(t *testing.T) {
	client := testutil.Client(t)
	for _, provider := range jev.Providers() {
		t.Setenv(provider.KeyEnv, "")
	}
	for _, tt := range []struct {
		model       string
		images, box bool
	}{
		{"jev-latest", false, false}, {"jev-latest", true, true}, {"jev-1.13.0", true, false},
	} {
		result, err := client.Describe(jev.Options{Model: tt.model, Images: tt.images})
		if err != nil {
			t.Fatal(err)
		}
		if result["available"].(map[string]any)["box"] != tt.box || result["verified"] != false || result["model"] != tt.model {
			t.Fatal(result)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "test-secret") || strings.Contains(string(encoded), "credentials.json") {
			t.Fatal("private configuration leaked")
		}
	}
}
