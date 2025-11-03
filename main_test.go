package main

import "testing"

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    }
    hasA, hasB := false, false
    for _, o := range ops {
        switch o.Name {
        case "Sync.AWS":
            hasA = true
        case "MFA.SMS":
            hasB = true
            if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                t.Fatalf("expected input ref captured, got %+v", o.Params)
            }
        }
    }
    if !hasA || !hasB {
        t.Fatalf("missing expected ops: %+v", ops)
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
package main

import "testing"

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    }
    hasA, hasB := false, false
    for _, o := range ops {
        switch o.Name {
        case "Sync.AWS":
            hasA = true
        case "MFA.SMS":
            hasB = true
            if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                t.Fatalf("expected input ref captured, got %+v", o.Params)
            }
        }
    }
    if !hasA || !hasB {
        t.Fatalf("missing expected ops: %+v", ops)
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
package main

import (
    "testing"
)

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    package main

    import (
        "testing"
    )

    func TestParseOperations_NormalMap(t *testing.T) {
        body := []byte(`{
            "application": {"name":"arize-automation","version":"1.0.0"},
            "operations": {
                "Sync.AWS": {"id":"Sync.AWS"},
                "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
            }
        }`)
        ops, err := parseOperations(body)
        if err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if len(ops) != 2 {
            t.Fatalf("expected 2 ops, got %d", len(ops))
        }
        found := map[string]bool{}
        for _, o := range ops {
            found[o.Name] = true
            if o.Name == "MFA.SMS" {
                if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                    t.Fatalf("expected input ref captured, got %+v", o.Params)
                }
            }
        }
        if !found["Sync.AWS"] || !found["MFA.SMS"] {
            t.Fatalf("missing expected ops: %+v", ops)
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

    import (
        "testing"
    )

    func TestParseOperations_NormalMap(t *testing.T) {
        body := []byte(`{
            "application": {"name":"arize-automation","version":"1.0.0"},
            "operations": {
                "Sync.AWS": {"id":"Sync.AWS"},
                "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
            }
        }`)
        ops, err := parseOperations(body)
        if err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if len(ops) != 2 {
            t.Fatalf("expected 2 ops, got %d", len(ops))
        }
        found := map[string]bool{}
        for _, o := range ops {
            found[o.Name] = true
            if o.Name == "MFA.SMS" {
                if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                    t.Fatalf("expected input ref captured, got %+v", o.Params)
                }
            }
        }
        if !found["Sync.AWS"] || !found["MFA.SMS"] {
            t.Fatalf("missing expected ops: %+v", ops)
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
        found[o.Name] = true
        if o.Name == "MFA.SMS" {
            if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                t.Fatalf("expected input ref captured, got %+v", o.Params)
            }
        }
    }
    if !found["Sync.AWS"] || !found["MFA.SMS"] {
        t.Fatalf("missing expected ops: %+v", ops)
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
package main

import (
    "testing"
)

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    }
    found := map[string]bool{}
    for _, o := range ops {
        found[o.Name] = true
        if o.Name == "MFA.SMS" {
            if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                t.Fatalf("expected input ref captured, got %+v", o.Params)
            }
        }
    }
    if !found["Sync.AWS"] || !found["MFA.SMS"] {
        t.Fatalf("missing expected ops: %+v", ops)
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
package main

import (
    "testing"
)

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    }
    found := map[string]bool{}
    for _, o := range ops {
        found[o.Name] = true
        if o.Name == "MFA.SMS" {
            if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                t.Fatalf("expected input ref captured, got %+v", o.Params)
            }
        }
    }
    if !found["Sync.AWS"] || !found["MFA.SMS"] {
        t.Fatalf("missing expected ops: %+v", ops)
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
package main

import (
    "testing"
)

func TestParseOperations_NormalMap(t *testing.T) {
    body := []byte(`{
        "application": {"name":"arize-automation","version":"1.0.0"},
        "operations": {
            "Sync.AWS": {"id":"Sync.AWS"},
            "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
        }
    }`)
    ops, err := parseOperations(body)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(ops) != 2 {
        t.Fatalf("expected 2 ops, got %d", len(ops))
    }
    package main

    import (
        "testing"
    )

    func TestParseOperations_NormalMap(t *testing.T) {
        body := []byte(`{
            "application": {"name":"arize-automation","version":"1.0.0"},
            "operations": {
                "Sync.AWS": {"id":"Sync.AWS"},
                "MFA.SMS": {"id":"MFA.SMS","input":"TwilioService.mfa.input"}
            }
        }`)
        ops, err := parseOperations(body)
        if err != nil {
            t.Fatalf("unexpected error: %v", err)
        }
        if len(ops) != 2 {
            t.Fatalf("expected 2 ops, got %d", len(ops))
        }
        found := map[string]bool{}
        for _, o := range ops {
            found[o.Name] = true
            if o.Name == "MFA.SMS" {
                if len(o.Params) != 1 || o.Params[0]["$ref"] != "TwilioService.mfa.input" {
                    t.Fatalf("expected input ref captured, got %+v", o.Params)
                }
            }
        }
        if !found["Sync.AWS"] || !found["MFA.SMS"] {
            t.Fatalf("missing expected ops: %+v", ops)
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
