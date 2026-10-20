// profile_test.go
package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfig_LegacyStringsAndUpdate(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("myapp: https://app.example.com\nupdate: prompt\n"), &c); err != nil {
		t.Fatal(err)
	}
	if c.Update != "prompt" {
		t.Fatalf("update = %q", c.Update)
	}
	p := c.Profiles["myapp"]
	if p.URL != "https://app.example.com" || p.EffectiveClientID() != "webda-cli" || p.EffectiveScope() != "* offline_access" {
		t.Fatalf("profile = %+v", p)
	}
	if _, ok := c.Profiles["update"]; ok {
		t.Fatal("update must not be a profile")
	}
}

func TestConfig_ModelGroups(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("myapp: https://app.example.com\nmodel_groups: both\n"), &c); err != nil {
		t.Fatal(err)
	}
	if c.ModelGroups != "both" {
		t.Fatalf("model_groups = %q", c.ModelGroups)
	}
	if _, ok := c.Profiles["model_groups"]; ok {
		t.Fatal("model_groups must not be a profile")
	}
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "model_groups: both") {
		t.Fatalf("marshaled config lost model_groups:\n%s", out)
	}
}

func TestConfig_ObjectProfile(t *testing.T) {
	var c Config
	in := "lc:\n  url: https://my.loopingz.com\n  client_id: loopcenter-cli\n  scope: Task.* offline_access\n"
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["lc"]
	if p.URL != "https://my.loopingz.com" || p.ClientID != "loopcenter-cli" || p.Scope != "Task.* offline_access" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestConfig_RoundTripKeepsStrings(t *testing.T) {
	c := Config{Update: "silent", Profiles: map[string]Profile{
		"a": {URL: "https://a.example"},
		"b": {URL: "https://b.example", Scope: "Task.*"},
	}}
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "a: https://a.example") {
		t.Fatalf("URL-only profile must stay a string:\n%s", s)
	}
	if !strings.Contains(s, "b:\n    url: https://b.example") || !strings.Contains(s, "scope: Task.*") {
		t.Fatalf("object profile:\n%s", s)
	}
	var back Config
	if err := yaml.Unmarshal(out, &back); err != nil || back.Profiles["b"].Scope != "Task.*" || back.Update != "silent" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}

func TestConfig_RejectsBadEntries(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("x:\n  - 1\n"), &c); err == nil {
		t.Fatal("a sequence is not a profile")
	}
}
