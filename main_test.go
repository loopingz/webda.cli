package main

import (
	"os"
	"testing"
)

func TestExtractToken(t *testing.T) {
	body := []byte(`{"refresh_token":"R123","sequence":42}`)
	ref, seq := extractToken(body)
	if ref != "R123" || seq != "42" {
		t.Fatalf("expected R123/42 got %s/%s", ref, seq)
	}
}

func TestParseTokenFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	content := "REFRESH\nACCESS\nSEQ123\n"
	if err := os.WriteFile(f.Name(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ti, err := parseTokenFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if ti.RefreshToken != "REFRESH" || ti.AccessToken != "ACCESS" || ti.Sequence != "SEQ123" {
		t.Fatalf("unexpected token info: %+v", ti)
	}
}
