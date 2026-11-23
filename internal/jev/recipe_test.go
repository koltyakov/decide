package jev_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestOpenRouterReferencesMatchWholeTokensOnly(t *testing.T) {
	image, err := jev.LoadImage("photo", bytes.NewReader(testutil.PNG(t, 300, 300)))
	if err != nil {
		t.Fatal(err)
	}
	request := jev.Question("noul", "Look at photo. Ignore photography and photo-2 terms.", "Is photo clear?", nil)
	prepared, err := jev.Prepare(request, jev.Options{Provider: "openrouter", Images: true, Attachments: []jev.Attachment{image}})
	if err != nil {
		t.Fatal(err)
	}
	state := prepared.Preview()["request"].(jev.Object)["state"].([]any)
	if len(state) != 3 || state[0] != "Look at " || state[2] != ". Ignore photography and photo-2 terms." {
		t.Fatalf("reference matched inside a word: %v", state)
	}
	instructions := prepared.Preview()["request"].(jev.Object)["questions"].(map[string]any)["answer"].(map[string]any)["instructions"].([]any)
	if len(instructions) != 3 || instructions[2] != " clear?" {
		t.Fatalf("instruction reference not replaced: %v", instructions)
	}
}

func TestAssetsMustBeReferencedAsWholeTokens(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(testutil.PNG(t, 280, 280))
	request := jev.Question("noul", "photography only", "Q?", nil)
	request["assets"] = jev.Object{"photo": jev.Object{"format": "png", "data_base64": png}}
	if _, err := jev.Prepare(request, jev.Options{Images: true}); fault.Code(err) != fault.Usage || !strings.Contains(err.Error(), "whole token") {
		t.Fatal("substring counted as a reference", err)
	}
	request["state"] = "the photo, please"
	if _, err := jev.Prepare(request, jev.Options{Images: true}); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeIsReusableConcurrentAndUnchanged(t *testing.T) {
	png := base64.StdEncoding.EncodeToString(testutil.PNG(t, 300, 300))
	request := jev.Question("noul", "", "Does ref-a match?", nil)
	request["questions"].(map[string]any)["answer"].(map[string]any)["criteria"] = jev.Object{"true": []any{"like", "ref-a"}}
	request["assets"] = jev.Object{"ref-a": jev.Object{"format": "png", "data_base64": png}, "ref-b": jev.Object{"format": "png", "data_base64": png}}
	before, _ := json.Marshal(request)
	for _, provider := range []string{"official", "aimlapi", "openrouter"} {
		recipe, err := jev.NewRecipe(request, jev.Options{Provider: provider, Images: true})
		if err != nil {
			t.Fatal(provider, err)
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if _, err := recipe.PrepareJSON([]byte(`{"image":"ref-b","n":1}`)); err != nil {
					t.Error(provider, err)
				}
				if _, err := recipe.Prepare("no second reference"); fault.Code(err) != fault.Usage {
					t.Error(provider, "unreferenced asset accepted", err)
				}
			})
		}
		wg.Wait()
		first, _ := recipe.Prepare(jev.Object{"image": "ref-b"})
		second, _ := recipe.Prepare(jev.Object{"image": "ref-b"})
		a, _ := json.Marshal(first.Preview()["request"])
		b, _ := json.Marshal(second.Preview()["request"])
		if !bytes.Equal(a, b) {
			t.Fatal(provider, "recipe changed between uses")
		}
	}
	after, _ := json.Marshal(request)
	if !bytes.Equal(before, after) {
		t.Fatal("recipe creation mutated caller input")
	}
}

func TestSummaryOmitsOnlyImageBytes(t *testing.T) {
	raw := testutil.PNG(t, 300, 300)
	png := base64.StdEncoding.EncodeToString(raw)
	for _, provider := range []string{"official", "openrouter"} {
		request := jev.Question("noul", jev.Object{"photo": "ref-a", "data_base64": "user text"}, "Q?", nil)
		request["assets"] = jev.Object{"ref-a": jev.Object{"format": "png", "data_base64": png}}
		prepared, err := jev.Prepare(request, jev.Options{Provider: provider, Images: true})
		if err != nil {
			t.Fatal(err)
		}
		full, _ := json.Marshal(prepared.Preview())
		summary, _ := json.Marshal(prepared.Summary())
		if !strings.Contains(string(full), png) || strings.Contains(string(summary), png) {
			t.Fatal(provider, "summary did not omit image bytes")
		}
		if !strings.Contains(string(summary), "user text") || !strings.Contains(string(summary), "[omitted: "+itoa(len(raw))+" image bytes]") {
			t.Fatal(provider, "summary changed state or size", string(summary))
		}
		if prepared.Summary()["bodyBytes"] != prepared.Preview()["bodyBytes"] {
			t.Fatal("summary changed the encoded size")
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestErrorHintsAndHeadersNeverEchoBody(t *testing.T) {
	client := testutil.Client(t)
	for _, tt := range []struct {
		status  int
		body    string
		headers map[string]string
		want    []string
		code    int
	}{
		{400, `{"error":"max_tokens_exceeded","echo":"private-state"}`, nil, []string{"API HTTP 400 (max_tokens_exceeded"}, fault.Failure},
		{413, "private-state", nil, []string{"exceeds the API limit"}, fault.Failure},
		{429, "private-state", map[string]string{"Retry-After": "7", "X-Request-Id": "req_123"}, []string{"(rate limited); not retried; retry-after 7s; request id req_123"}, fault.Remote},
		{529, "private-state", map[string]string{"Retry-After": "soon <script>", "X-Request-Id": "bad id with spaces"}, []string{"API HTTP 529 (API overloaded); not retried"}, fault.Remote},
	} {
		s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) {
			for key, value := range tt.headers {
				w.Header().Set(key, value)
			}
			w.WriteHeader(tt.status)
			_, _ = w.Write([]byte(tt.body))
		})
		_, err := client.Evaluate(context.Background(), jev.Question("noul", "x", "Q?", nil), jev.Options{Endpoint: s.URL})
		if fault.Code(err) != tt.code || strings.Contains(err.Error(), "private-state") || strings.Contains(err.Error(), "script") || strings.Contains(err.Error(), "spaces") {
			t.Fatalf("%d: %v", tt.status, err)
		}
		for _, want := range tt.want {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%d: %q lacks %q", tt.status, err, want)
			}
		}
	}
}

func TestBoxSendsImagesOptIn(t *testing.T) {
	image, err := jev.LoadImage("ref-a", bytes.NewReader(testutil.PNG(t, 300, 300)))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := jev.Prepare(jev.Question("box", "ref-a", "Find the mug", nil), jev.Options{Images: true, Attachments: []jev.Attachment{image}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Preview()["headers"].(http.Header).Get("TypeSafe-Danger-Zone-Images") != "true" {
		t.Fatal("box request without the images opt-in header")
	}
}
