package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/itchyny/gojq"
)

var jqIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// splitJQ splits only outside strings and nested syntax, tolerating an unfinished
// final segment. Unknown transformations are deliberately not evaluated.
func splitJQ(text string, delimiter byte) []string {
	var parts []string
	start, depth := 0, 0
	quoted, escaped := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
		default:
			if c == delimiter && depth == 0 {
				parts = append(parts, text[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, text[start:])
}

func completeQuery(root map[string]any, text string) []string {
	if root == nil || len(text) > 8192 {
		return nil
	}
	schemas := []map[string]any{root}
	pipes := splitJQ(text, '|')
	offset := 0
	for _, pipe := range pipes[:len(pipes)-1] {
		var ok bool
		schemas, _, ok = queryPath(root, schemas, strings.TrimSpace(pipe), false)
		if !ok {
			return nil
		}
		offset += len(pipe) + 1
	}
	tail := text[offset:]
	leading := len(tail) - len(strings.TrimLeft(tail, " \t"))
	offset += leading
	tail = tail[leading:]
	// Complete a shorthand projection key, or the value path after a literal key.
	if strings.HasPrefix(tail, "{") {
		fields := splitJQ(tail[1:], ',')
		fieldOffset := offset + 1
		for _, previous := range fields[:len(fields)-1] {
			if _, err := gojq.Parse("{" + previous + "}"); err != nil {
				return nil
			}
			fieldOffset += len(previous) + 1
		}
		field := fields[len(fields)-1]
		leading = len(field) - len(strings.TrimLeft(field, " \t"))
		fieldOffset += leading
		field = field[leading:]
		pair := splitJQ(field, ':')
		if len(pair) == 1 {
			if field != "" && !jqIdentifier.MatchString(field) {
				return nil
			}
			return queryPropertySuggestions(root, schemas, text[:fieldOffset], field, "shorthand")
		}
		if len(pair) != 2 {
			return nil
		}
		key := strings.TrimSpace(pair[0])
		if !jqIdentifier.MatchString(key) {
			var literal string
			if json.Unmarshal([]byte(key), &literal) != nil {
				return nil
			}
		}
		fieldOffset += len(pair[0]) + 1
		value := pair[1]
		leading = len(value) - len(strings.TrimLeft(value, " \t"))
		offset = fieldOffset + leading
		tail = value[leading:]
	}
	if tail == "" {
		tail = "."
		text += "."
	}
	_, result, _ := queryPath(root, schemas, tail, true)
	for i := range result {
		result[i] = text[:offset] + result[i]
	}
	return result
}

// queryPath traverses only identity, literal property access, [] and numeric
// indices. Its completion mode accepts the final unfinished property token.
func queryPath(root map[string]any, schemas []map[string]any, path string, completing bool) ([]map[string]any, []string, bool) {
	if path == "" || path[0] != '.' {
		return nil, nil, false
	}
	pos := 1
	property := true
	for {
		if property {
			start := pos
			for pos < len(path) && ((path[pos] >= 'a' && path[pos] <= 'z') || (path[pos] >= 'A' && path[pos] <= 'Z') || path[pos] == '_' || (pos > start && path[pos] >= '0' && path[pos] <= '9')) {
				pos++
			}
			key := path[start:pos]
			if pos == len(path) && completing {
				return nil, queryPropertySuggestions(root, schemas, path[:start], key, "dot"), true
			}
			if key != "" {
				schemas = queryChild(root, schemas, key, false)
			} else if pos < len(path) && path[pos] != '[' {
				return nil, nil, false
			}
			property = false
		}
		if pos == len(path) {
			return schemas, nil, len(schemas) > 0
		}
		switch path[pos] {
		case '.':
			pos++
			property = true
		case '[':
			start := pos
			pos++
			if pos < len(path) && path[pos] == '"' {
				quoteStart := pos
				pos++
				for pos < len(path) {
					if path[pos] == '\\' {
						pos += 2
						continue
					}
					if path[pos] == '"' {
						break
					}
					pos++
				}
				if pos >= len(path) {
					if !completing {
						return nil, nil, false
					}
					var partial string
					if json.Unmarshal([]byte(path[quoteStart:]+"\""), &partial) != nil {
						return nil, nil, false
					}
					return nil, queryPropertySuggestions(root, schemas, path[:start], partial, "bracket"), true
				}
				var key string
				if json.Unmarshal([]byte(path[quoteStart:pos+1]), &key) != nil {
					return nil, nil, false
				}
				pos++
				if pos >= len(path) || path[pos] != ']' {
					return nil, nil, false
				}
				pos++
				schemas = queryChild(root, schemas, key, false)
			} else {
				end := strings.IndexByte(path[pos:], ']')
				if end < 0 {
					return nil, nil, false
				}
				index := path[pos : pos+end]
				if index != "" {
					if _, err := strconv.Atoi(index); err != nil {
						return nil, nil, false
					}
				}
				pos += end + 1
				schemas = queryChild(root, schemas, "", true)
			}
		default:
			return nil, nil, false
		}
		if len(schemas) == 0 {
			return nil, nil, false
		}
	}
}

func queryPropertySuggestions(root map[string]any, schemas []map[string]any, before, partial, style string) []string {
	props := map[string]any{}
	for _, schema := range schemas {
		for _, choice := range querySchemaChoices(root, schema, 0) {
			fields, _ := choice["properties"].(map[string]any)
			for key, value := range fields {
				props[key] = value
			}
		}
	}
	var out []string
	for _, key := range sortedKeys(props) {
		if !strings.HasPrefix(key, partial) {
			continue
		}
		encoded, _ := json.Marshal(key)
		var candidate string
		switch style {
		case "shorthand":
			if !jqIdentifier.MatchString(key) {
				continue
			}
			candidate = before + key
		case "bracket":
			candidate = before + "[" + string(encoded) + "]"
		default:
			if jqIdentifier.MatchString(key) {
				candidate = before + key
			} else {
				// .["key"] at the root, .parent["key"] after an existing path.
				prefix := before
				if len(prefix) > 1 {
					prefix = strings.TrimSuffix(prefix, ".")
				}
				candidate = prefix + "[" + string(encoded) + "]"
			}
		}
		prop, _ := props[key].(map[string]any)
		typ, _ := prop["type"].(string)
		description, _ := prop["description"].(string)
		description = strings.Join(strings.Fields(typ+" "+description), " ")
		if description != "" {
			candidate += "\t" + description
		}
		out = append(out, candidate)
	}
	return out
}

func queryChild(root map[string]any, schemas []map[string]any, key string, array bool) []map[string]any {
	var children []map[string]any
	for _, schema := range schemas {
		for _, choice := range querySchemaChoices(root, schema, 0) {
			var child map[string]any
			if array {
				child, _ = choice["items"].(map[string]any)
			} else {
				properties, _ := choice["properties"].(map[string]any)
				child, _ = properties[key].(map[string]any)
			}
			if child != nil {
				children = append(children, child)
			}
		}
	}
	return children
}

// Resolve local JSON pointers and unions with a fixed budget for cyclic schemas.
func querySchemaChoices(root, schema map[string]any, depth int) []map[string]any {
	if schema == nil || depth >= 32 {
		return nil
	}
	choices := []map[string]any{schema}
	if ref, ok := schema["$ref"].(string); ok {
		if strings.HasPrefix(ref, "#/") {
			var target any = root
			for _, segment := range strings.Split(ref[2:], "/") {
				object, _ := target.(map[string]any)
				target = object[strings.NewReplacer("~1", "/", "~0", "~").Replace(segment)]
			}
			resolved, _ := target.(map[string]any)
			choices = append(choices, querySchemaChoices(root, resolved, depth+1)...)
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		alternatives, _ := schema[keyword].([]any)
		for _, alternative := range alternatives {
			child, _ := alternative.(map[string]any)
			choices = append(choices, querySchemaChoices(root, child, depth+1)...)
		}
	}
	return choices
}
