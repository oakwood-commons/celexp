// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"context"
	"errors"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compileCond(t *testing.T, c *Condition, decls []VarDecl, opts ...Option) *CompileResult {
	t.Helper()
	expr, err := c.Expression()
	require.NoError(t, err)
	prog, err := expr.CompileWithVarDecls(decls, opts...)
	require.NoError(t, err)
	return prog
}

// factError is a custom error type for errors.As checks.
type factError struct{ fact string }

func (e *factError) Error() string { return "fact " + e.fact + " unavailable" }

func TestCondition_NoCondition(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		cond *Condition
		call func(c *Condition) error
	}{
		{name: "nil Expression", cond: nil, call: func(c *Condition) error { _, err := c.Expression(); return err }},
		{name: "nil-expr Expression", cond: &Condition{}, call: func(c *Condition) error { _, err := c.Expression(); return err }},
		{name: "nil Evaluate", cond: nil, call: func(c *Condition) error {
			_, err := c.Evaluate(ctx, map[string]any{"env": "dev"})
			return err
		}},
		{name: "nil-expr EvaluateWithAdditionalVars", cond: &Condition{}, call: func(c *Condition) error {
			_, err := c.EvaluateWithAdditionalVars(ctx, nil, map[string]any{"x": 1})
			return err
		}},
		{name: "nil-expr EvaluateWithSelf", cond: &Condition{}, call: func(c *Condition) error {
			_, err := c.EvaluateWithSelf(ctx, map[string]any{"a": true}, "self")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.call(tt.cond), ErrNoCondition)
		})
	}

	// The one-shot helpers must report false alongside the error.
	got, err := (*Condition)(nil).Evaluate(ctx, nil)
	require.ErrorIs(t, err, ErrNoCondition)
	assert.False(t, got)
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
		{name: "false literal", cond: &Condition{Expr: exprPtr("false")}, vars: map[string]any{}, want: false},
		{name: "true literal nil vars", cond: &Condition{Expr: exprPtr("true")}, vars: nil, want: true},
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
			got, err := compileCond(t, tt.cond, tt.decls).EvalBool(context.Background(), tt.vars)
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
			prog := compileCond(t, &Condition{Expr: exprPtr(tt.expr)}, decls)

			var aCalls, bCalls int
			vars := map[string]any{
				"a": func() any { aCalls++; return tt.a },
				"b": func() (any, error) { bCalls++; return tt.b, nil },
			}
			got, err := prog.EvalBool(context.Background(), vars)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.aCalls, aCalls, "a computations")
			assert.Equal(t, tt.bCalls, bCalls, "b computations")
		})
	}
}

// TestCondition_VarsNotMutated reuses one vars map across evaluations: each
// evaluation must recompute the lazy value, and the caller's map must still
// hold the original funcs afterwards.
func TestCondition_VarsNotMutated(t *testing.T) {
	cond := &Condition{Expr: exprPtr("gate && level > 1")}
	prog := compileCond(t, cond, []VarDecl{NewVarDecl("gate", cel.BoolType), NewVarDecl("level", cel.IntType)})

	tests := []struct {
		name string
		eval func(ctx context.Context, vars map[string]any) (bool, error)
	}{
		{name: "EvalBool", eval: prog.EvalBool},
		{name: "EvaluateWithAdditionalVars", eval: func(ctx context.Context, vars map[string]any) (bool, error) {
			return cond.EvaluateWithAdditionalVars(ctx, map[string]any{"unused": 1}, vars)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			vars := map[string]any{
				"gate":  func() any { calls++; return calls > 1 }, // false, then true
				"level": func() (any, error) { return int64(2 + calls), nil },
			}

			first, err := tt.eval(context.Background(), vars)
			require.NoError(t, err)
			assert.False(t, first)

			second, err := tt.eval(context.Background(), vars)
			require.NoError(t, err)
			assert.True(t, second, "second evaluation must recompute the lazy value")
			assert.Equal(t, 2, calls)

			_, gateIsFunc := vars["gate"].(func() any)
			_, levelIsFunc := vars["level"].(func() (any, error))
			assert.True(t, gateIsFunc, "caller's map must still hold the func() any")
			assert.True(t, levelIsFunc, "caller's map must still hold the func() (any, error)")
			assert.Len(t, vars, 2)
		})
	}
}

