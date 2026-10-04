// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// condDoc embeds a Condition below the document root so YAML errors carry a
// non-trivial position.
type condDoc struct {
	Name string    `json:"name" yaml:"name"`
	When Condition `json:"when" yaml:"when"`
}

func decodeCond(t *testing.T, format, input string) (condDoc, error) {
	t.Helper()
	var d condDoc
	var err error
	switch format {
	case "yaml":
		err = yaml.Unmarshal([]byte(input), &d)
	case "json":
		err = json.Unmarshal([]byte(input), &d)
	default:
		t.Fatalf("unknown format %q", format)
	}
	return d, err
}

func TestCondition_Unmarshal(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		input    string
		wantExpr *string // nil = want nil Expr
		wantErr  []string
	}{
		// YAML accepted forms.
		{name: "yaml bool true", format: "yaml", input: "name: a\nwhen: true", wantExpr: new("true")},
		{name: "yaml bool capitalized normalizes", format: "yaml", input: "when: False\nname: b", wantExpr: new("false")},
		{name: "yaml string shorthand", format: "yaml", input: `when: "_.env == 'prod'"`, wantExpr: new("_.env == 'prod'")},
		{name: "yaml flow expr object", format: "yaml", input: "name: c\nwhen: {expr: \"size(_.items) > 0\"}", wantExpr: new("size(_.items) > 0")},
		{name: "yaml block expression alias", format: "yaml", input: "when:\n  expression: _.count >= 3\n", wantExpr: new("_.count >= 3")},
		{name: "yaml anchor alias", format: "yaml", input: "name: &n \"_.ok\"\nwhen: *n", wantExpr: new("_.ok")},
		{name: "yaml null", format: "yaml", input: "name: d\nwhen: ~", wantExpr: nil},

		// YAML rejected shapes, with positions.
		{name: "yaml int", format: "yaml", input: "name: e\nwhen: 42", wantErr: []string{"line 2, column 7", "unsupported type !!int"}},
		{name: "yaml float", format: "yaml", input: "when: 1.5", wantErr: []string{"line 1, column 7", "unsupported type !!float"}},
		{name: "yaml sequence", format: "yaml", input: "name: f\nwhen:\n  - a\n  - b", wantErr: []string{"line 3, column 3", "unsupported node kind"}},
		{name: "yaml empty string", format: "yaml", input: `when: ""`, wantErr: []string{"line 1, column 7", "empty string"}},
		{name: "yaml both keys", format: "yaml", input: "name: g\nwhen: {expr: a, expression: b}", wantErr: []string{"line 2, column 7", "not both"}},
		{name: "yaml typo key", format: "yaml", input: "when:\n  exrp: _.x\n", wantErr: []string{"line 2, column 3", "unknown key \"exrp\""}},
		{name: "yaml empty expression", format: "yaml", input: "when:\n  expression: ''", wantErr: []string{"line 2, column 15", "empty string"}},
		{name: "yaml expr via anchor", format: "yaml", input: "name: &n \"_.x > 1\"\nwhen: {expr: *n}", wantExpr: new("_.x > 1")},
		{name: "yaml expr is a list", format: "yaml", input: "when: {expr: [a, b]}", wantErr: []string{"line 1, column 14", "must be a string or boolean"}},
		{name: "yaml expression is an int", format: "yaml", input: "name: m\nwhen: {expression: 5}", wantErr: []string{"line 2, column 20", "unsupported type !!int"}},
		{name: "yaml expr bool normalizes", format: "yaml", input: "when:\n  expr: TRUE\nname: n", wantExpr: new("true")},
		{name: "yaml expr null rejected", format: "yaml", input: "when: {expr: null}", wantErr: []string{"line 1, column 14", "\"expr\" must not be null"}},
		{name: "yaml null plus other key rejected", format: "yaml", input: "when: {expr: null, expression: \"true\"}", wantErr: []string{"must not be null"}},
		{name: "yaml unknown key rejected", format: "yaml", input: "when: {expr: \"false\", exrp: \"true\"}", wantErr: []string{"unknown key \"exrp\""}},
		{name: "json unknown key rejected", format: "json", input: `{"when":{"expr":"false","exrp":"true"}}`, wantErr: []string{"unknown key \"exrp\""}},
		{name: "json null plus other key rejected", format: "json", input: `{"when":{"expr":null,"expression":"true"}}`, wantErr: []string{"must not be null"}},
		{name: "yaml bad bool tag", format: "yaml", input: "when: !!bool maybe", wantErr: []string{"line 1, column 7"}},

		// JSON accepted forms.
		{name: "json bool false", format: "json", input: `{"name":"h","when":false}`, wantExpr: new("false")},
		{name: "json bool true", format: "json", input: `{"when": true}`, wantExpr: new("true")},
		{name: "json string shorthand", format: "json", input: `{"when":"_.tier in ['gold','silver']"}`, wantExpr: new("_.tier in ['gold','silver']")},
		{name: "json expr object", format: "json", input: `{"when":{"expr":"_.count > 5"},"name":"i"}`, wantExpr: new("_.count > 5")},
		{name: "json expression alias", format: "json", input: `{"when":{"expression":"has(_.owner)"}}`, wantExpr: new("has(_.owner)")},
		{name: "json null", format: "json", input: `{"name":"j","when":null}`, wantExpr: nil},

		// JSON rejected shapes.
		{name: "json number", format: "json", input: `{"when":42}`, wantErr: []string{"expected boolean, string, or object"}},
		{name: "json array", format: "json", input: `{"when":["a"]}`, wantErr: []string{"expected boolean, string, or object"}},
		{name: "json empty string", format: "json", input: `{"name":"k","when":""}`, wantErr: []string{"empty string"}},
		{name: "json both keys", format: "json", input: `{"when":{"expr":"a","expression":"b"}}`, wantErr: []string{"not both"}},
		{name: "json empty object", format: "json", input: `{"when":{}}`, wantErr: []string{"expected object with 'expr' or 'expression'"}},
		{name: "json typo key", format: "json", input: `{"when":{"exp":"_.x"}}`, wantErr: []string{"unknown key \"exp\""}},
		{name: "json empty expr", format: "json", input: `{"when":{"expr":""}}`, wantErr: []string{"empty string"}},
		{name: "json non-string expr", format: "json", input: `{"when":{"expression":7}}`, wantErr: []string{"expected boolean, string, or object"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := decodeCond(t, tt.format, tt.input)
			if len(tt.wantErr) > 0 {
				require.Error(t, err)
				for _, s := range tt.wantErr {
					assert.Contains(t, err.Error(), s)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantExpr == nil {
				assert.Nil(t, d.When.Expr)
				return
			}
			require.NotNil(t, d.When.Expr)
			assert.Equal(t, *tt.wantExpr, string(*d.When.Expr))
		})
	}
}

func TestCondition_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		input    string
		wantYAML string
		wantJSON string
	}{
		{name: "yaml true", format: "yaml", input: "when: true", wantYAML: "true\n", wantJSON: "true"},
		{name: "yaml TRUE normalized", format: "yaml", input: "when: TRUE", wantYAML: "true\n", wantJSON: "true"},
		{name: "yaml expression alias to shorthand", format: "yaml", input: "when: {expression: \"_.a && !_.b\"}", wantYAML: "_.a && !_.b\n", wantJSON: `"_.a \u0026\u0026 !_.b"`},
		{name: "yaml null", format: "yaml", input: "when: null", wantYAML: "null\n", wantJSON: "null"},
		{name: "json false", format: "json", input: `{"when":false}`, wantYAML: "false\n", wantJSON: "false"},
		{name: "json expr object to shorthand", format: "json", input: `{"when":{"expr":"_.env == 'prod'"}}`, wantYAML: "_.env == 'prod'\n", wantJSON: `"_.env == 'prod'"`},
		{name: "json string that looks like a number", format: "json", input: `{"when":"1 == 1"}`, wantYAML: "1 == 1\n", wantJSON: `"1 == 1"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := decodeCond(t, tt.format, tt.input)
			require.NoError(t, err)

			y, err := yaml.Marshal(d.When)
			require.NoError(t, err)
			assert.Equal(t, tt.wantYAML, string(y))

			j, err := json.Marshal(d.When)
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, string(j))

			// The canonical output must decode back to the same condition.
			var fromYAML, fromJSON Condition
			require.NoError(t, yaml.Unmarshal(y, &fromYAML))
			require.NoError(t, json.Unmarshal(j, &fromJSON))
			assert.Equal(t, d.When, fromYAML)
			assert.Equal(t, d.When, fromJSON)
		})
	}
}

func TestCondition_EvalBool(t *testing.T) {
	tests := []struct {
		name    string
		cond    *Condition
		decls   []VarDecl
		vars    map[string]any
		want    bool
		wantErr string
	}{
		{name: "nil condition is true", cond: nil, vars: map[string]any{}, want: true},
		{name: "nil expr is true", cond: &Condition{}, decls: []VarDecl{NewVarDecl("x", cel.IntType)}, vars: map[string]any{"x": 1}, want: true},
		{name: "false literal", cond: &Condition{Expr: exprPtr("false")}, vars: map[string]any{}, want: false},
		{
			name:  "typed vars true",
			cond:  &Condition{Expr: exprPtr("x > 3 && name.startsWith('ad')")},
			decls: []VarDecl{NewVarDecl("x", cel.IntType), NewVarDecl("name", cel.StringType)},
			vars:  map[string]any{"x": 4, "name": "admin"},
			want:  true,
		},
		{
			name:  "dyn map false",
			cond:  &Condition{Expr: exprPtr("'ops' in user.groups")},
			decls: []VarDecl{NewVarDecl("user", cel.DynType)},
			vars:  map[string]any{"user": map[string]any{"groups": []any{"dev", "qa"}}},
			want:  false,
		},
		{
			name:    "int result is an error",
			cond:    &Condition{Expr: exprPtr("x + 1")},
			decls:   []VarDecl{NewVarDecl("x", cel.IntType)},
			vars:    map[string]any{"x": 5},
			wantErr: "must evaluate to boolean, got int64",
		},
		{
			name:    "null result is an error",
			cond:    &Condition{Expr: exprPtr("d.missing")},
			decls:   []VarDecl{NewVarDecl("d", cel.DynType)},
			vars:    map[string]any{"d": map[string]any{"missing": nil}},
			wantErr: "must evaluate to boolean, got <nil>",
		},
		{
			name:    "string result is an error",
			cond:    &Condition{Expr: exprPtr("name")},
			decls:   []VarDecl{NewVarDecl("name", cel.StringType)},
			vars:    map[string]any{"name": "true"},
			wantErr: "must evaluate to boolean, got string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := tt.cond.Expression().CompileWithVarDecls(tt.decls)
			require.NoError(t, err)
			got, err := prog.EvalBool(context.Background(), tt.vars)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCondition_EvalBool_Lazy(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		a, b   bool
		want   bool
		aCalls int
		bCalls int
	}{
		{name: "or short-circuits on true", expr: "a || b", a: true, b: false, want: true, aCalls: 1, bCalls: 0},
		{name: "or reads both on false", expr: "a || b", a: false, b: true, want: true, aCalls: 1, bCalls: 1},
		{name: "and short-circuits on false", expr: "a && b", a: false, b: true, want: false, aCalls: 1, bCalls: 0},
		{name: "unreferenced var never computed", expr: "!a", a: false, b: true, want: true, aCalls: 1, bCalls: 0},
		{name: "repeated read computed once", expr: "a == b && a", a: true, b: true, want: true, aCalls: 1, bCalls: 1},
	}

	decls := []VarDecl{NewVarDecl("a", cel.BoolType), NewVarDecl("b", cel.BoolType)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := Condition{Expr: exprPtr(tt.expr)}
			prog, err := cond.Expression().CompileWithVarDecls(decls)
			require.NoError(t, err)

			var aCalls, bCalls int
			vars := map[string]any{
				"a": func() any { aCalls++; return tt.a },
				"b": func() any { bCalls++; return tt.b },
			}
			got, err := prog.EvalBool(context.Background(), vars)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.aCalls, aCalls, "a computations")
			assert.Equal(t, tt.bCalls, bCalls, "b computations")
		})
	}
}

func TestCondition_CostLimit(t *testing.T) {
	const expensive = "[1, 2, 3, 4, 5, 6, 7, 8].all(i, [1, 2, 3, 4, 5, 6, 7, 8].exists(j, i * j > n))"

	tests := []struct {
		name    string
		ctx     context.Context
		opts    []Option
		oneShot bool
		wantErr string
	}{
		{name: "compiled with tiny limit", ctx: context.Background(), opts: []Option{WithCostLimit(5)}, wantErr: "cost limit exceeded"},
		{name: "compiled without limit", ctx: context.Background(), opts: []Option{WithNoCostLimit()}},
		{name: "one-shot with context limit", ctx: ContextWithCostLimit(context.Background(), 3), oneShot: true, wantErr: "cost limit exceeded"},
		{name: "one-shot with generous context limit", ctx: ContextWithCostLimit(context.Background(), 1_000_000), oneShot: true},
	}

	cond := Condition{Expr: exprPtr(expensive)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bool
			var err error
			if tt.oneShot {
				got, err = cond.EvaluateWithAdditionalVars(tt.ctx, nil, map[string]any{"n": 0})
			} else {
				prog, cerr := cond.Expression().CompileWithVarDecls([]VarDecl{NewVarDecl("n", cel.IntType)}, tt.opts...)
				require.NoError(t, cerr)
				got, err = prog.EvalBool(tt.ctx, map[string]any{"n": 0})
			}
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

func TestCondition_Evaluate(t *testing.T) {
	tests := []struct {
		name    string
		cond    *Condition
		root    map[string]any
		extra   map[string]any
		self    any
		useSelf bool
		want    bool
		wantErr string
	}{
		{name: "nil condition", cond: nil, root: map[string]any{"env": "dev"}, want: true},
		{name: "root equality", cond: &Condition{Expr: exprPtr("_.env == 'prod'")}, root: map[string]any{"env": "prod"}, want: true},
		{name: "root comparison false", cond: &Condition{Expr: exprPtr("_.count > 5")}, root: map[string]any{"count": 2}, want: false},
		{name: "additional var", cond: &Condition{Expr: exprPtr("greeting == 'hi' && _.ok")}, root: map[string]any{"ok": true}, extra: map[string]any{"greeting": "hi"}, want: true},
		{name: "self int", cond: &Condition{Expr: exprPtr("__self > 5 && _.on")}, root: map[string]any{"on": true}, self: 10, useSelf: true, want: true},
		{name: "self null", cond: &Condition{Expr: exprPtr("__self == null")}, self: nil, useSelf: true, want: true},
		{name: "non-bool result", cond: &Condition{Expr: exprPtr("_.count + 1")}, root: map[string]any{"count": 5}, wantErr: "boolean"},
		{name: "undeclared var", cond: &Condition{Expr: exprPtr("nope")}, root: map[string]any{"a": 1}, wantErr: "condition evaluation failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			var got bool
			var err error
			switch {
			case tt.useSelf:
				got, err = tt.cond.EvaluateWithSelf(ctx, tt.root, tt.self)
			case tt.extra != nil:
				got, err = tt.cond.EvaluateWithAdditionalVars(ctx, tt.root, tt.extra)
			default:
				got, err = tt.cond.Evaluate(ctx, tt.root)
			}
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
