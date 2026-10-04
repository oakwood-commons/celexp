// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp_test

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/oakwood-commons/celexp"
	"gopkg.in/yaml.v3"
)

// ExampleCondition decodes a condition from YAML, compiles it once, and
// evaluates it against a lazily computed variable.
func ExampleCondition() {
	var rule struct {
		When celexp.Condition `yaml:"when"`
	}
	if err := yaml.Unmarshal([]byte(`when: {expr: "admin || 'ops' in user.groups"}`), &rule); err != nil {
		panic(err)
	}

	expr, err := rule.When.Expression() // ErrNoCondition if the rule had no condition
	if err != nil {
		panic(err)
	}
	prog, err := expr.CompileWithVarDecls([]celexp.VarDecl{
		celexp.NewVarDecl("admin", cel.BoolType),
		celexp.NewVarDecl("user", cel.DynType),
	}, celexp.WithCostLimit(10_000))
	if err != nil {
		panic(err)
	}

	lookups := 0
	ok, err := prog.EvalBool(context.Background(), map[string]any{
		"admin": true,
		// Lazy facts may also return an error: func() (any, error).
		"user": func() (any, error) { lookups++; return map[string]any{"groups": []any{"dev"}}, nil },
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(ok, lookups)
	// Output: true 0
}
