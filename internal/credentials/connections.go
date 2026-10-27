package credentials

import (
	"encoding/json"
	"strings"

	"github.com/koltyakov/decide/internal/fault"
	"github.com/koltyakov/decide/internal/store"
)

type Connection struct {
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model,omitempty"`
}

type Connections struct {
	Default   string                `json:"defaultProvider,omitempty"`
	Providers map[string]Connection `json:"connections"`
}

func connections(root map[string]json.RawMessage) (Connections, error) {
	c := Connections{Providers: map[string]Connection{}}
	if value, ok := root["defaultProvider"]; ok {
		if json.Unmarshal(value, &c.Default) != nil {
			return c, fault.New(fault.Auth, "invalid saved default provider")
		}
	}
	if value, ok := root["connections"]; ok {
		if json.Unmarshal(value, &c.Providers) != nil || c.Providers == nil {
			return c, fault.New(fault.Auth, "invalid saved provider connections")
		}
	}
	return c, nil
}

func (s Store) Connections() (Connections, error) {
	root, _, err := s.load()
	if err != nil {
		return Connections{}, err
	}
	return connections(root)
}

// SaveConnection saves the key, connection settings, and first default atomically.
// A blank key means credentials remain in the environment, not that a key is removed.
func (s Store) SaveConnection(name, key string, connection Connection) error {
	key = strings.TrimSpace(key)
	if strings.ContainsAny(key, "\r\n") {
		return fault.New(fault.Usage, "API key must be one line")
	}
	return s.editConnections(func(c *Connections, keys map[string]string) {
		if key != "" {
			keys[name] = key
		}
		c.Providers[name] = connection
		if c.Default == "" {
			c.Default = name
		}
	})
}

func (s Store) Use(name string) error {
	return s.editConnections(func(c *Connections, _ map[string]string) { c.Default = name })
}

func (s Store) Disconnect(name string) error {
	return s.editConnections(func(c *Connections, keys map[string]string) {
		delete(c.Providers, name)
		delete(keys, name)
		if c.Default == name {
			c.Default = ""
		}
	})
}

func (s Store) editConnections(edit func(*Connections, map[string]string)) error {
	unlock, err := store.Lock(s.Path)
	if err != nil {
		return err
	}
	defer unlock()
	root, keys, err := s.load()
	if err != nil {
		return err
	}
	c, err := connections(root)
	if err != nil {
		return err
	}
	edit(&c, keys)
	root["defaultProvider"], err = json.Marshal(c.Default)
	if err != nil {
		return err
	}
	root["connections"], err = json.Marshal(c.Providers)
	if err != nil {
		return err
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
