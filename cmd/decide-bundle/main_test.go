package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleVerification(t *testing.T) {
	dir := t.TempDir()
	file := assetName("linux", "amd64")
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte("binary fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	size, hash, err := digest(path)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: "v1.0.0", Assets: []asset{{OS: "linux", Arch: "amd64", File: file, Size: size, SHA256: hash}}}
	b, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(dir, "decide-manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums(m)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = checkBundle(dir); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupted binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = checkBundle(dir); err == nil {
		t.Error("accepted corrupted binary")
	}
	if assetName("windows", "arm64") != "decide_windows_arm64.exe" {
		t.Error("Windows release must be an executable")
	}
}
