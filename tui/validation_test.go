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
