package main

import (
	"strings"
	"unicode"
)

// camelToKebab converts camelCase or PascalCase strings to kebab-case.
// Consecutive uppercase letters are treated as an acronym (e.g., "HTTP" stays together).
func camelToKebab(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) {
					b.WriteRune('-')
				} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					b.WriteRune('-')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitOperationName splits "Foo.Bar.Baz" into ["foo", "bar", "baz"],
// converting each segment from camelCase to kebab-case.
func splitOperationName(name string) []string {
	parts := strings.Split(name, ".")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = camelToKebab(p)
	}
	return out
}
