package jev_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestProviderDefaultsAndRoutes(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", strings.Repeat("a", 32))
	for _, tt := range []struct{ name, model, endpoint, key, family string }{
		{"digitalocean", "typesafe-jev-1.13.0", "https://inference.do-ai.run/v1/systemone", "DIGITALOCEAN_MODEL_ACCESS_KEY", "jev"},
		{"aimlapi", "typesafe/jev", "https://api.aimlapi.com/v1/decisions", "AIMLAPI_KEY", "jev"},
		{"cloudflare", "typesafe/jev", "https://api.cloudflare.com/client/v4/accounts/" + strings.Repeat("a", 32) + "/ai/run", "CLOUDFLARE_API_TOKEN", "jev"},
		{"clef", "clef-flash", "https://api.cloudflare.com/client/v4/accounts/" + strings.Repeat("a", 32) + "/ai/run/@cf/cloudflare/clef-flash", "CLOUDFLARE_API_TOKEN", "clef"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := jev.Lookup(tt.name)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := jev.Prepare(jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: tt.name})
			if err != nil {
				t.Fatal(err)
			}
			preview := prepared.Preview()
			if preview["endpoint"] != tt.endpoint || preview["model"] != tt.model || p.KeyEnv != tt.key || p.Family != tt.family {
				t.Fatalf("provider=%+v preview=%v", p, preview)
			}
		})
	}
}

func TestNewProviderRequestsAndKeys(t *testing.T) {
	for _, name := range []string{"digitalocean", "aimlapi", "cloudflare", "clef"} {
		t.Run(name, func(t *testing.T) {
			client := testutil.Client(t)
			provider, _ := jev.Lookup(name)
			t.Setenv(provider.KeyEnv, name+"-private-key")
			s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+name+"-private-key" {
					t.Error("wrong provider credential")
				}
				payload, images := testutil.Request(t, r)
				if payload["model"] != provider.Model || len(images) != 0 {
					t.Error("wrong model or encoding")
				}
				if name == "cloudflare" {
					payload = payload["input"].(map[string]any)
					if payload["model"] != nil {
						t.Error("routing model copied into input")
					}
				}
				if payload["extra"] != true || payload["state"] != "state" {
					t.Error("native fields lost")
				}
				q := payload["questions"].(map[string]any)["answer"].(map[string]any)
				if q["type"] != "noul" || q["instructions"] != "Q?" {
					t.Error("question changed")
				}
				response := jev.Object{"model": "resolved-version", "answers": jev.Object{"answer": jev.Object{"type": "noul", "noul": 0.8}}, "usage": jev.Object{"input_tokens": 10}, "meta": jev.Object{"keep": true}}
				if name == "clef" {
					response = jev.Object{"success": true, "result": response}
				}
				testutil.Reply(t, w, response)
			})
			request := jev.Question("noul", "state", "Q?", nil)
			request["extra"] = true
			result, err := client.Evaluate(context.Background(), request, jev.Options{Provider: name, Endpoint: s.URL})
			if err != nil {
				t.Fatal(err)
			}
			if result["model"] != "resolved-version" || result["meta"].(map[string]any)["keep"] != true || result["answers"].(map[string]any)["answer"].(map[string]any)["type"] != "noul" {
				t.Fatal("native response fields changed", result)
			}
		})
	}
}

func TestAIMLAPIConvertsFileImagesToInlineAssets(t *testing.T) {
	client := testutil.Client(t)
	png := testutil.PNG(t, 256, 256)
	image, err := jev.LoadImage("photo-reference", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	request := jev.Question("box", jev.Object{"photo": "photo-reference"}, "The mug in photo", nil)
	before, _ := json.Marshal(request)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("TypeSafe-Danger-Zone-Images") != "" {
			t.Error("AIMLAPI must use native JSON without TypeSafe opt-in headers")
		}
		payload, images := testutil.Request(t, r)
		asset := payload["assets"].(map[string]any)["photo-reference"].(map[string]any)
		if payload["model"] != "typesafe/jev" || asset["part"] != nil || asset["format"] != "png" || len(images) != 0 {
			t.Error("wrong AIMLAPI image request")
		}
		decoded, err := base64.StdEncoding.DecodeString(asset["data_base64"].(string))
		if err != nil || !bytes.Equal(decoded, png) {
			t.Error("image bytes changed")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"type": "box", "box": jev.Object{"left": 0.1, "right": 0.9, "top": 0.2, "bottom": 0.8}, "confidence": 0.7}}, "meta": jev.Object{"provider": "typesafe"}})
	})
	options := jev.Options{Provider: "aimlapi", Endpoint: s.URL, Images: true, Attachments: []jev.Attachment{image}}
	if _, err = client.Evaluate(context.Background(), request, options); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(request)
	if !bytes.Equal(before, after) {
		t.Error("image conversion mutated input")
	}
	options.Provider = "official"
	if _, err = client.Evaluate(context.Background(), request, options); fault.Code(err) != fault.Usage {
		t.Fatal("official image minimum wasn't enforced", err)
	}
}

