package cli

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func rows(t *testing.T, out string) []batchResult {
	t.Helper()
	var result []batchResult
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var row batchResult
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err, line)
		}
		result = append(result, row)
	}
	return result
}

func TestBatchSlowRecordDoesNotIdleOtherSlots(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"questions":{"a":{"type":"noul"}}}`)
	third := make(chan struct{})
	var once sync.Once
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		switch request["state"] {
		case "0":
			// A fixed window would never start record 2 while record 0 runs.
			select {
			case <-third:
			case <-time.After(5 * time.Second):
				w.WriteHeader(500)
				return
			}
		case "2":
			once.Do(func() { close(third) })
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"a": jev.Object{"noul": 0.5}}})
	})
	out, _, err := execute(t, []byte("\"0\"\n\"1\"\n\"2\"\n"), "batch", recipe, "--jobs", "2", "--endpoint", s.URL)
	result := rows(t, out)
	if err != nil || len(result) != 3 {
		t.Fatalf("%v %s", err, out)
	}
	for i, row := range result {
		if row.Index != i || !row.OK {
			t.Fatal("slow record blocked the window or reordered output", out)
		}
	}
}

func TestBatchResumeAndOnlyFailed(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"questions":{"a":{"type":"noul"}}}`)
	var mu sync.Mutex
	var seen []string
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		mu.Lock()
		seen = append(seen, request["state"].(string))
		mu.Unlock()
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"a": jev.Object{"noul": 0.5}}})
	})
	input := []byte("\"0\"\n\n\"1\"\n\"2\"\n\"3\"\n")
	out, _, err := execute(t, input, "batch", recipe, "--from-index", "2", "--endpoint", s.URL)
	if result := rows(t, out); err != nil || len(result) != 2 || result[0].Index != 2 || result[1].Index != 3 {
		t.Fatalf("resume did not keep indices: %v %s", err, out)
	}
	prior := filepath.Join(t.TempDir(), "prior.jsonl")
	if err = os.WriteFile(prior, []byte(`{"index":0,"ok":true,"code":5,"response":{}}
{"index":1,"ok":false,"error":"API HTTP 429; not retried","code":4}
{"index":3,"ok":false,"error":"x","code":4}
`), 0600); err != nil {
		t.Fatal(err)
	}
	seen = nil
	out, _, err = execute(t, input, "batch", recipe, "--only-failed", prior, "--endpoint", s.URL)
	result := rows(t, out)
	if err != nil || len(result) != 2 || result[0].Index != 1 || result[1].Index != 3 || strings.Join(seen, ",") != "1,3" {
		t.Fatalf("only-failed reran the wrong records: %v %s %v", err, out, seen)
	}
	out, _, err = execute(t, []byte("\"0\"\n\n\"1\"\n"), "batch", recipe, "--only-failed", prior, "--dry-run")
	result = rows(t, out)
	if err == nil || fault.Code(err) != fault.Usage || !strings.Contains(err.Error(), "indices [3]") || len(result) != 1 || result[0].Index != 1 || !result[0].OK {
		t.Fatalf("truncated resume lost the completed row or missing index: %v %s", err, out)
	}
	out, _, err = execute(t, nil, "batch", recipe, "--only-failed", prior, "--from-index", "2", "--dry-run")
	if err == nil || fault.Code(err) != fault.Usage || !strings.Contains(err.Error(), "indices [3]") || out != "" {
		t.Fatalf("missing indices should exclude those below --from-index: %v %s", err, out)
	}
	out, _, err = execute(t, nil, "batch", recipe, "--only-failed", prior, "--from-index", "4", "--dry-run")
	if err != nil || out != "" {
		t.Fatalf("empty selection should succeed: %v %s", err, out)
	}
	if err = os.WriteFile(prior, []byte(`{"ok":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = execute(t, input, "batch", recipe, "--only-failed", prior, "--endpoint", s.URL); fault.Code(err) != fault.Usage {
		t.Fatal("malformed prior output accepted", err)
	}
}

func TestBatchRecipeErrorsFailBeforeRecords(t *testing.T) {
	_ = testutil.Client(t)
	png := base64.StdEncoding.EncodeToString(testutil.PNG(t, 280, 280))
	recipe := writeRecipe(t, `{"questions":{"a":{"type":"noul"}},"assets":{"ref-a":{"format":"png","data_base64":"`+png+`"}}}`)
	out, _, err := execute(t, []byte("\"ref-a\"\n"), "batch", recipe, "--dry-run")
	if fault.Code(err) != fault.Usage || out != "" {
		t.Fatal("recipe without image opt-in became record errors", err, out)
	}
	out, _, err = execute(t, []byte("\"ref-a\"\n\"no reference\"\n"), "batch", recipe, "--dry-run", "--experimental-images", "--preview-images", "summary")
	result := rows(t, out)
	if fault.Code(err) != fault.Batch || len(result) != 2 || !result[0].OK || result[1].OK || strings.Contains(out, png) {
		t.Fatalf("per-record reference checks or summary preview failed: %v %s", err, out)
	}
}

func TestPolicyFlagsAndUpperBounds(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"state":"s","questions":{"risk":{"type":"noul"},"team":{"type":"choice","criteria":{"a":"A","b":"B"}}}}`)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		testutil.Request(t, r)
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"risk": jev.Object{"noul": 0.2}, "team": jev.Object{"choice": "b", "confidence": 0.9}}})
	})
	out, _, err := execute(t, nil, "systemone", recipe, "--answer", "risk", "--at-most", "0.3", "--output", "value", "--endpoint", s.URL)
	if err != nil || out != "0.2\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, _, err = execute(t, nil, "systemone", recipe, "--answer", "team", "--is", "a", "--is", "b", "--endpoint", s.URL); err != nil {
		t.Fatal("repeated --is did not accept any listed key", err)
	}
	out, _, err = execute(t, nil, "systemone", recipe, "--policy", `[{"answer":"risk","atMost":0.1},{"answer":"team","is":"b"}]`, "--endpoint", s.URL)
	if fault.Code(err) != fault.Rejected || !strings.Contains(out, `"gates"`) {
		t.Fatalf("multi-policy gate: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"--policy", `{"answer":"risk","atLeast":0.1}`, "--at-most", "0.5"},
		{"--policy", `{"answer":"risk","atleast":0.1}`},
		{"--answer", "risk", "--at-least", "0.5", "--at-most", "0.4"},
		{"--preview-images", "summary"},
	} {
		if _, _, err = execute(t, nil, append([]string{"systemone", recipe, "--endpoint", s.URL}, args...)...); fault.Code(err) != fault.Usage {
			t.Fatal("invalid policy flags accepted", args, err)
		}
	}
}
