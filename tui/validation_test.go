package tui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTypedFormAndOmission(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"count": map[string]any{"type": "integer"}, "score": map[string]any{"type": "number"}, "active": map[string]any{"type": "boolean"}, "optional": map[string]any{"type": "boolean"}, "level": map[string]any{"type": "integer", "enum": []any{1, 2}}, "items": map[string]any{"type": "array"}, "config": map[string]any{"type": "object"},
	}}
	fields := SchemaFields(schema)
	original := map[string]any{"count": 0, "score": 0.0, "active": false, "level": 2, "items": []any{1.0}, "config": map[string]any{"x": true}}
	_, bindings := BuildForm(fields, original)
	result, err := NormalizeForm(fields, original, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if result["count"] != int64(0) || result["score"] != 0.0 || result["active"] != false || result["level"] != 2 {
		t.Fatalf("incorrect types/zero values: %#v", result)
	}
	if _, exists := result["optional"]; exists {
		t.Fatal("untouched optional field included")
	}
	if !reflect.DeepEqual(result["items"], []any{1.0}) || !reflect.DeepEqual(result["config"], map[string]any{"x": true}) {
		t.Fatalf("structured data lost: %#v", result)
	}
}
func TestOptionalBooleanExplicitFalse(t *testing.T) {
	fields := []FieldDef{{Name: "active", Type: "boolean"}}
	_, bindings := BuildForm(fields, nil)
	*bindings["__ptr_active"].(*string) = "false"
	result, err := NormalizeForm(fields, nil, bindings)
	if err != nil || result["active"] != false {
		t.Fatalf("explicit false lost: %#v %v", result, err)
	}
}
func TestSchemaOrderDefaultsAndValidation(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"z"}, "properties": map[string]any{"a": map[string]any{"type": "integer", "minimum": 1, "default": 2}, "z": map[string]any{"type": "string", "minLength": 2}, "b": map[string]any{"type": "string", "enum": []any{"yes", "no"}}}}
	fields := SchemaFields(schema)
	if fields[0].Name != "z" || fields[1].Name != "a" || fields[2].Name != "b" {
		t.Fatalf("unexpected order: %#v", fields)
	}
	input := map[string]any{"z": "ok"}
	out, err := ValidateInput(schema, input)
	if err != nil || out["a"] != 2 {
		t.Fatalf("default not applied: %#v %v", out, err)
	}
	if _, ok := input["a"]; ok {
		t.Fatal("validation mutated source input")
	}
	for _, input := range []map[string]any{{}, {"z": "x"}, {"z": "ok", "a": 0}, {"z": "ok", "a": 1.5}, {"z": "ok", "b": "invalid"}} {
		if _, err := ValidateInput(schema, input); err == nil {
			t.Fatalf("invalid input accepted: %#v", input)
		}
	}
}
func TestNestedRedaction(t *testing.T) {
	input := map[string]any{"password": "sensitive", "nested": map[string]any{"token": "sensitive"}, "items": []any{map[string]any{"secret": "sensitive"}}, "custom": "sensitive", "name": "alice"}
	schema := map[string]any{"properties": map[string]any{"custom": map[string]any{"writeOnly": true}}}
	out := RedactInput(schema, input)
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "sensitive") {
		t.Fatal("secret leaked")
	}
	if out["password"] != "[redacted]" || out["custom"] != "[redacted]" || out["nested"].(map[string]any)["token"] != "[redacted]" || out["items"].([]any)[0].(map[string]any)["secret"] != "[redacted]" || out["name"] != "alice" {
		t.Fatalf("redaction failed: %#v", out)
	}
	if input["password"] != "sensitive" {
		t.Fatal("redaction mutated input")
	}
}

func TestNestedSchemaConstraints(t *testing.T) {
	tests := []struct {
		name           string
		schema         map[string]any
		valid, invalid any
		message        string
	}{
		{"object type", map[string]any{"type": "object"}, map[string]any{}, "text", "must be object"},
		{"boolean type", map[string]any{"type": "boolean"}, false, "false", "must be boolean"},
		{"array type", map[string]any{"type": "array"}, []any{}, "[]", "must be array"},
		{"array minimum", map[string]any{"type": "array", "minItems": 2}, []any{1, 2}, []any{1}, "at least 2 items"},
		{"array maximum", map[string]any{"type": "array", "maxItems": 1}, []any{1}, []any{1, 2}, "at most 1 items"},
		{"array item type", map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, []any{1}, []any{"one"}, "[0] must be integer"},
		{"string type", map[string]any{"type": "string"}, "name", true, "must be string"},
		{"string maximum", map[string]any{"type": "string", "maxLength": 2}, "é好", "abc", "at most 2 characters"},
		{"string pattern", map[string]any{"type": "string", "pattern": "^[a-z]+$"}, "alice", "123", "does not match"},
		{"number maximum", map[string]any{"type": "number", "maximum": 2}, 2.0, 3.0, "at most 2"},
		{"unknown property", map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "additionalProperties": false}, map[string]any{"name": "alice"}, map[string]any{"typo": "alice"}, "typo is unknown"},
		{"nested required", map[string]any{"type": "object", "properties": map[string]any{"user": map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}}}}, map[string]any{"user": map[string]any{"name": "alice"}}, map[string]any{"user": map[string]any{}}, "user.name is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateValue(tc.schema, tc.valid, "input"); err != nil {
				t.Fatalf("valid input rejected: %v", err)
			}
			if err := validateValue(tc.schema, tc.invalid, "input"); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q, got %v", tc.message, err)
			}
		})
	}
	if err := validateValue(map[string]any{"type": "string", "pattern": "["}, "alice", "input"); err == nil || !strings.Contains(err.Error(), "invalid schema pattern") {
		t.Fatalf("malformed schema not reported: %v", err)
	}
}
func TestTypedEnumsIncludingEmptyString(t *testing.T) {
	for _, value := range []any{false, 2, "", "2"} {
		schema := map[string]any{"properties": map[string]any{"choice": map[string]any{"enum": []any{false, 2, "", "2"}}}}
		fields := SchemaFields(schema)
		original := map[string]any{"choice": value}
		_, bindings := BuildForm(fields, original)
		out, err := NormalizeForm(fields, original, bindings)
		if err != nil || !reflect.DeepEqual(out["choice"], value) {
			t.Fatalf("enum type lost: %#v → %#v (%v)", value, out, err)
		}
	}
}
