package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/jev"
	"github.com/koltyakov/decide/internal/testutil"
)

func execute(t *testing.T, input []byte, args ...string) (string, string, error) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	cmd := New("test", bytes.NewReader(input), &out, &diagnostic)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), diagnostic.String(), err
}

func TestCLIIdentityAndCredentialPath(t *testing.T) {
	for _, tt := range []struct {
		arg, want string
	}{
		{"--help", "decide [command]"},
		{"--version", "decide version test"},
	} {
		out, diagnostic, err := execute(t, nil, tt.arg)
		if err != nil || diagnostic != "" || !strings.Contains(out, tt.want) {
			t.Fatalf("%s: stdout=%q stderr=%q err=%v", tt.arg, out, diagnostic, err)
		}
	}
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("DECIDE_CREDENTIALS_FILE", "")
	for _, tt := range []struct {
		flag, env, want string
	}{
		{"", "", filepath.Join(base, "decide", "credentials.json")},
		{"", filepath.Join(base, "env.json"), filepath.Join(base, "env.json")},
		{filepath.Join(base, "flag.json"), filepath.Join(base, "env.json"), filepath.Join(base, "flag.json")},
	} {
		t.Setenv("DECIDE_CREDENTIALS_FILE", tt.env)
		a := app{credentialPath: tt.flag}
		path, err := a.path()
		if err != nil || path != tt.want {
			t.Fatalf("credential path=%q want=%q err=%v", path, tt.want, err)
		}
	}
}

func TestCommandsAndPipelineInputs(t *testing.T) {
	_ = testutil.Client(t)
	statePath := filepath.Join(t.TempDir(), "state.txt")
	if err := os.WriteFile(statePath, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, state, output string
		input               []byte
		args                []string
	}{
		{"noul", "literal", "0.8\n", nil, []string{"noul", "--instructions", "Q?", "--state", "literal", "--output", "value"}},
		{"stdin", "piped", "0.8\n", []byte("piped"), []string{"noul", "--instructions", "Q?", "--output", "value"}},
		{"file", "from file", "0.8\n", nil, []string{"noul", "--instructions", "Q?", "--state-file", statePath, "--output", "value"}},
		{"literal path", "@not-a-file", "0.8\n", nil, []string{"noul", "--instructions", "Q?", "--state", "@not-a-file", "--output", "value"}},
		{"choice", "literal", "a\n", nil, []string{"choice", "--instructions", "Q?", "--state", "literal", "--criteria", `{"a":"Alpha, with comma","b":"Beta"}`, "--output", "value"}},
		{"score", "literal", "0.8\n", nil, []string{"score", "--instructions", "Q?", "--state", "literal", "--criteria", `["Low","High"]`, "--output", "value"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
				request, _ := testutil.Request(t, r)
				if request["state"] != tt.state {
					t.Error("wrong state", request["state"])
				}
				kind := request["questions"].(map[string]any)["answer"].(map[string]any)["type"].(string)
				var answer any = 0.8
				if kind == "choice" {
					answer = "a"
				}
				testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{kind: answer}}})
			})
			args := append(tt.args, "--endpoint", s.URL, "--provider", "official")
			out, diagnostic, err := execute(t, tt.input, args...)
			if err != nil || out != tt.output || diagnostic != "" {
				t.Fatalf("out=%q stderr=%q err=%v", out, diagnostic, err)
			}
		})
	}
}

func TestSystemOneJSONAndModelPrecedence(t *testing.T) {
	_ = testutil.Client(t)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, _ := testutil.Request(t, r)
		if request["model"] != "override" || request["extra"] != true || request["state"].(map[string]any)["id"] != json.Number("9007199254740993") {
			t.Error("request lost fields or model override")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"x": jev.Object{"noul": 0.8}}})
	})
	input := []byte(`{"model":"original","state":{"id":9007199254740993},"questions":{"x":{"type":"noul","instructions":{"question":"Q?"}}},"extra":true}`)
	out, _, err := execute(t, input, "systemone", "-", "--endpoint", s.URL, "--model", "override")
	if err != nil || !strings.Contains(out, `"x"`) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestImageStdinPipeline(t *testing.T) {
	_ = testutil.Client(t)
	png := testutil.PNG(t, 280, 280)
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		request, images := testutil.Request(t, r)
		if request["state"].(map[string]any)["photo"] == nil || len(images) != 1 || !bytes.Equal(images[0], png) {
			t.Error("image stdin pipeline failed")
		}
		testutil.Reply(t, w, jev.Object{"answers": jev.Object{"answer": jev.Object{"box": jev.Object{"left": 0, "top": 0, "right": 1, "bottom": 1}}}})
	})
	out, _, err := execute(t, png, "box", "--instructions", "Find mug in photo", "--image", "photo=-", "--experimental-images", "--endpoint", s.URL, "--output", "value")
	if err != nil || !strings.Contains(out, `"bottom":1`) {
		t.Fatalf("%s %v", out, err)
	}
}

func TestInvalidCLIInput(t *testing.T) {
	for _, args := range [][]string{
		{"noul"}, {"choice", "--instructions", "Q?"}, {"score", "--instructions", "Q?"},
		{"noul", "--instructions", "Q?", "--state", "x", "--state-file", "-"},
		{"noul", "--instructions", "Q?", "--at-least", "NaN"},
		{"systemone", "-", "--value"}, {"noul", "--instructions", "Q?", "--timeout", "0"},
		{"noul", "--instructions", "Q?", "--image", "photo=-"},
		{"noul", "--instructions", "Q?", "--state-file", "-", "--image", "photo=-", "--experimental-images"},
	} {
		out, _, err := execute(t, nil, args...)
		if err == nil || fault.Code(err) != fault.Usage || out != "" {
			t.Errorf("args=%v out=%q err=%v", args, out, err)
		}
	}
}

func TestProviderConnectionNeverPrintsKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("DECIDE_PROVIDER", "")
	path := filepath.Join(t.TempDir(), "credentials.json")
	s := testutil.API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer super-private-key" {
			t.Error("connection must verify using model discovery and the entered key")
		}
		testutil.Reply(t, w, jev.Object{"models": []any{}})
	})
	for _, args := range [][]string{{"connect", "official", "--key-stdin", "--endpoint", s.URL + "/v1/systemone"}, {"status"}, {"list"}, {"use", "official"}, {"disconnect", "official"}} {
		args = append([]string{"providers"}, args...)
		args = append(args, "--credentials", path)
		out, diagnostic, err := execute(t, []byte("super-private-key"), args...)
		if err != nil || strings.Contains(out+diagnostic, "super-private-key") {
			t.Fatalf("%v: %s %s %v", args, out, diagnostic, err)
		}
	}
}
