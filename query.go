package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/itchyny/gojq"
)

func compileQuery(text string, supplied bool) (*gojq.Code, error) {
	if !supplied {
		return nil, nil
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("--query requires a jq expression")
	}
	query, err := gojq.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("invalid --query: %w", err)
	}
	// No module loader, input iterator, or host environment is exposed.
	code, err := gojq.Compile(query, gojq.WithEnvironLoader(func() []string { return nil }))
	if err != nil {
		return nil, fmt.Errorf("invalid --query: %w", err)
	}
	return code, nil
}

// Buffer the complete result so evaluation/formatting errors never print partial output.
func renderQuery(ctx context.Context, data []byte, format string, code *gojq.Code) ([]byte, error) {
	if code == nil {
		return renderOutput(data, format)
	}
	if !validOutput(format) {
		return nil, fmt.Errorf("unknown output format %q", format)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("--query requires a JSON response: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("--query requires a single JSON response")
	}
	var out bytes.Buffer
	var rows []any
	iter := code.RunWithContext(ctx, value)
	for {
		result, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := result.(error); ok {
			return nil, fmt.Errorf("--query evaluation failed: %w", err)
		}
		if format == "table" {
			if array, ok := result.([]any); ok {
				rows = append(rows, array...)
			} else {
				rows = append(rows, result)
			}
			continue
		}
		if s, ok := result.(string); ok && format == "raw" {
			out.WriteString(s)
			out.WriteByte('\n')
			continue
		}
		encoded, err := gojq.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("--query result: %w", err)
		}
		if format == "raw" {
			out.Write(encoded)
			out.WriteByte('\n')
		} else {
			if err := json.Indent(&out, encoded, "", "  "); err != nil {
				return nil, err
			}
			out.WriteByte('\n')
		}
	}
	if format == "table" {
		if len(rows) == 0 {
			return nil, nil
		}
		return renderTableValue(rows)
	}
	return out.Bytes(), nil
}
