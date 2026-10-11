package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
	Default     any
	Example     string
	EnumValues  []any
	Schema      map[string]any
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
			Schema:   prop,
			Default:  prop["default"],
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
		if examples, ok := prop["examples"].([]any); ok && len(examples) > 0 {
			f.Example = fmt.Sprint(examples[0])
		}
		if enumVals, ok := prop["enum"].([]any); ok {
			f.EnumValues = enumVals
			for _, e := range enumVals {
				f.Enum = append(f.Enum, fmt.Sprint(e))
			}
		}
		fields = append(fields, f)
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Required != fields[j].Required {
			return fields[i].Required
		}
		return fields[i].Name < fields[j].Name
	})
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

	for i := range fields {
		f := &fields[i]
		if _, ok := results[f.Name]; !ok && f.Default != nil {
			results[f.Name] = f.Default
		}
	}
	var required, optional []huh.Field
	for i := range fields {
		f := &fields[i]
		field := buildField(*f, results)
		if f.Required {
			required = append(required, field)
		} else {
			optional = append(optional, field)
		}
	}
	if len(fields) == 0 {
		return nil, results
	}
	var groups []*huh.Group
	addGroups := func(title string, items []huh.Field, hide func() bool) {
		for i := 0; i < len(items); i += 4 {
			end := min(i+4, len(items))
			group := huh.NewGroup(items[i:end]...).Title(fmt.Sprintf("%s · step %d of %d", title, i/4+1, (len(items)+3)/4))
			if hide != nil {
				group = group.WithHideFunc(hide)
			}
			groups = append(groups, group)
		}
	}
	addGroups("Required input", required, nil)
	showOptional := len(required) == 0
	for i := range fields {
		f := &fields[i]
		if !f.Required {
			if _, ok := prefilled[f.Name]; ok {
				showOptional = true
			}
		}
	}
	if len(optional) > 0 && len(required) > 0 {
		groups = append(groups, huh.NewGroup(huh.NewConfirm().Title("More options").Description("Edit optional fields? Existing values and defaults are preserved when skipped.").Value(&showOptional)))
	}
	addGroups("Optional input", optional, func() bool { return !showOptional })

	form := huh.NewForm(groups...)
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		form = form.WithTheme(huh.ThemeBase())
	}
	return form, results
}

func buildField(f FieldDef, results map[string]any) huh.Field {
	title := f.Name
	description := f.Description
	if f.Example != "" {
		description += " Example: " + f.Example
	}
	if f.Required {
		title += " *"
	}

	// Enum → select
	if len(f.Enum) > 0 {
		options := make([]huh.Option[string], len(f.Enum))
		for i, e := range f.Enum {
			token := e
			if i < len(f.EnumValues) {
				data, _ := json.Marshal(f.EnumValues[i])
				token = string(data)
			}
			options[i] = huh.NewOption(e, token)
		}
		initial := ""
		if v, ok := results[f.Name]; ok {
			if len(f.EnumValues) > 0 {
				data, _ := json.Marshal(v)
				initial = string(data)
			} else {
				initial = fmt.Sprint(v)
			}
		}
		if !f.Required {
			options = append([]huh.Option[string]{huh.NewOption("Leave unset", "")}, options...)
		}
		results[f.Name] = initial
		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(options...).
			Value(ptrTo(results, f.Name))
	}

	switch f.Type {
	case "boolean":
		if !f.Required {
			initial := ""
			if v, ok := results[f.Name]; ok {
				initial = fmt.Sprint(v)
			}
			results[f.Name] = initial
			return huh.NewSelect[string]().Title(title).Description(description).Options(huh.NewOption("Leave unset", ""), huh.NewOption("Yes", "true"), huh.NewOption("No", "false")).Value(ptrTo(results, f.Name))
		}
		initial := false
		if v, ok := results[f.Name].(bool); ok {
			initial = v
		}
		results[f.Name] = initial
		return huh.NewConfirm().
			Title(title).
			Description(description).
			Value(ptrToBool(results, f.Name))
	case "integer", "number":
		initial := ""
		if v, ok := results[f.Name]; ok {
			initial = fmt.Sprint(v)
		}
		results[f.Name] = initial
		return huh.NewInput().
			Title(title).
			Description(description).
			Value(ptrTo(results, f.Name)).
			Validate(func(s string) error {
				if s == "" && !f.Required {
					return nil
				}
				if f.Type == "integer" {
					v, err := strconv.ParseInt(s, 10, 64)
					if err != nil {
						return fmt.Errorf("enter a whole number")
					}
					return validateValue(f.Schema, v, f.Name)
				}
				v, err := strconv.ParseFloat(s, 64)
				if err != nil {
					return fmt.Errorf("enter a number")
				}
				return validateValue(f.Schema, v, f.Name)
			})
	case "object", "array":
		initial := ""
		if v, ok := results[f.Name]; ok {
			data, _ := json.MarshalIndent(v, "", "  ")
			initial = string(data)
		}
		results[f.Name] = initial
		return huh.NewText().Title(title).Description(description + " (JSON)").Value(ptrTo(results, f.Name)).Validate(func(s string) error {
			if s == "" && !f.Required {
				return nil
			}
			var v any
			if err := json.Unmarshal([]byte(s), &v); err != nil {
				return fmt.Errorf("enter valid JSON: %w", err)
			}
			return validateValue(f.Schema, v, f.Name)
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
			Description(description).
			Value(ptrTo(results, f.Name))
		if f.Format == "password" {
			inp = inp.EchoMode(huh.EchoModePassword)
		}
		inp = inp.Validate(func(s string) error {
			if s == "" && !f.Required {
				return nil
			}
			return validateValue(f.Schema, s, f.Name)
		})
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
