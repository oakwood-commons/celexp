// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// ErrNoCondition is returned when evaluating or compiling a nil Condition or
// one with a nil Expr (e.g. decoded from null). Conditions fail closed:
// callers that want "absent = allow/run" must check for nil themselves or
// test errors.Is(err, ErrNoCondition).
var ErrNoCondition = errors.New("no condition: nil Condition or Expr")

// Condition is a boolean CEL expression written in YAML or JSON.
//
// Accepted forms (YAML shown; JSON accepts the same shapes):
//   - Boolean literal:  when: true / when: false
//   - String shorthand: when: "_.environment == 'prod'"
//   - Explicit object:  when: { expr: "_.environment == 'prod'" }
//   - Expression alias: when: { expression: "_.environment == 'prod'" }
//
// null decodes to a zero Condition (nil Expr). Evaluating or compiling it
// fails closed with ErrNoCondition rather than returning true.
type Condition struct {
	Expr *Expression `json:"expr" yaml:"expr" doc:"CEL expression that must evaluate to boolean" example:"_.environment == 'prod'"`
}

// Expression returns the condition's CEL expression, or ErrNoCondition for
// a nil Condition or nil Expr. Use it to compile the condition once with the
// caller's own environment and options, then evaluate with EvalBool:
//
//	expr, err := cond.Expression()
//	prog, err := expr.CompileWithVarDecls(decls, celexp.WithCostLimit(10000))
//	ok, err := prog.EvalBool(ctx, vars)
func (c *Condition) Expression() (Expression, error) {
	if c == nil || c.Expr == nil {
		return "", ErrNoCondition
	}
	return *c.Expr, nil
}

// UnmarshalYAML implements yaml.Unmarshaler. It accepts a boolean literal,
// a non-empty string, or a mapping with exactly one of "expr" or
// "expression". Errors carry the node's line and column.
func (c *Condition) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return c.unmarshalScalarYAML(node)
	case yaml.MappingNode:
		return c.unmarshalMappingYAML(node)
	case yaml.AliasNode:
		// yaml.v3 resolves aliases before calling us; kept for direct callers.
		return c.UnmarshalYAML(node.Alias)
	case yaml.DocumentNode, yaml.SequenceNode:
		fallthrough
	default:
		return fmt.Errorf("invalid condition at line %d, column %d: expected boolean, string, or object {expr: \"...\"}, got unsupported node kind", node.Line, node.Column)
	}
}

func (c *Condition) unmarshalScalarYAML(node *yaml.Node) error {
	switch node.ShortTag() {
	case "!!bool":
		// Normalize True/TRUE to the CEL literal spelling.
		var b bool
		if err := node.Decode(&b); err != nil {
			return fmt.Errorf("invalid condition at line %d, column %d: %w", node.Line, node.Column, err)
		}
		c.Expr = exprPtr(strconv.FormatBool(b))
		return nil
	case "!!str":
		if node.Value == "" {
			return fmt.Errorf("invalid condition at line %d, column %d: empty string is not a valid CEL expression", node.Line, node.Column)
		}
		c.Expr = exprPtr(node.Value)
		return nil
	case "!!null":
		c.Expr = nil
		return nil
	default:
		return fmt.Errorf("invalid condition at line %d, column %d: unsupported type %s; use true, false, a CEL expression string, or {expr: \"...\"}", node.Line, node.Column, node.ShortTag())
	}
}

func (c *Condition) unmarshalMappingYAML(node *yaml.Node) error {
	var vals [2]*Expression // expr, expression
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		idx, ok := conditionKeys[k.Value]
		if !ok {
			return fmt.Errorf("invalid condition at line %d, column %d: unknown key %q; use 'expr' or 'expression'", k.Line, k.Column, k.Value)
		}
		e, err := yamlExprValue(v)
		if err != nil {
			return err
		}
		if e == nil {
			return fmt.Errorf("invalid condition at line %d, column %d: %q must not be null", v.Line, v.Column, k.Value)
		}
		vals[idx] = e
	}
	expr, err := pickExpr(vals[0], vals[1])
	if err != nil {
		return fmt.Errorf("invalid condition at line %d, column %d: %w", node.Line, node.Column, err)
	}
	c.Expr = expr
	return nil
}

// conditionKeys maps the accepted object keys to their slot.
var conditionKeys = map[string]int{"expr": 0, "expression": 1}

// yamlExprValue decodes the value of an expr/expression key with the same
// scalar rules as the top-level form (so a non-string is rejected at its own
// position), returning nil for an absent or null value.
func yamlExprValue(n *yaml.Node) (*Expression, error) {
	if n.Kind == 0 { // key absent
		return nil, nil
	}
	if n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("invalid condition at line %d, column %d: expression must be a string or boolean", n.Line, n.Column)
	}
	var c Condition
	if err := c.unmarshalScalarYAML(n); err != nil {
		return nil, err
	}
	return c.Expr, nil
}

