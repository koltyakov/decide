package credentials

import (
	"path/filepath"
	"testing"
)

func TestConnectionLifecycle(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := s.SaveConnection("custom", "key", Connection{Endpoint: "https://example.com/v1/systemone", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveConnection("official", "other", Connection{}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Connections()
	if err != nil || c.Default != "custom" || len(c.Providers) != 2 {
		t.Fatalf("%v %v", c, err)
	}
	if err = s.Use("official"); err != nil {
		t.Fatal(err)
	}
	if err = s.Disconnect("official"); err != nil {
		t.Fatal(err)
	}
	c, err = s.Connections()
	if err != nil || c.Default != "" || len(c.Providers) != 1 {
		t.Fatalf("%v %v", c, err)
	}
	if _, _, err = s.Get("official", "UNSET_DECIDE_TEST_KEY"); err == nil {
		t.Fatal("disconnected key retained")
	}
	if key, _, err := s.Get("custom", "UNSET_DECIDE_TEST_KEY"); err != nil || key != "key" {
		t.Fatal("another connection was damaged")
	}
}