func TestCondition_LazyFactErrors(t *testing.T) {
	errBackend := errors.New("backend down")

	tests := []struct {
		name      string
		expr      string
		fact      error // error returned by the "f" fact
		other     bool  // value of the "o" fact
		want      bool
		wantIs    error
		wantAs    bool
		wantCalls int
	}{
		{name: "read fact error wraps sentinel", expr: "f && o", fact: errBackend, other: true, wantIs: errBackend, wantCalls: 1},
		{name: "read fact error wraps custom type", expr: "o && f", fact: &factError{fact: "f"}, other: true, wantAs: true, wantCalls: 1},
		{name: "error not absorbed by commutative or", expr: "f || o", fact: errBackend, other: true, wantIs: errBackend, wantCalls: 1},
		{name: "short-circuited or never runs fact", expr: "o || f", fact: errBackend, other: true, want: true, wantCalls: 0},
		{name: "short-circuited and never runs fact", expr: "o && f", fact: &factError{fact: "f"}, other: false, want: false, wantCalls: 0},
	}

	decls := []VarDecl{NewVarDecl("f", cel.BoolType), NewVarDecl("o", cel.BoolType)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &Condition{Expr: exprPtr(tt.expr)}
			prog := compileCond(t, cond, decls)

			for _, eval := range []struct {
				name string
				fn   func(map[string]any) (bool, error)
			}{
				{"EvalBool", func(v map[string]any) (bool, error) { return prog.EvalBool(context.Background(), v) }},
				{"EvaluateWithAdditionalVars", func(v map[string]any) (bool, error) {
					return cond.EvaluateWithAdditionalVars(context.Background(), nil, v)
				}},
			} {
				t.Run(eval.name, func(t *testing.T) {
					calls := 0
					vars := map[string]any{
						"f": func() (any, error) { calls++; return nil, tt.fact },
						"o": tt.other,
					}
					got, err := eval.fn(vars)
					assert.Equal(t, tt.wantCalls, calls, "erroring fact computations")
					switch {
					case tt.wantIs != nil:
						require.ErrorIs(t, err, tt.wantIs)
						assert.Contains(t, err.Error(), `variable "f"`)
						assert.False(t, got)
					case tt.wantAs:
						var fe *factError
						require.ErrorAs(t, err, &fe)
						assert.Equal(t, "f", fe.fact)
						assert.False(t, got)
					default:
						require.NoError(t, err)
						assert.Equal(t, tt.want, got)
					}
				})
			}
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

	cond := &Condition{Expr: exprPtr(expensive)}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bool
			var err error
			if tt.oneShot {
				got, err = cond.EvaluateWithAdditionalVars(tt.ctx, nil, map[string]any{"n": 0})
			} else {
				prog := compileCond(t, cond, []VarDecl{NewVarDecl("n", cel.IntType)}, tt.opts...)
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
		root    any
		extra   map[string]any
		self    any
		useSelf bool
		want    bool
		wantErr string
	}{
		{name: "nil root is null", cond: &Condition{Expr: exprPtr("_ == null")}, want: true},
		{name: "extra var cannot replace root", cond: &Condition{Expr: exprPtr("_.role == 'admin'")}, root: map[string]any{"role": "user"}, extra: map[string]any{"_": map[string]any{"role": "admin"}}, wantErr: `must not set "_"`},
		{name: "extra var cannot set root when nil", cond: &Condition{Expr: exprPtr("_.role == 'admin'")}, extra: map[string]any{"_": map[string]any{"role": "admin"}}, wantErr: `must not set "_"`},
		{name: "nil root with extra", cond: &Condition{Expr: exprPtr("_ == null && x == 1")}, extra: map[string]any{"x": 1}, want: true},
		{name: "nil root with self", cond: &Condition{Expr: exprPtr("_ == null && __self == 2")}, self: 2, useSelf: true, want: true},
		{name: "root equality", cond: &Condition{Expr: exprPtr("_.env == 'prod'")}, root: map[string]any{"env": "prod"}, want: true},
		{name: "root comparison false", cond: &Condition{Expr: exprPtr("_.count > 5")}, root: map[string]any{"count": 2}, want: false},
		{name: "additional var", cond: &Condition{Expr: exprPtr("greeting == 'hi' && _.ok")}, root: map[string]any{"ok": true}, extra: map[string]any{"greeting": "hi"}, want: true},
		{name: "self int", cond: &Condition{Expr: exprPtr("__self > 5 && _.on")}, root: map[string]any{"on": true}, self: 10, useSelf: true, want: true},
		{name: "self null", cond: &Condition{Expr: exprPtr("__self == null")}, self: nil, useSelf: true, want: true},
		{name: "non-bool result", cond: &Condition{Expr: exprPtr("_.count + 1")}, root: map[string]any{"count": 5}, wantErr: "boolean"},
		{name: "undeclared var", cond: &Condition{Expr: exprPtr("nope")}, root: map[string]any{"a": 1}, wantErr: "condition evaluation failed"},
		{name: "undeclared var, nil root lists _", cond: &Condition{Expr: exprPtr("nope")}, wantErr: "Available variables: _"},
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
