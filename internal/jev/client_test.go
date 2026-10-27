package jev_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestProviders(t *testing.T) {
	for _, provider := range jev.ProviderNames() {
		t.Run(provider, func(t *testing.T) {
			client := testutil.Client(t)
			request := jev.Question("noul", jev.Object{"message": "today", "id": json.Number("9007199254740993")}, "Urgent?", nil)
			request["model"] = "request-model"
			if provider == "clef" {
				request["model"] = "clef"
			}
			if provider == "cloudflare" {
				request["model"] = "typesafe/jev"
			}
			request["future"] = jev.Object{"keep": true}
			before, _ := json.Marshal(request)
			s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				if r.UserAgent() != "decide/test" {
					t.Error("wrong client identity", r.UserAgent())
				}
				if r.Header.Get("Authorization") != "Bearer test-secret" || r.Method != http.MethodPost {
					t.Error("missing authorization or wrong method")
				}
				payload, _ := testutil.Request(t, r)
				if provider == "cloudflare" {
					if payload["model"] != "typesafe/jev" {
						t.Error("missing Cloudflare model route")
					}
					payload = payload["input"].(map[string]any)
					if payload["model"] != nil {
						t.Error("Cloudflare route leaked into native Jev model field")
					}
				}
				if payload["future"] == nil || payload["state"].(map[string]any)["id"] != json.Number("9007199254740993") {
					t.Error("request fields or exact numbers lost")
				}
				q := payload["questions"].(map[string]any)["answer"].(map[string]any)
				answer := jev.Object{"noul": 0.8, "type": "noul"}
				if provider == "vercel" {
					if payload["model"] != nil || q["type"] != "boolean" || r.Header.Get("ai-model-id") != "request-model" || r.Header.Get("ai-evaluation-model-specification-version") != "4" {
						t.Error("invalid Vercel translation")
					}
					answer = jev.Object{"type": "boolean", "probability": 0.8}
				} else if q["type"] != "noul" || (provider != "cloudflare" && payload["model"] != request["model"]) {
					t.Error("invalid native request")
				}
				response := jev.Object{"answers": jev.Object{"answer": answer}, "usage": jev.Object{"input_tokens": 10}}
				if provider == "cloudflare" || provider == "clef" {
					response = jev.Object{"success": true, "result": response}
				}
				testutil.Reply(t, w, response)
			})
			result, err := client.Evaluate(context.Background(), request, jev.Options{Provider: provider, Endpoint: s.URL})
			if err != nil {
				t.Fatal(err)
			}
			if result["answers"].(map[string]any)["answer"].(map[string]any)["noul"] != json.Number("0.8") {
				t.Error("answer not normalized")
			}
			after, _ := json.Marshal(request)
			if !bytes.Equal(before, after) {
				t.Error("client mutated the caller's request")
			}
		})
	}
}

func TestFailuresNeverRetryOrLeakBody(t *testing.T) {
	for _, tt := range []struct{ status, code int }{{400, 1}, {401, 3}, {403, 3}, {408, 4}, {429, 4}, {500, 4}, {529, 4}, {307, 1}} {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			client := testutil.Client(t)
			var calls atomic.Int32
			s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("private-state test-secret"))
			})
			_, err := client.Evaluate(context.Background(), jev.Question("noul", "private-state", "Q?", nil), jev.Options{Provider: "official", Endpoint: s.URL})
			if err == nil || fault.Code(err) != tt.code || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-state") || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestResponsesFailClosed(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{}`, `{"answers":{}}`, `{"answers":{"answer":{"noul":2}}}`, `{"answers":{"answer":{"noul":"0.5"}}}`, `{"answers":{"answer":{"noul":0.8,"type":"choice"}}}`, `{"answers":{"answer":{"noul":0.8}}} {}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			client := testutil.Client(t)
			s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			result, err := client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Endpoint: s.URL})
			if err == nil || result != nil {
				t.Fatalf("accepted malformed response: %v", result)
			}
		})
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	client := testutil.Client(t)
	client.Timeout = 20 * time.Millisecond
	s := testutil.API(t, func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	_, err := client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Endpoint: s.URL})
	if err == nil || fault.Code(err) != fault.Remote {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Evaluate(ctx, jev.Question("noul", "state", "Q?", nil), jev.Options{Endpoint: s.URL})
	if err == nil || fault.Code(err) != fault.Remote {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestImageMultipartBox(t *testing.T) {
	client := testutil.Client(t)
	png := testutil.PNG(t, 280, 300)
	attachment, err := jev.LoadImage("unique-photo-ref", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		payload, images := testutil.Request(t, r)
		if len(images) != 1 || !bytes.Equal(images[0], png) || payload["assets"].(map[string]any)["unique-photo-ref"] == nil {
			t.Error("missing multipart image/asset")
		}
		if r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" || r.Header.Get("TypeSafe-Danger-Zone-Long-Context") != "true" {
			t.Error("missing opt-in headers")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"type": "box", "box": jev.Object{"top": 0.1, "bottom": 0.9, "left": 0.2, "right": 0.8}, "confidence": nil}}})
	})
	request := jev.Question("box", jev.Object{"photo": attachment.Reference}, "Find the mug in photo", nil)
	o := jev.Options{Endpoint: s.URL, Images: true, LongContext: true, Attachments: []jev.Attachment{attachment}}
	result, err := client.Evaluate(context.Background(), request, o)
	if err != nil || result == nil {
		t.Fatalf("multipart failed: %v", err)
	}
	o.Images = false
	if _, err = client.Evaluate(context.Background(), request, o); fault.Code(err) != fault.Usage {
		t.Fatalf("missing opt-in accepted: %v", err)
	}
}

