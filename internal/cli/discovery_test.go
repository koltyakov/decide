package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestCapabilitiesAndCompressedPreflightWithoutKey(t *testing.T) {
	_ = testutil.Client(t)
	t.Setenv("TYPESAFE_API_KEY", "")
	out, diagnostic, err := execute(t, nil, "capabilities", "--model", "jev-1.13.0", "--experimental-images")
	if err != nil || diagnostic != "" {
		t.Fatalf("capabilities: %s %s %v", out, diagnostic, err)
	}
	var capabilities jev.Object
	if err := json.Unmarshal([]byte(out), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities["model"] != "jev-1.13.0" || capabilities["available"].(map[string]any)["box"] != false || capabilities["verified"] != false {
		t.Fatal(out)
	}
	for _, mode := range []string{"none", "gzip", "x-gzip", "zstd"} {
		out, diagnostic, err := execute(t, nil, "noul", "--instructions", "Does the evidence support the claim?", "--state", strings.Repeat("evidence ", 1000), "--dry-run", "--compression", mode)
		if err != nil || diagnostic != "" {
			t.Fatalf("preflight %s: %s %v", mode, diagnostic, err)
		}
		var preview jev.Object
		if err := json.Unmarshal([]byte(out), &preview); err != nil {
			t.Fatal(err)
		}
		if preview["compression"] != mode || preview["uncompressedBodyBytes"].(float64) < preview["bodyBytes"].(float64) {
			t.Fatal("unexpected preflight sizes", out)
		}
	}
}

func TestInstalledMCPPreservesProcessPermissions(t *testing.T) {
	_ = testutil.Client(t)
	project := t.TempDir()
	out, diagnostic, err := execute(t, nil, "install-mcp", "codex", "--project", project, "--provider", "official", "--model", "jev-1.14.0", "--compression", "zstd", "--experimental-images", "--long-context")
	if err != nil || diagnostic != "" {
		t.Fatalf("installation: %s %s %v", out, diagnostic, err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"--compression", "zstd", "--experimental-images", "--long-context", "--provider", "jev-1.14.0"} {
		if !strings.Contains(string(data), value) {
			t.Errorf("installed MCP lost %s", value)
		}
	}
	if strings.Contains(string(data), "test-secret") {
		t.Fatal("credential leaked into integration config")
	}
}
