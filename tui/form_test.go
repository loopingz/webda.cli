package tui

import "testing"

func TestBuildForm_SimpleString(t *testing.T) {
	fields := []FieldDef{
		{Name: "user", Type: "string", Required: true},
	}
	form, results := BuildForm(fields, nil)
	if form == nil {
		t.Fatal("expected non-nil form")
	}
	if results == nil {
		t.Fatal("expected non-nil results")
	}
}

func TestBuildForm_PrefilledValues(t *testing.T) {
	fields := []FieldDef{
		{Name: "user", Type: "string"},
	}
	prefilled := map[string]any{"user": "alice"}
	_, results := BuildForm(fields, prefilled)
	// After BuildForm, the ptrTo helper should have set up the binding
	if results == nil {
		t.Fatal("expected results map")
	}
}

func TestBuildForm_Boolean(t *testing.T) {
	fields := []FieldDef{
		{Name: "active", Type: "boolean"},
	}
	form, results := BuildForm(fields, map[string]any{"active": true})
	if form == nil {
		t.Fatal("expected non-nil form")
	}
	if results == nil {
		t.Fatal("expected non-nil results")
	}
}

func TestBuildForm_Enum(t *testing.T) {
	fields := []FieldDef{
		{Name: "role", Type: "string", Enum: []string{"admin", "user"}},
	}
	form, results := BuildForm(fields, nil)
	if form == nil {
		t.Fatal("expected non-nil form")
	}
	if results == nil {
		t.Fatal("expected non-nil results")
	}
}

func TestBuildForm_Number(t *testing.T) {
	fields := []FieldDef{
		{Name: "count", Type: "integer", Required: true},
		{Name: "score", Type: "number"},
	}
	form, _ := BuildForm(fields, nil)
	if form == nil {
		t.Fatal("expected non-nil form")
	}
}

func TestBuildForm_Password(t *testing.T) {
	fields := []FieldDef{
		{Name: "secret", Type: "string", Format: "password"},
	}
	form, _ := BuildForm(fields, nil)
	if form == nil {
		t.Fatal("expected non-nil form")
	}
}

func TestBuildForm_ComplexSchema(t *testing.T) {
	// More than 5 fields → should create multiple groups
	fields := []FieldDef{
		{Name: "a", Type: "string"},
		{Name: "b", Type: "string"},
		{Name: "c", Type: "string"},
		{Name: "d", Type: "string"},
		{Name: "e", Type: "string"},
		{Name: "f", Type: "string"},
	}
	form, _ := BuildForm(fields, nil)
	if form == nil {
		t.Fatal("expected non-nil form for complex schema")
	}
}

func TestBuildForm_Empty(t *testing.T) {
	form, results := BuildForm(nil, nil)
	if form != nil {
		t.Fatal("expected nil form for nil fields")
	}
	if results == nil {
		t.Fatal("expected non-nil results even with nil fields")
	}
}

func TestFinalizeResults(t *testing.T) {
	results := map[string]any{}

	// Simulate what ptrTo does
	s := "hello"
	results["name"] = "old"
	results["__ptr_name"] = &s

	b := true
	results["active"] = false
	results["__ptr_active"] = &b

	out := FinalizeResults(results)
	if out["name"] != "hello" {
		t.Errorf("expected pointer value 'hello', got %v", out["name"])
	}
	if out["active"] != true {
		t.Errorf("expected pointer value true, got %v", out["active"])
	}
}

func TestFinalizeResults_NoPointers(t *testing.T) {
	results := map[string]any{
		"user": "alice",
		"age":  25,
	}
	out := FinalizeResults(results)
	if out["user"] != "alice" {
		t.Errorf("expected 'alice', got %v", out["user"])
	}
	if out["age"] != 25 {
		t.Errorf("expected 25, got %v", out["age"])
	}
}

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
