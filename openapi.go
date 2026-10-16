package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var openAPIMethods = []string{"get", "post", "put", "patch", "delete"}

// parseOpenAPI maps the paths of an OpenAPI document to operations.
func parseOpenAPI(doc map[string]any) []Operation {
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil
	}
	var out []Operation
	for pth, v := range paths {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		shared, _ := item["parameters"].([]any)
		for _, method := range openAPIMethods {
			spec, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			op := Operation{Path: pth, Method: strings.ToUpper(method), Source: "openapi", Raw: map[string]any{"openapi": spec}}
			op.Name, _ = spec["operationId"].(string)
			if op.Name == "" {
				op.Name = deriveOpName(method, pth)
			}
			op.Summary, _ = spec["summary"].(string)
			op.Description, _ = spec["description"].(string)
			if op.Description == "" {
				op.Description = op.Summary
			}
			op.Destructive = method == "delete"
			op.Input = openAPIInput(doc, shared, spec)
			out = append(out, op)
		}
	}
	return out
}

// resolveRef resolves a local #/components/schemas/ reference one level deep.
func resolveRef(doc map[string]any, schema map[string]any) (map[string]any, bool) {
	ref, ok := schema["$ref"].(string)
	if !ok {
		return schema, true
	}
	name, ok := strings.CutPrefix(ref, "#/components/schemas/")
	if !ok {
		return nil, false
	}
	comps, _ := doc["components"].(map[string]any)
	schemas, _ := comps["schemas"].(map[string]any)
	resolved, ok := schemas[name].(map[string]any)
	return resolved, ok
}

func openAPIInput(doc map[string]any, shared []any, spec map[string]any) map[string]any {
	props := map[string]any{}
	var required []any
	params, _ := spec["parameters"].([]any)
	for _, p := range append(append([]any{}, shared...), params...) {
		param, ok := p.(map[string]any)
		if !ok {
			continue
		}
		in, _ := param["in"].(string)
		name, _ := param["name"].(string)
		if name == "" || (in != "path" && in != "query") {
			continue
		}
		prop := map[string]any{"type": "string"}
		if s, ok := param["schema"].(map[string]any); ok {
			if r, ok := resolveRef(doc, s); ok {
				prop = r
			}
		}
		if d, ok := param["description"].(string); ok && prop["description"] == nil {
			prop = copyMap(prop)
			prop["description"] = d
		}
		props[name] = prop
		if req, _ := param["required"].(bool); req || in == "path" {
			required = append(required, name)
		}
	}
	if body, ok := spec["requestBody"].(map[string]any); ok {
		content, _ := body["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		if s, ok := media["schema"].(map[string]any); ok {
			if r, ok := resolveRef(doc, s); ok {
				bp, _ := r["properties"].(map[string]any)
				for k, v := range bp {
					if pm, ok := v.(map[string]any); ok {
						if resolved, ok := resolveRef(doc, pm); ok {
							v = resolved
						} else {
							v = map[string]any{"type": "string"}
						}
					}
					props[k] = v
				}
				if br, ok := r["required"].([]any); ok {
					required = append(required, br...)
				}
			}
		}
	}
	input := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		input["required"] = required
	}
	return input
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

var pathParamRegexp = regexp.MustCompile(`\{([^}/]+)\}`)

// buildRequest builds the HTTP request of an operation for its source (operations or openapi).
func buildRequest(ctx context.Context, op Operation, baseURL string, input map[string]any) (*http.Request, error) {
	base := strings.TrimRight(baseURL, "/")
	if op.Source != "openapi" {
		var reader io.Reader
		if len(input) > 0 {
			data, err := json.Marshal(input)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/operations/"+url.PathEscape(op.Name), reader)
		if err == nil && reader != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req, err
	}
	rest := copyMap(input)
	var missing []string
	target := pathParamRegexp.ReplaceAllStringFunc(op.Path, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := rest[name]
		if !ok {
			missing = append(missing, name)
			return m
		}
		delete(rest, name)
		return url.PathEscape(fmt.Sprint(v))
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s: missing path parameter %s", op.Name, strings.Join(missing, ", "))
	}
	method := op.Method
	if method == "" {
		method = http.MethodGet
	}
	var reader io.Reader
	switch method {
	case http.MethodGet, http.MethodDelete, http.MethodHead:
		if q := queryString(rest); q != "" {
			target += "?" + q
		}
	default:
		if len(rest) > 0 {
			data, err := json.Marshal(rest)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(data)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+target, reader)
	if err == nil && reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, err
}

func queryString(values map[string]any) string {
	q := url.Values{}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := values[k].(type) {
		case []any:
			for _, item := range v {
				q.Add(k, fmt.Sprint(item))
			}
		case nil:
		default:
			q.Add(k, fmt.Sprint(v))
		}
	}
	return q.Encode()
}
