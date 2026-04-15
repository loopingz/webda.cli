package main

import "testing"

func TestParseOperations_NormalMap(t *testing.T) {
	body := []byte(`{
		"operations": {
			"Sync.AWS": {"id":"Sync.AWS"},
			"MFA.SMS": {
				"id":"MFA.SMS",
				"input":{"type":"object","properties":{"phone":{"type":"string"}},"required":["phone"]},
				"output":{"type":"object","properties":{"message":{"type":"string"}}}
			}
		}
	}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	for _, o := range ops {
		if o.Name == "MFA.SMS" {
			if o.Input == nil {
				t.Fatal("expected Input to be set for MFA.SMS")
			}
			if o.Input["type"] != "object" {
				t.Fatalf("expected Input type=object, got %v", o.Input["type"])
			}
			if o.Output == nil {
				t.Fatal("expected Output to be set for MFA.SMS")
			}
		}
	}
}

func TestParseOperations_Array(t *testing.T) {
	body := []byte(`[ {"name":"OpA","method":"GET","path":"/a"}, {"name":"OpB"} ]`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
}

func TestParseOperations_Wrapper(t *testing.T) {
	body := []byte(`{"operations": [ {"name":"Op1"} ] }`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Name != "Op1" {
		t.Fatalf("unexpected ops: %+v", ops)
	}
}

func TestParseOperations_OpenAPIPaths(t *testing.T) {
	body := []byte(`{"paths": {"/users/{id}": {"get": {"description":"get user"}}}}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Method != "GET" || ops[0].Path != "/users/{id}" {
		t.Fatalf("unexpected op: %+v", ops[0])
	}
}

func TestParseOperations_Smoke(t *testing.T) {
	body := []byte(`{"operations": {"A": {"id":"A"}}}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
}
