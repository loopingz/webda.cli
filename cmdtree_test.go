package main

import "testing"

func TestCamelToKebab(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"testOperations", "test-operations"},
		{"AuthorizerService", "authorizer-service"},
		{"MFA", "mfa"},
		{"SMS", "sms"},
		{"getHTTPResponse", "get-http-response"},
		{"already-kebab", "already-kebab"},
		{"simple", "simple"},
		{"ABCDef", "abc-def"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := camelToKebab(tt.in)
			if got != tt.want {
				t.Errorf("camelToKebab(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitOperationName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"two parts", "AuthorizerService.testOperations", []string{"authorizer-service", "test-operations"}},
		{"three parts", "Foo.Bar.Baz", []string{"foo", "bar", "baz"}},
		{"acronyms", "MFA.SMS", []string{"mfa", "sms"}},
		{"single", "Deploy", []string{"deploy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitOperationName(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitOperationName(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitOperationName(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}
