package tui

import "testing"

func TestSchemaFields_String(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user": map[string]any{"type": "string", "description": "The username"},
		},
		"required": []any{"user"},
	}
	fields := SchemaFields(schema)
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	f := fields[0]
	if f.Name != "user" || f.Type != "string" || f.Description != "The username" || !f.Required {
		t.Errorf("unexpected field: %+v", f)
	}
}

func TestSchemaFields_Mixed(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":   map[string]any{"type": "string"},
			"age":    map[string]any{"type": "integer"},
			"active": map[string]any{"type": "boolean"},
			"role":   map[string]any{"type": "string", "enum": []any{"admin", "user", "viewer"}},
			"score":  map[string]any{"type": "number"},
		},
		"required": []any{"name"},
	}
	fields := SchemaFields(schema)
	if len(fields) != 5 {
		t.Fatalf("expected 5 fields, got %d", len(fields))
	}
	byName := map[string]FieldDef{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	if !byName["name"].Required {
		t.Error("name should be required")
	}
	if byName["age"].Type != "integer" {
		t.Error("age should be integer")
	}
	if byName["active"].Type != "boolean" {
		t.Error("active should be boolean")
	}
	if len(byName["role"].Enum) != 3 {
		t.Errorf("role should have 3 enum values, got %d", len(byName["role"].Enum))
	}
}

func TestSchemaFields_Empty(t *testing.T) {
	fields := SchemaFields(nil)
	if len(fields) != 0 {
		t.Fatalf("expected 0 fields, got %d", len(fields))
	}
}

func TestIsSimpleSchema(t *testing.T) {
	simple := []FieldDef{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	if !IsSimpleSchema(simple) {
		t.Error("3 fields should be simple")
	}
	complex := []FieldDef{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
		{Name: "d"}, {Name: "e"}, {Name: "f"},
	}
	if IsSimpleSchema(complex) {
		t.Error("6 fields should not be simple")
	}
}
