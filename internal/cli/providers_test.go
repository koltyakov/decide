package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/koltyakov/decide/internal/credentials"
	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func TestNewProviderConnectionLifecycle(t *testing.T) {
	for _, name := range []string{"digitalocean", "cloudflare", "aimlapi", "clef", "openrouter"} {
		t.Run(name, func(t *testing.T) {
			client := testutil.Client(t)
			provider, _ := jev.Lookup(name)
			t.Setenv(provider.KeyEnv, "")
			var calls atomic.Int32
			s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer connection-private-key" {
					t.Error("connection didn't use the entered key")
				}
				if name == "digitalocean" {
					if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
						t.Error("DigitalOcean connection should use model discovery")
					}
					testutil.Reply(t, w, jev.Object{"data": []any{jev.Object{"id": provider.Model}}})
					return
				}
				payload, _ := testutil.Request(t, r)
				if payload["model"] != provider.Model {
					t.Error("wrong provider default model")
				}
				if name == "cloudflare" {
					payload = payload["input"].(map[string]any)
				}
				if payload["state"] != "connection test" {
					t.Error("missing native connection probe")
				}
				result := jev.Object{"answers": jev.Object{"answer": jev.Object{"type": "noul", "noul": 0.9}}}
				if name == "cloudflare" || name == "clef" {
					result = jev.Object{"success": true, "result": result}
				}
				testutil.Reply(t, w, result)
			})
			endpoint := s.URL + "/v1/systemone"
			if name == "aimlapi" {
				endpoint = s.URL + "/v1/decisions"
			}
			if name == "cloudflare" {
				endpoint = s.URL + "/v4/accounts/test/ai/run"
			}
			if name == "openrouter" {
				endpoint = s.URL + "/api/alpha/decisions"
			}
			out, diagnostic, err := execute(t, []byte("connection-private-key"), "providers", "connect", name, "--key-stdin", "--endpoint", endpoint)
			if err != nil || strings.Contains(out+diagnostic, "connection-private-key") {
				t.Fatalf("%s %s %v", out, diagnostic, err)
			}
			saved, err := client.Credentials.Connections()
			if err != nil || saved.Default != name || saved.Providers[name].Endpoint != endpoint {
				t.Fatalf("%+v %v", saved, err)
			}
			before := calls.Load()
			out, _, err = execute(t, nil, "providers", "status", name)
			if err != nil || calls.Load() != before || strings.Contains(out, "connection-private-key") {
				t.Fatalf("status contacted API or exposed a key: %s %v", out, err)
			}
			var status jev.Object
			if err = json.Unmarshal([]byte(out), &status); err != nil {
				t.Fatal(err)
			}
			if status["family"] != provider.Family || status["model"] != provider.Model || status["ready"] != true || status["capabilities"] == nil {
				t.Fatal("incorrect provider status", status)
			}
			if _, _, err = execute(t, nil, "providers", "test", name); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != before+1 {
				t.Error("provider verification did not use the saved connection exactly once")
			}
		})
	}
}

func TestProviderListDistinguishesJevAndClef(t *testing.T) {
	_ = testutil.Client(t)
	out, _, err := execute(t, nil, "providers", "list")
	if err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Providers []jev.Object `json:"providers"`
	}
	if err = json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatal(err)
	}
	seen := map[string]jev.Object{}
	for _, row := range listing.Providers {
		seen[row["provider"].(string)] = row
	}
	if len(seen) != len(jev.Providers()) || seen["cloudflare"]["family"] != "jev" || seen["cloudflare"]["defaultModel"] != "typesafe/jev" || seen["clef"]["family"] != "clef" {
		t.Fatal(out)
	}
	if seen["digitalocean"]["capabilities"].(map[string]any)["images"] != "none" || seen["aimlapi"]["capabilities"].(map[string]any)["images"] != "base64" {
		t.Fatal("incorrect image capabilities", out)
	}
}

func TestLegacyCloudflareClefConnectionFailsWithoutMutation(t *testing.T) {
	client := testutil.Client(t)
	if err := client.Credentials.SaveConnection("cloudflare", "old-key", credentials.Connection{Model: "clef-flash"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(client.Credentials.Path)
	out, _, err := execute(t, nil, "noul", "--instructions", "Q?", "--state", "text")
	after, _ := os.ReadFile(client.Credentials.Path)
	if fault.Code(err) != fault.Usage || out != "" || !strings.Contains(err.Error(), "--provider clef") || string(before) != string(after) {
		t.Fatalf("legacy connection silently reinterpreted: %s %v", out, err)
	}
}
