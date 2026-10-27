package skills

import (
	"strings"
	"testing"
)

func TestSkillDiscoveryMetadata(t *testing.T) {
	lf := strings.ReplaceAll(string(Decide), "\r\n", "\n")
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "LF", content: lf},
		{name: "CRLF", content: strings.ReplaceAll(lf, "\n", "\r\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkSkillDiscoveryMetadata(t, tc.content)
		})
	}
}

func checkSkillDiscoveryMetadata(t *testing.T, content string) {
	t.Helper()
	content = strings.ReplaceAll(content, "\r\n", "\n")
	parts := strings.SplitN(content, "---", 3)
	if len(parts) != 3 || parts[0] != "" || !strings.Contains(parts[1], "\nname: decide\n") {
		t.Fatal("skill must start with portable name/description frontmatter")
	}
	var description string
	for line := range strings.SplitSeq(parts[1], "\n") {
		if strings.HasPrefix(line, "description: ") {
			description = strings.TrimPrefix(line, "description: ")
		}
	}
	if len(description) == 0 || len(description) > 1024 {
		t.Fatal("invalid discovery description length")
	}
	for _, trigger := range []string{"decide", "Jev", "TypeSafe AI", "classification", "routing", "verification", "noul", "choice", "score", "box"} {
		if !strings.Contains(description, trigger) {
			t.Errorf("missing trigger %q", trigger)
		}
	}
	if strings.Contains(parts[1], "disable-model-invocation") || !strings.Contains(content, "<!-- Managed by decide -->") {
		t.Fatal("automatic discovery disabled or ownership marker missing")
	}
}
