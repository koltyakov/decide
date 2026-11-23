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
		{"choice and confidence", "choice", jev.Object{"a": "A", "b": "B"}, jev.Object{"choice": "a", "confidence": 0.9, "probabilities": jev.Object{"a": 0.9, "b": 0.1}}, Policy{Is: Keys{"a"}, MinConfidence: ptr(0.8)}, false},
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
			result, err := Evaluate(context.Background(), client, request, o, Policies{tt.policy}, Execute)
			if err != nil || result.Rejected() != tt.reject {
				t.Fatalf("%v %v", result, err)
			}
			b, _ := json.Marshal(result.Response)
			if !strings.Contains(string(b), `"answers"`) || !strings.Contains(string(b), `"_decide"`) {
				t.Fatal("native answers or explicit gate missing", string(b))
			}
			if tt.name == "missing confidence" && result.Gates[0].Checks[0].Reason != "confidence_unavailable" {
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
	result, err := Evaluate(context.Background(), client, jev.Question("noul", "private state", "Q?", nil), jev.Options{Endpoint: s.URL}, Policies{{AtLeast: ptr(0.8)}}, Preview)
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
	for _, p := range []Policy{{AtLeast: ptr(1.1)}, {Is: Keys{"a"}}, {MinConfidence: ptr(0.2)}, {Answer: "unknown"}} {
		_, err := Evaluate(context.Background(), client, request, jev.Options{}, Policies{p}, Execute)
		if fault.Code(err) != fault.Usage {
			t.Fatalf("invalid policy accepted: %v", err)
		}
	}
	request["questions"].(map[string]any)["second"] = jev.Object{"type": "noul"}
	if _, err := Evaluate(context.Background(), client, request, jev.Options{}, Policies{{AtLeast: ptr(0.5)}}, Preview); fault.Code(err) != fault.Usage {
		t.Fatal("guessed primary answer", err)
	}
}

func TestUpperBoundsChoiceSetsAndMultipleGates(t *testing.T) {
	client := testutil.Client(t)
	s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) {
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"risk": jev.Object{"noul": 0.2}, "team": jev.Object{"choice": "b", "confidence": 0.9}}})
	})
	request := jev.Question("noul", "state", "Risky?", nil)
	questions := request["questions"].(map[string]any)
	questions["risk"] = questions["answer"]
	delete(questions, "answer")
	questions["team"] = jev.Object{"type": "choice", "criteria": jev.Object{"a": "A", "b": "B", "c": "C"}}
	o := jev.Options{Endpoint: s.URL}
	for _, tt := range []struct {
		name     string
		policies Policies
		reject   bool
	}{
		{"at most passes", Policies{{Answer: "risk", AtMost: ptr(0.3)}}, false},
		{"at most rejects", Policies{{Answer: "risk", AtMost: ptr(0.1)}}, true},
		{"any of keys", Policies{{Answer: "team", Is: Keys{"a", "b"}}}, false},
		{"all gates pass", Policies{{Answer: "risk", AtMost: ptr(0.3)}, {Answer: "team", Is: Keys{"b"}, MinConfidence: ptr(0.8)}}, false},
		{"one gate rejects", Policies{{Answer: "risk", AtMost: ptr(0.3)}, {Answer: "team", Is: Keys{"c"}}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Evaluate(context.Background(), client, request, o, tt.policies, Execute)
			if err != nil || result.Rejected() != tt.reject || len(result.Gates) != len(tt.policies) {
				t.Fatalf("%v %v", result, err)
			}
			meta := result.Response.(jev.Object)["_decide"].(jev.Object)
			if len(tt.policies) == 1 && (meta["gate"] == nil || meta["policy"] == nil) {
				t.Fatal("single policy lost its metadata shape", meta)
			}
			if len(tt.policies) > 1 && (meta["gates"] == nil || meta["passed"] != !tt.reject) {
				t.Fatal("multiple gates not reported", meta)
			}
		})
	}
	for _, p := range []Policies{
		{{Answer: "risk", AtLeast: ptr(0.6), AtMost: ptr(0.4)}},
		{{Answer: "team", AtMost: ptr(0.5)}},
		{{Answer: "team", Is: Keys{}}},
		{{Answer: "team", Is: Keys{"a", "unknown"}}},
		{{Answer: "risk", AtMost: ptr(0.5)}, {Answer: "team"}},
	} {
		if _, err := Evaluate(context.Background(), client, request, o, p, Preview); fault.Code(err) != fault.Usage {
			t.Fatalf("invalid policies accepted: %v", err)
		}
	}
}

func TestParsePoliciesIsStrict(t *testing.T) {
	for _, valid := range []string{`{"atLeast":0.5}`, `[{"answer":"a","is":["x","y"]},{"answer":"b","atMost":0.2}]`, `{"is":"x"}`} {
		if _, err := ParsePolicies([]byte(valid)); err != nil {
			t.Fatalf("%s: %v", valid, err)
		}
	}
	for _, invalid := range []string{`{"atleast":0.5}`, `"x"`, `{"is":5}`, `{} {}`, ``, `null`} {
		if _, err := ParsePolicies([]byte(invalid)); fault.Code(err) != fault.Usage {
			t.Fatalf("%s accepted: %v", invalid, err)
		}
	}
	b, _ := json.Marshal(Policy{Is: Keys{"x"}})
	if string(b) != `{"is":"x"}` {
		t.Fatal("single key changed encoding", string(b))
	}
}
