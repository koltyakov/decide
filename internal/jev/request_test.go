package jev_test

import (
	"testing"

	"github.com/koltyakov/decide/internal/jev"
)

func TestStructuredQuestions(t *testing.T) {
	var request jev.Object
	if err := jev.Decode([]byte(`{
      "state":{"data":"record"},
      "questions":{
        "a":{"type":"noul","instructions":{"task":"Check field"},"criteria":{"true":{"examples":["valid"]},"false":null}},
        "b":{"type":"choice","criteria":{"a":null,"b":{"examples":["sample"]}}},
        "c":{"type":"score","criteria":[{"definition":"low"},["high"]]}
      }
    }`), &request); err != nil {
		t.Fatal(err)
	}
	if err := jev.Validate(request); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidRequests(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"state":true,"questions":{}}`, `{"state":"x","questions":[]}`, `{"state":"x","questions":{"a":{"type":"unknown"}}}`, `{"state":"x","questions":{"a":{"type":"choice","criteria":{}}}}`, `{"state":"x","questions":{"a":{"type":"score","criteria":[null]}}}`, `{"state":"x","questions":{"a":{"type":"box","criteria":[]}}}`, `{"state":{"n":1e400},"questions":{"a":{"type":"noul"}}}`, `{"state":"x","questions":{"a":{"type":"noul","criteria":{"maybe":"x"}}}}`} {
		var request jev.Object
		if err := jev.Decode([]byte(raw), &request); err != nil {
			t.Fatal(err)
		}
		if err := jev.Validate(request); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestDecodeRejectsTrailingOrInvalidUTF8(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{} {}`), []byte(`{} garbage`), {0xff}} {
		if err := jev.Decode(raw, new(any)); err == nil {
			t.Error("invalid JSON accepted")
		}
	}
}
