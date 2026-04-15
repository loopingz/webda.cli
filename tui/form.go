package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
)

// FieldDef represents a single field extracted from a JSON schema.
type FieldDef struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Enum        []string
	Format      string
}

const simpleSchemaThreshold = 5

// SchemaFields extracts field definitions from a JSON schema.
func SchemaFields(schema map[string]any) []FieldDef {
	if schema == nil {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	var fields []FieldDef
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		f := FieldDef{
			Name:     name,
			Required: required[name],
		}
		if t, ok := prop["type"].(string); ok {
			f.Type = t
		}
		if d, ok := prop["description"].(string); ok {
			f.Description = d
		}
		if format, ok := prop["format"].(string); ok {
			f.Format = format
		}
		if enumVals, ok := prop["enum"].([]any); ok {
			for _, e := range enumVals {
				f.Enum = append(f.Enum, fmt.Sprint(e))
			}
		}
		fields = append(fields, f)
	}
	return fields
}

// IsSimpleSchema returns true if the fields are simple enough for a single form.
func IsSimpleSchema(fields []FieldDef) bool {
	return len(fields) <= simpleSchemaThreshold
}

// BuildForm creates a huh.Form from field definitions and pre-filled values.
// Returns the form and a map that will be populated with results.
func BuildForm(fields []FieldDef, prefilled map[string]any) (*huh.Form, map[string]any) {
	results := map[string]any{}
	for k, v := range prefilled {
		results[k] = v
	}

	var formFields []huh.Field
	for _, f := range fields {
		field := buildField(f, results)
		if field != nil {
			formFields = append(formFields, field)
		}
	}

	if len(formFields) == 0 {
		return nil, results
	}

	if IsSimpleSchema(fields) {
		form := huh.NewForm(huh.NewGroup(formFields...))
		return form, results
	}

	// Multiple groups for complex schemas (batch 3 fields per group)
	var groups []*huh.Group
	for i := 0; i < len(formFields); i += 3 {
		end := i + 3
		if end > len(formFields) {
			end = len(formFields)
		}
		groups = append(groups, huh.NewGroup(formFields[i:end]...))
	}
	form := huh.NewForm(groups...)
	return form, results
}

func buildField(f FieldDef, results map[string]any) huh.Field {
	title := f.Name
	if f.Description != "" {
		title = f.Description
	}
	if f.Required {
		title += " *"
	}

	// Enum → select
	if len(f.Enum) > 0 {
		options := make([]huh.Option[string], len(f.Enum))
		for i, e := range f.Enum {
			options[i] = huh.NewOption(e, e)
		}
		initial := ""
		if v, ok := results[f.Name].(string); ok {
			initial = v
		}
		results[f.Name] = initial
		return huh.NewSelect[string]().
			Title(title).
			Options(options...).
			Value(ptrTo(results, f.Name))
	}

	switch f.Type {
	case "boolean":
		initial := false
		if v, ok := results[f.Name].(bool); ok {
			initial = v
		}
		results[f.Name] = initial
		return huh.NewConfirm().
			Title(title).
			Value(ptrToBool(results, f.Name))
	case "integer", "number":
		initial := ""
		if v, ok := results[f.Name]; ok {
			initial = fmt.Sprint(v)
		}
		results[f.Name] = initial
		return huh.NewInput().
			Title(title).
			Value(ptrTo(results, f.Name)).
			Validate(func(s string) error {
				if s == "" && !f.Required {
					return nil
				}
				if f.Type == "integer" {
					_, err := strconv.Atoi(s)
					return err
				}
				_, err := strconv.ParseFloat(s, 64)
				return err
			})
	default:
		// string
		initial := ""
		if v, ok := results[f.Name].(string); ok {
			initial = v
		}
		results[f.Name] = initial
		inp := huh.NewInput().
			Title(title).
			Value(ptrTo(results, f.Name))
		if f.Format == "password" {
			inp = inp.EchoMode(huh.EchoModePassword)
		}
		if f.Required {
			inp = inp.Validate(func(s string) error {
				if s == "" {
					return fmt.Errorf("%s is required", f.Name)
				}
				return nil
			})
		}
		return inp
	}
}

func ptrTo(m map[string]any, key string) *string {
	s := ""
	if v, ok := m[key].(string); ok {
		s = v
	}
	m[key] = s
	sp := &s
	m["__ptr_"+key] = sp
	return sp
}

func ptrToBool(m map[string]any, key string) *bool {
	b := false
	if v, ok := m[key].(bool); ok {
		b = v
	}
	m[key] = b
	bp := &b
	m["__ptr_"+key] = bp
	return bp
}

// FinalizeResults copies pointer values back into the results map
// and removes internal pointer keys.
func FinalizeResults(results map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range results {
		if strings.HasPrefix(k, "__ptr_") {
			realKey := k[6:]
			switch p := v.(type) {
			case *string:
				out[realKey] = *p
			case *bool:
				out[realKey] = *p
			}
		}
	}
	for k, v := range results {
		if strings.HasPrefix(k, "__ptr_") {
			continue
		}
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}
