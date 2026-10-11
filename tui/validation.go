package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ValidateInput applies schema defaults and validates supported inline JSON Schema
// constraints identically for flags, files, and forms. The server remains the
// authority for reference schemas and constraints not supported here.
func ValidateInput(schema map[string]any, input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		prop, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := out[key]; !exists {
			if value, ok := prop["default"]; ok {
				out[key] = value
			}
		}
	}
	if err := validateValue(schema, out, "input"); err != nil {
		return nil, err
	}
	return out, nil
}
func validateValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return nil
	}
	typ, _ := schema["type"].(string)
	if typ == "" {
		if _, ok := schema["properties"]; ok {
			typ = "object"
		}
	}
	fail := func() error { return fmt.Errorf("%s must be %s", path, typ) }
	switch typ {
	case "object":
		m, ok := value.(map[string]any)
		if !ok {
			return fail()
		}
		required, _ := schema["required"].([]any)
		for _, r := range required {
			key, ok := r.(string)
			if ok {
				if _, exists := m[key]; !exists {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for key, v := range m {
			prop, ok := props[key].(map[string]any)
			if !ok {
				if allow, ok := schema["additionalProperties"].(bool); ok && !allow {
					return fmt.Errorf("%s.%s is unknown", path, key)
				}
				continue
			}
			if err := validateValue(prop, v, path+"."+key); err != nil {
				return err
			}
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return fail()
		}
		if min, ok := numeric(schema["minItems"]); ok && float64(len(a)) < min {
			return fmt.Errorf("%s needs at least %g items", path, min)
		}
		if max, ok := numeric(schema["maxItems"]); ok && float64(len(a)) > max {
			return fmt.Errorf("%s allows at most %g items", path, max)
		}
		itemSchema, _ := schema["items"].(map[string]any)
		for i, v := range a {
			if err := validateValue(itemSchema, v, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return fail()
		}
		if min, ok := numeric(schema["minLength"]); ok && float64(utf8.RuneCountInString(s)) < min {
			return fmt.Errorf("%s needs at least %g characters", path, min)
		}
		if max, ok := numeric(schema["maxLength"]); ok && float64(utf8.RuneCountInString(s)) > max {
			return fmt.Errorf("%s allows at most %g characters", path, max)
		}
		if pattern, ok := schema["pattern"].(string); ok {
			r, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("%s: invalid schema pattern", path)
			}
			if !r.MatchString(s) {
				return fmt.Errorf("%s does not match %s", path, pattern)
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fail()
		}
	case "integer", "number":
		n, ok := numeric(value)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return fail()
		}
		if typ == "integer" && math.Trunc(n) != n {
			return fail()
		}
		if min, ok := numeric(schema["minimum"]); ok && n < min {
			return fmt.Errorf("%s must be at least %g", path, min)
		}
		if max, ok := numeric(schema["maximum"]); ok && n > max {
			return fmt.Errorf("%s must be at most %g", path, max)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, candidate := range enum {
			a, _ := json.Marshal(candidate)
			b, _ := json.Marshal(value)
			if bytes.Equal(a, b) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s must be one of %v", path, enum)
		}
	}
	return nil
}
func numeric(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, e := v.Float64()
		return n, e == nil
	}
	return 0, false
}

// NormalizeForm converts textual bindings back to their schema types and keeps
// untouched optional values absent. Existing explicit zero values are preserved.
func NormalizeForm(fields []FieldDef, original, results map[string]any) (map[string]any, error) {
	out := FinalizeResults(results)
	for i := range fields {
		f := &fields[i]
		value := out[f.Name]
		_, provided := original[f.Name]
		if !f.Required && (value == "") && (f.Type != "string" || len(f.Enum) > 0 || !provided && f.Default == nil) {
			delete(out, f.Name)
			continue
		}
		s, ok := value.(string)
		if !ok {
			continue
		}
		if len(f.Enum) > 0 {
			for i, label := range f.Enum {
				if i < len(f.EnumValues) {
					token, _ := json.Marshal(f.EnumValues[i])
					if string(token) == s {
						out[f.Name] = f.EnumValues[i]
						break
					}
				} else if label == s {
					out[f.Name] = label
					break
				}
			}
			continue
		}
		var err error
		switch f.Type {
		case "boolean":
			out[f.Name], err = strconv.ParseBool(s)
		case "integer":
			out[f.Name], err = strconv.ParseInt(s, 10, 64)
		case "number":
			out[f.Name], err = strconv.ParseFloat(s, 64)
		case "object", "array":
			var decoded any
			err = json.Unmarshal([]byte(s), &decoded)
			out[f.Name] = decoded
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return out, nil
}
func RedactInput(schema map[string]any, input map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for key, value := range input {
		prop, _ := props[key].(map[string]any)
		format, _ := prop["format"].(string)
		writeOnly, _ := prop["writeOnly"].(bool)
		lower := strings.ToLower(key)
		if format == "password" || writeOnly || strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			out[key] = "[redacted]"
			continue
		}
		if m, ok := value.(map[string]any); ok {
			out[key] = RedactInput(prop, m)
		} else if a, ok := value.([]any); ok {
			items, _ := prop["items"].(map[string]any)
			copyItems := make([]any, len(a))
			for i, item := range a {
				if m, ok := item.(map[string]any); ok {
					copyItems[i] = RedactInput(items, m)
				} else {
					copyItems[i] = item
				}
			}
			out[key] = copyItems
		} else {
			out[key] = value
		}
	}
	return out
}
