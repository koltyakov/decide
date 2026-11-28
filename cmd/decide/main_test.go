package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExecutableCLIAndStdio(t *testing.T) {
	_ = testutil.Client(t)
	binary := filepath.Join(t.TempDir(), "decide")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	sources := map[string]stdinSource{"pipe": pipeStdin}
	if socketStdin != nil {
		sources["socket"] = socketStdin
	}
	for name, connect := range sources {
		t.Run("interrupt batch with "+name+" stdin open", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("os.Process.Signal does not support Interrupt on Windows")
			}
			interruptBatch(t, binary, connect)
		})
	}
	for _, args := range [][]string{{"noul"}, {"unknown-command"}, {"systemone", "-", "--output", "invalid"}} {
		cmd := exec.Command(binary, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || stdout.Len() != 0 {
			t.Fatalf("args=%v err=%v stdout=%s stderr=%s", args, err, stdout.String(), stderr.String())
		}
		var failure map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &failure); err != nil || failure["ok"] != false {
			t.Fatal("stderr is not structured JSON", stderr.String())
		}
	}
	api := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		if request["state"] != "@literal-not-a-path" {
			t.Error("MCP interpreted state as file input")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"noul": 0.8}}})
	})
	gateCmd := exec.Command(binary, "noul", "--instructions", "Q?", "--state", "@literal-not-a-path", "--at-least", "0.9", "--output", "value", "--endpoint", api.URL)
	var gateOut, gateErr bytes.Buffer
	gateCmd.Stdout, gateCmd.Stderr = &gateOut, &gateErr
	gateExit, ok := gateCmd.Run().(*exec.ExitError)
	if !ok || gateExit.ExitCode() != 5 || gateOut.String() != "0.8\n" || gateErr.Len() != 0 {
		t.Fatalf("gate process contract: %v stdout=%s stderr=%s", gateExit, gateOut.String(), gateErr.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.Command(binary, "--endpoint", api.URL, "mcp")
	cmd.Stderr = &stderr
	transport := &mcp.CommandTransport{Command: cmd}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "test"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal("MCP stdio initialization", err)
	}
	if session.InitializeResult().ServerInfo.Name != "decide" {
		t.Fatal("wrong MCP server identity", session.InitializeResult().ServerInfo)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 7 {
		t.Fatalf("stdio discovery: %v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Description == "" || tool.OutputSchema == nil || tool.Annotations == nil {
			t.Fatal("stdio discovery lost tool metadata", tool)
		}
	}
	resources, err := session.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != "decide://skills/decide" || resources.Resources[0].Name != "decide" {
		t.Fatalf("stdio skill discovery: %v %v", resources, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "noul", Arguments: jev.Object{"state": "@literal-not-a-path", "instructions": "Q?"}})
	if err != nil || result.IsError {
		t.Fatalf("stdio call: %v %v", result, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "test-secret") {
		t.Error("credential leaked to stderr")
	}
}

// stdinSource connects a child's stdin and returns the parent's writer.
type stdinSource func(t *testing.T, cmd *exec.Cmd) io.WriteCloser

func pipeStdin(t *testing.T, cmd *exec.Cmd) io.WriteCloser {
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// interruptBatch checks that SIGINT stops a batch whose stdin stays open
// without more data, after it has emitted a row.
func interruptBatch(t *testing.T, binary string, connect stdinSource) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "batch", "../../examples/triage.json", "--jobs", "2", "--dry-run")
	input := connect(t, cmd)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		if cmd.ProcessState == nil {
			cancel()
			_ = cmd.Wait()
		}
	}()
	if _, err := io.WriteString(input, "\"first\"\n"); err != nil {
		t.Fatal(err)
	}
	var row struct{ OK bool }
	if err := json.NewDecoder(output).Decode(&row); err != nil || !row.OK {
		t.Fatalf("first row: %+v, err=%v", row, err)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	exit, ok := cmd.Wait().(*exec.ExitError)
	if !ok || exit.ExitCode() != 130 {
		t.Fatalf("batch interrupt with stdin open: %v", exit)
	}
}
