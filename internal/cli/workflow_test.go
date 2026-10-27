package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func writeRecipe(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recipe.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStructuredInputsAndDryRun(t *testing.T) {
	_ = testutil.Client(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	criteria := writeRecipe(t, `{"a":{"definition":"A"},"b":["B"]}`)
	out, _, err := execute(t, nil, "choice", "--instructions", `{"task":"Q?"}`, "--instructions-format", "json", "--criteria-file", criteria, "--state", `{"record":1}`, "--state-format", "json", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	var result jev.Object
	if err = jev.Decode([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	request := result["request"].(map[string]any)
	q := request["questions"].(map[string]any)["answer"].(map[string]any)
	if q["instructions"].(map[string]any)["task"] != "Q?" || request["state"].(map[string]any)["record"] != json.Number("1") {
		t.Fatal("did not preserve native structured fields")
	}
}

func TestRecipeStateReplacementAndValueSelection(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"state":"old","questions":{"urgent":{"type":"noul"},"team":{"type":"choice","criteria":{"a":"A"}}}}`)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		if request["state"] != "new" {
			t.Error("state override not applied")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"urgent": jev.Object{"noul": 0.8}, "team": jev.Object{"choice": "a"}}})
	})
	out, _, err := execute(t, nil, "systemone", recipe, "--state", "new", "--answer", "urgent", "--at-least", "0.9", "--output", "value", "--endpoint", s.URL)
	if fault.Code(err) != fault.Rejected || out != "0.8\n" {
		t.Fatalf("%s %v", out, err)
	}
	out, _, err = execute(t, nil, "systemone", recipe, "--output", "value")
	if fault.Code(err) != fault.Usage || out != "" {
		t.Fatal("multi-answer selection guessed", out, err)
	}
}

func TestBatchBoundedConcurrencyOrderedResultsAndErrors(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`)
	var calls, active, peak atomic.Int32
	release := make(chan struct{})
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		id := request["state"].(map[string]any)["id"].(json.Number)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		count := calls.Add(1)
		if count == 3 {
			close(release)
		}
		if count <= 3 {
			<-release
		}
		if id == "4" {
			w.WriteHeader(429)
			return
		}
		value := 0.8
		if id == "5" {
			value = 0.1
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"urgent": jev.Object{"noul": value}}})
	})
	input := []byte("{\"id\":0}\n{\"id\":1}\n{\"id\":2}\nnot-json\n{\"id\":4}\n{\"id\":5}\n")
	out, _, err := execute(t, input, "batch", recipe, "--jobs", "3", "--at-least", "0.5", "--endpoint", s.URL)
	if fault.Code(err) != fault.Batch || calls.Load() != 5 || peak.Load() != 3 {
		t.Fatalf("%v calls=%d peak=%d", err, calls.Load(), peak.Load())
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 6 {
		t.Fatal(out)
	}
	for i, line := range lines {
		var row batchResult
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row.Index != i {
			t.Fatal("results reordered", out)
		}
		if (i == 3 || i == 4) == row.OK {
			t.Fatal("wrong record success status", line)
		}
		if i == 5 && row.Code != fault.Rejected {
			t.Fatal("gate failure missing", line)
		}
	}
}

func TestBatchCancellationStopsLaterWindows(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"questions":{"a":{"type":"noul"}}}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	s := testutil.API(t, func(_ http.ResponseWriter, r *http.Request) {
		testutil.Request(t, r)
		calls.Add(1)
		cancel()
		<-r.Context().Done()
	})
	var out bytes.Buffer
	cmd := New("test", strings.NewReader("\"first\"\n\"second\"\n"), &out, &out)
	cmd.SetArgs([]string{"batch", recipe, "--endpoint", s.URL})
	if err := cmd.ExecuteContext(ctx); err == nil || calls.Load() != 1 {
		t.Fatalf("%v calls=%d", err, calls.Load())
	}
}

type signaledReader struct {
	io.ReadCloser
	started chan struct{}
}

func (r *signaledReader) Read(b []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.ReadCloser.Read(b)
}

func TestBatchCancellationUnblocksInput(t *testing.T) {
	_ = testutil.Client(t)
	recipe := writeRecipe(t, `{"questions":{"a":{"type":"noul"}}}`)
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	cmd := New("test", &signaledReader{ReadCloser: reader, started: started}, &out, &out)
	cmd.SetArgs([]string{"batch", recipe, "--dry-run"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not start reading")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation was lost")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("batch left input read blocked after cancellation")
	}
}

func TestFailedProviderConnectionDoesNotChangeStore(t *testing.T) {
	client := testutil.Client(t)
	if err := client.Credentials.SaveConnection("official", "old", credentials.Connection{Model: "saved"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(client.Credentials.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := testutil.API(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("new-secret private-state"))
	})
	out, diagnostic, err := execute(t, []byte("new-secret"), "providers", "connect", "official", "--key-stdin", "--endpoint", s.URL+"/v1/systemone")
	after, _ := os.ReadFile(client.Credentials.Path)
	if fault.Code(err) != fault.Auth || !bytes.Equal(before, after) || strings.Contains(out+diagnostic+err.Error(), "new-secret") {
		t.Fatalf("unsafe failed connection: %v", err)
	}
}

func TestProviderSavedDefaultUsedByDecisions(t *testing.T) {
	client := testutil.Client(t)
	t.Setenv("DECIDE_API_KEY", "")
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved-key" {
			t.Error("saved credentials not used")
		}
		request, _ := testutil.Request(t, r)
		if request["model"] != "saved-model" {
			t.Error("saved model not used")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"noul": 0.8}}})
	})
	if err := client.Credentials.SaveConnection("custom", "saved-key", credentials.Connection{Endpoint: s.URL, Model: "saved-model"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, nil, "noul", "--instructions", "Q?", "--state", "x", "--output", "value")
	if err != nil || out != "0.8\n" {
		t.Fatalf("%s %v", out, err)
	}
}
