package jev_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestLatestModelDefaultsAndOlderPins(t *testing.T) {
	for _, tt := range []struct{ provider, model string }{
		{"official", "jev-latest"}, {"openrouter", "~typesafe/jev-latest"}, {"vercel", "typesafe-ai/jev-1.14.0"}, {"aimlapi", "typesafe/jev"},
	} {
		prepared, err := jev.Prepare(jev.Question("noul", "state", "Q?", nil), jev.Options{Provider: tt.provider})
		if err != nil || prepared.Preview()["model"] != tt.model {
			t.Fatalf("%s default not current: %v %v", tt.provider, prepared, err)
		}
	}
	for _, tt := range []struct{ provider, model string }{
		{"official", "jev-1.13.0"}, {"openrouter", "typesafe/jev-1.13"}, {"vercel", "typesafe-ai/jev-1.13.0"}, {"aimlapi", "typesafe/jev-1.13"},
	} {
		request := jev.Question("noul", "state", "Q?", nil)
		request["model"] = tt.model
		prepared, err := jev.Prepare(request, jev.Options{Provider: tt.provider})
		if err != nil || prepared.Preview()["model"] != tt.model {
			t.Fatalf("explicit pin overridden: %v %v", prepared, err)
		}
		request = jev.Question("box", "state", "Q?", nil)
		request["model"] = tt.model
		if _, err = jev.Prepare(request, jev.Options{Provider: tt.provider, Images: true}); fault.Code(err) != fault.Usage {
			t.Fatal("older version accepted Box", err)
		}
	}
}

func TestOpenRouterImageReferencesBecomeNativeParts(t *testing.T) {
	client := testutil.Client(t)
	png := testutil.PNG(t, 280, 280)
	image, err := jev.LoadImage("unique-photo-ref", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	request := jev.Question("box", jev.Object{"photo": "unique-photo-ref"}, "Find the mug matching unique-photo-ref in photo", nil)
	request["questions"].(map[string]any)["compare"] = jev.Object{"type": "choice", "instructions": "Compare examples", "criteria": jev.Object{"type": "image_url", "same": jev.Object{"reference": "unique-photo-ref"}, "other": "Other"}}
	before, _ := json.Marshal(request)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		payload, images := testutil.Request(t, r)
		if payload["assets"] != nil || len(images) != 0 || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("TypeSafe-Danger-Zone-Images") != "" {
			t.Error("OpenRouter must receive image parts, not ignored assets or multipart")
		}
		if payload["model"] != "~typesafe/jev-latest" {
			t.Error("not using latest Jev route")
		}
		part := payload["state"].(map[string]any)["photo"].(map[string]any)
		url := part["image_url"].(map[string]any)["url"].(string)
		if url != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
			t.Error("image bytes changed")
		}
		q := payload["questions"].(map[string]any)
		instructions := q["answer"].(map[string]any)["instructions"].([]any)
		if len(instructions) != 3 || instructions[0] != "Find the mug matching " || instructions[2] != " in photo" {
			t.Error("embedded image text lost", instructions)
		}
		if q["compare"].(map[string]any)["criteria"].(map[string]any)["same"].(map[string]any)["reference"].(map[string]any)["type"] != "image_url" {
			t.Error("structured criterion image was not converted")
		}
		if q["compare"].(map[string]any)["criteria"].(map[string]any)["type"] != "image_url" {
			t.Error("Choice option keys were treated as an image part")
		}
		testutil.Reply(t, w, jev.Object{"model": "typesafe/jev-1.14", "answers": jev.Object{"answer": jev.Object{"type": "box", "box": jev.Object{"left": 0, "right": 1, "top": 0, "bottom": 1}, "confidence": 0.8}, "compare": jev.Object{"type": "choice", "choice": "same"}}, "usage": jev.Object{"input_tokens": 10, "cost": 0.001}})
	})
	result, err := client.Evaluate(context.Background(), request, jev.Options{Provider: "openrouter", Endpoint: s.URL, Images: true, Attachments: []jev.Attachment{image}})
	if err != nil || result["usage"].(map[string]any)["cost"] != json.Number("0.001") {
		t.Fatalf("%v %v", result, err)
	}
	after, _ := json.Marshal(request)
	if !bytes.Equal(before, after) {
		t.Error("conversion mutated caller input")
	}
}

func TestOpenRouterNativePartsCannotBypassValidation(t *testing.T) {
	png := testutil.PNG(t, 280, 280)
	good := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	for _, tt := range []struct {
		url   string
		optIn bool
		valid bool
	}{
		{good, true, true}, {good, false, false}, {"https://example.com/private.png", true, false}, {"data:image/webp;base64,AAAA", true, false}, {"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(png), true, false},
		{"data:image/png;base64," + base64.StdEncoding.EncodeToString(testutil.PNG(t, 256, 256)), true, false},
	} {
		request := jev.Question("box", []any{"photo", jev.Object{"type": "image_url", "image_url": jev.Object{"url": tt.url}}}, "Find the mug", nil)
		_, err := jev.Prepare(request, jev.Options{Provider: "openrouter", Images: tt.optIn})
		if (err == nil) != tt.valid {
			t.Fatalf("native image validation: valid=%v err=%v", tt.valid, err)
		}
	}
}

func TestOpenRouterExpansionIsBounded(t *testing.T) {
	png := testutil.PNG(t, 280, 280)
	request := jev.Question("noul", strings.Repeat("photo-ref ", 200000), "Q?", nil)
	request["assets"] = jev.Object{"photo-ref": jev.Object{"format": "png", "data_base64": base64.StdEncoding.EncodeToString(png)}}
	_, err := jev.Prepare(request, jev.Options{Provider: "openrouter", Images: true})
	if fault.Code(err) != fault.Usage || !strings.Contains(err.Error(), "expansion") {
		t.Fatal("image expansion wasn't bounded", err)
	}
}

func TestOpenRouterDecisionsCatalog(t *testing.T) {
	client := testutil.Client(t)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" || r.URL.Query().Get("output_modalities") != "decisions" || r.Method != http.MethodGet {
			t.Error("wrong Decisions catalog request", r.URL)
		}
		testutil.Reply(t, w, jev.Object{"data": []any{jev.Object{"id": "typesafe/jev-1.14"}}})
	})
	result, err := client.Models(context.Background(), jev.Options{Provider: "openrouter", Endpoint: s.URL + "/api/alpha/decisions"})
	if err != nil || result["data"] == nil {
		t.Fatalf("%v %v", result, err)
	}
}