func TestCloudflareEnvelopeFailuresDoNotFallbackToAnswers(t *testing.T) {
	for _, provider := range []string{"cloudflare", "clef"} {
		for _, body := range []string{
			`{"success":false,"answers":{"answer":{"noul":0.8}},"errors":[{"message":"private-state"}]}`,
			`{"success":"true","result":{"answers":{"answer":{"noul":0.8}}}}`,
			`{"success":true,"result":[]}`,
			`{"result":{"answers":{"answer":{"noul":0.8}}}}`,
			`{"success":true,"result":{"answers":{}}}`,
		} {
			t.Run(provider+body, func(t *testing.T) {
				client := testutil.Client(t)
				s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
				result, err := client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: provider, Endpoint: s.URL})
				if err == nil || result != nil || strings.Contains(err.Error(), "private-state") {
					t.Fatalf("result=%v err=%v", result, err)
				}
			})
		}
	}
}

func TestUnsupportedProviderFeaturesFailBeforeNetwork(t *testing.T) {
	client := testutil.Client(t)
	image, err := jev.LoadImage("photo-reference", bytes.NewReader(testutil.PNG(t, 280, 280)))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s := testutil.API(t, func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) })
	for _, provider := range []string{"digitalocean", "cloudflare", "clef", "vercel"} {
		for _, kind := range []string{"noul", "box"} {
			_, err = client.Evaluate(context.Background(), jev.Question(kind, "photo-reference", "Q?", nil), jev.Options{Provider: provider, Endpoint: s.URL, Images: true, Attachments: []jev.Attachment{image}})
			if fault.Code(err) != fault.Usage {
				t.Errorf("%s %s accepted images: %v", provider, kind, err)
			}
		}
	}
	for _, provider := range []string{"digitalocean", "cloudflare", "clef", "aimlapi", "openrouter"} {
		_, err = client.Evaluate(context.Background(), jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: provider, Endpoint: s.URL, LongContext: true})
		if fault.Code(err) != fault.Usage {
			t.Errorf("%s accepted TypeSafe long context: %v", provider, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported request reached the API")
	}
}

func TestCloudflareAndClefModelSeparation(t *testing.T) {
	for _, tt := range []struct{ provider, model, endpoint string }{
		{"cloudflare", "clef", "http://127.0.0.1"}, {"cloudflare", "clef-flash", "http://127.0.0.1"},
		{"cloudflare", "typesafe/jev", "https://api.cloudflare.com/client/v4/accounts/account/ai/run/@cf/cloudflare/clef-flash"},
		{"clef", "typesafe/jev", "http://127.0.0.1"},
	} {
		_, err := jev.Prepare(jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: tt.provider, Model: tt.model, Endpoint: tt.endpoint})
		if fault.Code(err) != fault.Usage {
			t.Fatal("mixed model families accepted", tt, err)
		}
	}
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	for _, provider := range []string{"cloudflare", "clef"} {
		_, err := jev.Prepare(jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: provider})
		if fault.Code(err) != fault.Usage {
			t.Fatal("missing account accepted", err)
		}
	}
}

func TestDigitalOceanModelDiscoveryPreservesNativeCatalog(t *testing.T) {
	client := testutil.Client(t)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Error("wrong catalog request")
		}
		testutil.Reply(t, w, jev.Object{"object": "list", "data": []any{jev.Object{"id": "typesafe-jev-1.13.0", "object": "model"}}})
	})
	result, err := client.Models(context.Background(), jev.Options{Provider: "digitalocean", Endpoint: s.URL + "/v1/systemone"})
	if err != nil || result["object"] != "list" || result["data"] == nil {
		t.Fatalf("%v %v", result, err)
	}
}

func TestAIMLAPIQuestionConstraints(t *testing.T) {
	for _, request := range []jev.Object{
		{"state": "state", "questions": jev.Object{"a": jev.Object{"type": "noul"}}},
		jev.Question("score", "state", "Q?", []any{"one"}),
	} {
		_, err := jev.Prepare(request, jev.Options{Provider: "aimlapi"})
		if fault.Code(err) != fault.Usage {
			t.Fatal("AIMLAPI constraints not enforced", err)
		}
	}
	_, err := jev.Prepare(jev.Question("box", "state", "Q?", nil), jev.Options{Provider: "aimlapi", Model: "typesafe/jev-1.13", Images: true})
	if fault.Code(err) != fault.Usage {
		t.Fatal("older model accepted Box", err)
	}
}