func TestInlineImagesAndAssetReferences(t *testing.T) {
	client := testutil.Client(t)
	png := testutil.PNG(t, 280, 280)
	request := jev.Question("noul", "photo-ref", "A person?", jev.Object{"true": jev.Object{"definition": "Person visible"}, "false": "Empty"})
	request["assets"] = jev.Object{"photo-ref": jev.Object{"format": "png", "data_base64": base64.StdEncoding.EncodeToString(png)}}
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		payload, images := testutil.Request(t, r)
		if len(images) != 0 || !reflect.DeepEqual(payload["assets"], request["assets"]) {
			t.Error("inline asset changed")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"noul": 0.3}}})
	})
	if _, err := client.Evaluate(context.Background(), request, jev.Options{Images: true, Endpoint: s.URL}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []any{jev.Object{"photo-ref": "only a key"}, "unrelated"} {
		request["state"] = state
		if _, err := client.Evaluate(context.Background(), request, jev.Options{Images: true, Endpoint: s.URL}); fault.Code(err) != fault.Usage {
			t.Fatalf("unreferenced asset accepted: %v", err)
		}
	}
}

func TestImageValidation(t *testing.T) {
	for _, b := range [][]byte{[]byte("not an image"), testutil.PNG(t, 255, 280), testutil.PNG(t, 280, 1569), make([]byte, jev.MaxImageBytes+1)} {
		if _, err := jev.LoadImage("ref", bytes.NewReader(b)); fault.Code(err) != fault.Usage {
			t.Errorf("invalid image accepted: %v", err)
		}
	}
}

func TestModelDiscoveryAndOverride(t *testing.T) {
	client := testutil.Client(t)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/v1/models" {
				t.Error(r.URL.Path)
			}
			testutil.Reply(t, w, jev.Object{"models": []any{jev.Object{"name": "jev-latest"}}})
			return
		}
		payload, _ := testutil.Request(t, r)
		if payload["model"] != "override" {
			t.Error("explicit model did not override request model")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"noul": 0.2}}})
	})
	options := jev.Options{Endpoint: s.URL + "/v1/systemone", Model: "override"}
	if _, err := client.Models(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	request := jev.Question("noul", "state", "Q?", nil)
	request["model"] = "request-model"
	if _, err := client.Evaluate(context.Background(), request, options); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeEndpointsAndCloudflareModels(t *testing.T) {
	client := testutil.Client(t)
	for _, endpoint := range []string{"http://example.com/api", "file:///key", "https://user:pass@example.com", "https://example.com/?key=secret", "https://example.com/#fragment", ""} {
		_, err := client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: "custom", Endpoint: endpoint})
		if fault.Code(err) != fault.Usage {
			t.Errorf("unsafe endpoint accepted: %q %v", endpoint, err)
		}
	}
	_, err := client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: "cloudflare", Model: "unknown", Endpoint: "http://127.0.0.1"})
	if fault.Code(err) != fault.Usage {
		t.Fatal(err)
	}
}

func TestSavedConnectionPrecedence(t *testing.T) {
	client := testutil.Client(t)
	if err := client.Credentials.SaveConnection("custom", "saved-key", credentials.Connection{Endpoint: "https://saved.example.com/v1/systemone", Model: "saved-model"}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, flagModel, requestModel, envModel, expected string }{
		{"flag wins", "flag-model", "request-model", "env-model", "flag-model"},
		{"request wins", "", "request-model", "env-model", "request-model"},
		{"environment wins", "", "", "env-model", "env-model"},
		{"saved default", "", "", "", "saved-model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DECIDE_MODEL", tt.envModel)
			request := jev.Question("noul", "state", "Q?", nil)
			if tt.requestModel != "" {
				request["model"] = tt.requestModel
			}
			o, err := client.Resolve(jev.Options{Model: tt.flagModel})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := jev.Prepare(request, o)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Preview()["model"] != tt.expected || prepared.Preview()["provider"] != "custom" {
				t.Fatal(prepared.Preview())
			}
		})
	}
	t.Setenv("DECIDE_PROVIDER", "openrouter")
	o, err := client.Resolve(jev.Options{})
	if err != nil || o.Provider != "openrouter" {
		t.Fatalf("%v %v", o, err)
	}
	t.Setenv("DECIDE_ENDPOINT", "https://env.example.com/v1/systemone")
	o, err = client.Resolve(jev.Options{Provider: "custom"})
	if err != nil || o.Endpoint != "https://env.example.com/v1/systemone" {
		t.Fatalf("%v %v", o, err)
	}
	o, err = client.Resolve(jev.Options{Provider: "custom", Endpoint: "https://flag.example.com/v1/systemone"})
	if err != nil || o.Endpoint != "https://flag.example.com/v1/systemone" {
		t.Fatalf("%v %v", o, err)
	}
}
