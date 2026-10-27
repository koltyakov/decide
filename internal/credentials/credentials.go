// Package credentials stores provider keys without exposing a read command.
package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/store"
)

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "decide", "credentials.json"), nil
}

type Store struct{ Path string }

// Raw members preserve connection settings and metadata written by newer clients.
func (s Store) load() (map[string]json.RawMessage, map[string]string, error) {
	root := map[string]json.RawMessage{}
	keys := map[string]string{}
	b, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return root, keys, nil
	}
	if err != nil {
		return nil, nil, fault.New(fault.Auth, "cannot read credential store")
	}
	if json.Unmarshal(b, &root) != nil || root == nil {
		return nil, nil, fault.New(fault.Auth, "invalid credential store; refusing to overwrite it")
	}
	if v, ok := root["providers"]; ok {
		if json.Unmarshal(v, &keys) != nil || keys == nil {
			return nil, nil, fault.New(fault.Auth, "invalid providers in credential store")
		}
	}
	return root, keys, nil
}

func (s Store) Get(provider, env string) (key, source string, err error) {
	if value := strings.TrimSpace(os.Getenv(env)); value != "" {
		return value, "environment", nil
	}
	_, keys, err := s.load()
	if err != nil {
		return "", "", err
	}
	if value := strings.TrimSpace(keys[provider]); value != "" {
		return value, "store", nil
	}
	return "", "", fault.New(fault.Auth, fmt.Sprintf("%s API key missing; set %s or run decide providers connect %s", provider, env, provider))
}

func (s Store) Set(provider, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return fault.New(fault.Usage, "API key must be a single non-empty line")
	}
	return s.update(provider, key)
}

func (s Store) Remove(provider string) error { return s.update(provider, "") }

func (s Store) update(provider, key string) error {
	unlock, err := store.Lock(s.Path)
	if err != nil {
		return err
	}
	defer unlock()
	root, keys, err := s.load()
	if err != nil {
		return err
	}
	if key == "" {
		delete(keys, provider)
	} else {
		keys[provider] = key
	}
	root["providers"], err = json.Marshal(keys)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return store.Write(s.Path, append(b, '\n'))
}