// UnmarshalJSON implements json.Unmarshaler. It accepts the same shapes as
// UnmarshalYAML.
func (c *Condition) UnmarshalJSON(data []byte) error {
	*c = Condition{}

	if string(bytes.TrimSpace(data)) == "null" {
		return nil
	}

	var b bool
	if json.Unmarshal(data, &b) == nil {
		c.Expr = exprPtr(strconv.FormatBool(b))
		return nil
	}

	var s string
	if json.Unmarshal(data, &s) == nil {
		if s == "" {
			return fmt.Errorf("invalid condition: empty string is not a valid CEL expression")
		}
		c.Expr = exprPtr(s)
		return nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("invalid condition: expected boolean, string, or object {\"expr\": \"...\"}: %w", err)
	}
	var vals [2]*Expression
	for k, raw := range obj {
		idx, ok := conditionKeys[k]
		if !ok {
			return fmt.Errorf("invalid condition: unknown key %q; use 'expr' or 'expression'", k)
		}
		var v any
		_ = json.Unmarshal(raw, &v) // raw is valid JSON: the outer decode succeeded
		switch v := v.(type) {
		case nil:
			return fmt.Errorf("invalid condition: %q must not be null", k)
		case bool:
			vals[idx] = exprPtr(strconv.FormatBool(v))
		case string:
			vals[idx] = exprPtr(v)
		default:
			return fmt.Errorf("invalid condition: %q must be a string or boolean", k)
		}
	}
	expr, err := pickExpr(vals[0], vals[1])
	if err != nil {
		return fmt.Errorf("invalid condition: %w", err)
	}
	c.Expr = expr
	return nil
}

// pickExpr validates the object form: exactly one of expr/expression, non-empty.
func pickExpr(expr, expression *Expression) (*Expression, error) {
	switch {
	case expr != nil && expression != nil:
		return nil, fmt.Errorf("specify either 'expr' or 'expression', not both")
	case expr == nil && expression == nil:
		return nil, fmt.Errorf("expected object with 'expr' or 'expression'")
	case expression != nil:
		expr = expression
	}
	if *expr == "" {
		return nil, fmt.Errorf("empty string is not a valid CEL expression")
	}
	return expr, nil
}

// MarshalYAML implements yaml.Marshaler. "true"/"false" marshal as booleans,
// anything else as the string shorthand, nil as null.
func (c Condition) MarshalYAML() (any, error) {
	if c.Expr == nil {
		return nil, nil
	}
	switch s := string(*c.Expr); s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return s, nil
	}
}

// MarshalJSON implements json.Marshaler with the same canonical form as
// MarshalYAML.
func (c Condition) MarshalJSON() ([]byte, error) {
	v, err := c.MarshalYAML()
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// Evaluate compiles and evaluates the condition in one shot, with rootData
// bound to "_" (declared dyn; see BuildCELContext). rootData may be a
// LazyMap. A nil Condition or nil Expr returns ErrNoCondition. A non-boolean
// result is an error.
func (c *Condition) Evaluate(ctx context.Context, rootData any) (bool, error) {
	return c.EvaluateWithAdditionalVars(ctx, rootData, nil)
}

// EvaluateWithAdditionalVars is Evaluate with extra top-level variables.
// Values may be lazy as described on EvalBool; neither rootData nor
// additionalVars is modified.
func (c *Condition) EvaluateWithAdditionalVars(ctx context.Context, rootData any, additionalVars map[string]any) (bool, error) {
	expr, err := c.Expression()
	if err != nil {
		return false, err
	}
	envOpts, vars := BuildCELContext(rootData, additionalVars)
	prog, err := expr.Compile(envOpts, WithContext(ctx))
	if err != nil {
		return false, fmt.Errorf("condition evaluation failed: %w\nAvailable variables: %s",
			err, describeAvailableVars(rootData, additionalVars))
	}
	ok, err := prog.EvalBool(ctx, vars)
	if err != nil {
		return false, fmt.Errorf("condition evaluation failed: %w", err)
	}
	return ok, nil
}

// EvaluateWithSelf is Evaluate with VarSelf (__self) bound to self.
func (c *Condition) EvaluateWithSelf(ctx context.Context, rootData, self any) (bool, error) {
	return c.EvaluateWithAdditionalVars(ctx, rootData, map[string]any{VarSelf: self})
}

// EvalBool evaluates a compiled program and requires a boolean result; any
// other result (including null) is an error, never a silent false.
//
// vars may hold lazily computed values as func() any, func() ref.Val, or
// func() (any, error): the function runs only when the expression reads that
// variable (so a short-circuited branch never computes it), at most once per
// evaluation. A LazyMap value (top-level or nested in map[string]any values)
// defers each of its entries the same way. vars itself is never modified. If
// a func() (any, error) returns an error, evaluation fails with an error
// wrapping it, even when CEL's commutative ||/&& would otherwise have
// absorbed it.
func (r *CompileResult) EvalBool(ctx context.Context, vars map[string]any) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("compile result or program is nil")
	}
	act, ev := bindVars(vars, r.adapter)
	result, err := r.EvalWithContext(ctx, act)
	if ferr := ev.factErr(); ferr != nil {
		return false, fmt.Errorf("failed to evaluate expression %q: %w", r.Expression, ferr)
	}
	if err != nil {
		return false, err
	}
	return asBool(result)
}

func asBool(v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("condition must evaluate to boolean, got %T", v)
	}
	return b, nil
}

func exprPtr(s string) *Expression {
	e := Expression(s)
	return &e
}
