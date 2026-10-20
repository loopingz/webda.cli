package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultClientID = "webda-cli"
	defaultScope    = "* offline_access"
	updateKey       = "update"
	modelGroupsKey  = "model_groups"
)

// Profile is one server the CLI talks to.
type Profile struct {
	URL      string `yaml:"url"`
	ClientID string `yaml:"client_id,omitempty"`
	Scope    string `yaml:"scope,omitempty"`
}

// EffectiveClientID returns the OAuth client id, webda-cli by default.
func (p Profile) EffectiveClientID() string {
	if p.ClientID != "" {
		return p.ClientID
	}
	return defaultClientID
}

// EffectiveScope returns the requested scope, "* offline_access" by default.
func (p Profile) EffectiveScope() string {
	if p.Scope != "" {
		return p.Scope
	}
	return defaultScope
}

// Config is ~/.webdacli/config.yaml: profile name -> URL string or Profile object, plus the update mode.
type Config struct {
	Profiles map[string]Profile
	Update   string
	// ModelGroups names the command groups of a model: singular (default), plural or both.
	ModelGroups string
}

// UnmarshalYAML accepts the legacy `name: url` form and the `name: {url, client_id, scope}` form.
func (c *Config) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: expected a mapping")
	}
	c.Profiles = map[string]Profile{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		if key == updateKey {
			c.Update = val.Value
			continue
		}
		if key == modelGroupsKey {
			c.ModelGroups = val.Value
			continue
		}
		switch val.Kind {
		case yaml.ScalarNode:
			c.Profiles[key] = Profile{URL: val.Value}
		case yaml.MappingNode:
			var p Profile
			if err := val.Decode(&p); err != nil {
				return fmt.Errorf("config: profile %q: %w", key, err)
			}
			c.Profiles[key] = p
		default:
			return fmt.Errorf("config: profile %q must be a URL or an object", key)
		}
	}
	return nil
}

// MarshalYAML writes URL-only profiles as plain strings (readable by older CLI versions).
func (c Config) MarshalYAML() (any, error) {
	out := map[string]any{}
	for name, p := range c.Profiles {
		if p.ClientID == "" && p.Scope == "" {
			out[name] = p.URL
		} else {
			out[name] = p
		}
	}
	if c.Update != "" {
		out[updateKey] = c.Update
	}
	if c.ModelGroups != "" {
		out[modelGroupsKey] = c.ModelGroups
	}
	return out, nil
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(filepath.Join(configDir(), configFileName))
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func saveConfig(c Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(configDir(), configFileName), data)
}
