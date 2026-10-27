// decide-bundle builds CGO-free release binaries and verifies their manifest.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Version string  `json:"version"`
	Assets  []asset `json:"assets"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("out", "dist", "release output directory")
	check := flag.Bool("check", false, "verify an existing release bundle")
	binary := flag.String("binary", "", "build a native binary only")
	version := flag.String("version", "dev", "embedded CLI version")
	platforms := flag.String("platforms", "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64", "comma-separated release platforms")
	flag.Parse()
	if *check {
		return checkBundle(*dir)
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9_.+-]+$`).MatchString(*version) {
		return fmt.Errorf("invalid version")
	}
	if *binary != "" {
		osName, arch := runtime.GOOS, runtime.GOARCH
		if value := os.Getenv("GOOS"); value != "" {
			osName = value
		}
		if value := os.Getenv("GOARCH"); value != "" {
			arch = value
		}
		return buildBinary(*binary, osName, arch, *version)
	}
	m := manifest{Version: *version}
	seen := map[string]bool{}
	for _, platform := range strings.Split(*platforms, ",") {
		osName, arch, ok := strings.Cut(platform, "/")
		if !ok || !validPlatform(osName, arch) || seen[platform] {
			return fmt.Errorf("invalid or duplicate platform %q", platform)
		}
		seen[platform] = true
		m.Assets = append(m.Assets, asset{OS: osName, Arch: arch, File: assetName(osName, arch)})
	}
	for i := range m.Assets {
		a := &m.Assets[i]
		path := filepath.Join(*dir, a.File)
		fmt.Fprintln(os.Stderr, "building", a.OS+"/"+a.Arch)
		if err := buildBinary(path, a.OS, a.Arch, *version); err != nil {
			return err
		}
		var err error
		a.Size, a.SHA256, err = digest(path)
		if err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(*dir, "decide-manifest.json"), append(b, '\n'), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*dir, "checksums.txt"), []byte(checksums(m)), 0644)
}

func validPlatform(osName, arch string) bool {
	return (osName == "linux" || osName == "darwin" || osName == "windows") && (arch == "amd64" || arch == "arm64")
}

func assetName(osName, arch string) string {
	name := "decide_" + osName + "_" + arch
	if osName == "windows" {
		name += ".exe"
	}
	return name
}

func buildBinary(path, osName, arch, version string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version, "-o", path, "./cmd/decide")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+osName, "GOARCH="+arch)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func digest(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func checksums(m manifest) string {
	var b strings.Builder
	for _, a := range m.Assets {
		fmt.Fprintf(&b, "%s  %s\n", a.SHA256, a.File)
	}
	return b.String()
}

func checkBundle(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "decide-manifest.json"))
	if err != nil {
		return err
	}
	var m manifest
	if err = json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Version == "" || len(m.Assets) == 0 {
		return fmt.Errorf("empty release manifest")
	}
	seen := map[string]bool{}
	for _, a := range m.Assets {
		if !validPlatform(a.OS, a.Arch) || a.File != assetName(a.OS, a.Arch) || seen[a.File] || a.Size <= 0 {
			return fmt.Errorf("invalid release asset")
		}
		seen[a.File] = true
		size, hash, err := digest(filepath.Join(dir, a.File))
		if err != nil {
			return err
		}
		if size != a.Size || hash != a.SHA256 {
			return fmt.Errorf("checksum or size mismatch: %s", a.File)
		}
	}
	b, err = os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return err
	}
	if string(b) != checksums(m) {
		return fmt.Errorf("checksums.txt does not match release manifest")
	}
	return nil
}
