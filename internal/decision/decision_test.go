package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func ptr[T any](value T) *T { return &value }

func TestNativeAnswersAndExplicitGates(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		criteria   any
		answer     jev.Object
		policy     Policy
		reject     bool
	}{
		{"probability pass", "noul", nil, jev.Object{"type": "noul", "noul": 0.8}, Policy{AtLeast: ptr(0.7)}, false},
		{"probability reject", "noul", nil, jev.Object{"noul": 0.8}, Policy{AtLeast: ptr(0.9)}, true},
		{"choice and confidence", "choice", jev.Object{"a": "A", "b": "B"}, jev.Object{"choice": "a", "confidence": 0.9, "probabilities": jev.Object{"a": 0.9, "b": 0.1}}, Policy{Is: ptr("a"), MinConfidence: ptr(0.8)}, false},
		{"missing confidence", "choice", jev.Object{"a": "A", "b": "B"}, jev.Object{"choice": "a"}, Policy{MinConfidence: ptr(0.0)}, true},
		{"null confidence", "choice", jev.Object{"a": "A"}, jev.Object{"choice": "a", "confidence": nil}, Policy{MinConfidence: ptr(0.0)}, true},
		{"fractional score", "score", []any{"low", "mid", "high"}, jev.Object{"score": 1.7, "confidence": 0.8}, Policy{AtLeast: ptr(1.5)}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := testutil.Client(t)
			s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) {
				testutil.Reply(t, w, jev.Object{"model": "model", "answers": jev.Object{"answer": tt.answer}, "usage": jev.Object{"input_tokens": 10}})
			})
			request := jev.Question(tt.kind, "state", "Instructions", tt.criteria)
			o := jev.Options{Endpoint: s.URL}
			result, err := Evaluate(context.Background(), client, request, o, tt.policy, false)
			if err != nil || result.Rejected() != tt.reject {
				t.Fatalf("%v %v", result, err)
			}
			b, _ := json.Marshal(result.Response)
			if !strings.Contains(string(b), `"answers"`) || !strings.Contains(string(b), `"_decide"`) {
				t.Fatal("native answers or explicit gate missing", string(b))
			}
			if tt.name == "missing confidence" && result.Gate.Checks[0].Reason != "confidence_unavailable" {
				t.Fatal("missing confidence did not fail closed")
			}
		})
	}
}

func TestPreflightRequiresNoKeyOrNetwork(t *testing.T) {
	client := testutil.Client(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	var calls atomic.Int32
	s := testutil.API(t, func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) })
	result, err := Evaluate(context.Background(), client, jev.Question("noul", "private state", "Q?", nil), jev.Options{Endpoint: s.URL}, Policy{AtLeast: ptr(0.8)}, true)
	if err != nil || calls.Load() != 0 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
	b, _ := json.Marshal(result.Response)
	if strings.Contains(strings.ToLower(string(b)), "authorization") || !strings.Contains(string(b), "bodyBytes") {
		t.Fatal("preflight exposed credentials or omitted encoded size")
	}
}

func TestInvalidPoliciesFailBeforeNetwork(t *testing.T) {
	client := testutil.Client(t)
	request := jev.Question("noul", "state", "Q?", nil)
	for _, p := range []Policy{{AtLeast: ptr(1.1)}, {Is: ptr("a")}, {MinConfidence: ptr(0.2)}, {Answer: "unknown"}} {
		_, err := Evaluate(context.Background(), client, request, jev.Options{}, p, false)
		if fault.Code(err) != fault.Usage {
			t.Fatalf("invalid policy accepted: %v", err)
		}
	}
	request["questions"].(map[string]any)["second"] = jev.Object{"type": "noul"}
	if _, err := Evaluate(context.Background(), client, request, jev.Options{}, Policy{AtLeast: ptr(0.5)}, true); fault.Code(err) != fault.Usage {
		t.Fatal("guessed primary answer", err)
	}
}
