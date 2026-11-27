// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package bootstrap_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/bootstrap"
	"github.com/oakwood-commons/celexp/env"
)

// TestDefault verifies that after bootstrap.Default() the core resolves the full
// extension set (a custom function like strings.slugify compiles and evaluates).
// Without the registration, such an expression fails to compile against the bare
// fallback environment.
func TestDefault(t *testing.T) {
	bootstrap.Default()

	out, err := celexp.EvaluateExpression(context.Background(),
		`strings.slugify("Hello, World!")`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "hello-world", out)

	sumOut, err := celexp.EvaluateExpression(context.Background(),
		`math.sum(json.unmarshalJSONC('[1, 2, /* comment */ 3,]').map(x, int(x)))`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(6), sumOut)

	fmOut, err := celexp.EvaluateExpression(context.Background(),
		`frontmatter.parse(frontmatter.format({"title": "Guide"}, "# Hi")).meta.title`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "Guide", fmOut)

	portsDoc := "---\nports:\n  80: http\n  443: https\n  \"8080\": alt\nflags:\n  true: enabled\n---\n# Service\n"
	evalParsed, err := celexp.EvaluateExpression(context.Background(),
		`frontmatter.parse(doc)`, nil, map[string]any{"doc": portsDoc})
	require.NoError(t, err)
	evalMap, ok := evalParsed.(map[string]any)
	require.True(t, ok)
	evalMeta, ok := evalMap["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[any]any{
		int64(80):  "http",
		int64(443): "https",
		"8080":     "alt",
	}, evalMeta["ports"])
	assert.Equal(t, map[any]any{true: "enabled"}, evalMeta["flags"])

	envOpts, celVars := celexp.BuildCELContext(nil, map[string]any{"doc": portsDoc})
	compiled, err := celexp.Expression(`frontmatter.parse(doc)`).Compile(envOpts)
	require.NoError(t, err)
	typedMap, err := celexp.EvalAs[map[string]any](compiled, celVars)
	require.NoError(t, err)
	typedMeta, ok := typedMap["meta"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[any]any{
		int64(80):  "http",
		int64(443): "https",
		"8080":     "alt",
	}, typedMeta["ports"])
	assert.Equal(t, map[any]any{true: "enabled"}, typedMeta["flags"])

	cyclicAny := map[any]any{}
	cyclicAny[int64(1)] = cyclicAny
	_, err = celexp.EvaluateExpression(context.Background(), `m`, nil, map[string]any{"m": cyclicAny})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cyclic value")

	dupKeys := map[string]any{"ports": map[any]any{int(1): "a", int64(1): "b"}}
	dupOpts, dupVars := celexp.BuildCELContext(nil, map[string]any{"m": dupKeys})
	dupCompiled, err := celexp.Expression(`m`).Compile(dupOpts)
	require.NoError(t, err)
	_, err = celexp.EvalAs[map[string]any](dupCompiled, dupVars)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate map key")
}

// TestDefault_Idempotent confirms calling Default() more than once is safe
// (the underlying setters are one-shot; extra calls are no-ops).
func TestDefault_Idempotent(t *testing.T) {
	require.NotPanics(t, func() {
		bootstrap.Default()
		bootstrap.Default()
	})
}

// TestNewRestrictedUnaffectedByBootstrapDefault confirms that
// env.NewRestricted stays restricted even after bootstrap.Default() has
// registered the process-global full-extension factory elsewhere. This is
// the core guarantee that makes NewRestricted usable in the same process as
// the default environment: it talks to cel.NewEnv directly and never
// consults the global factory that Default() installs.
func TestNewRestrictedUnaffectedByBootstrapDefault(t *testing.T) {
	bootstrap.Default()

	e, err := env.NewRestricted(context.Background(), env.SafePredicate)
	require.NoError(t, err)
	require.NotNil(t, e)

	_, iss := e.Compile(`host.configDir("myapp")`)
	assert.Error(t, iss.Err(), "restricted env must not gain host.* just because bootstrap.Default() ran")

	_, iss = e.Compile(`"a,b".split(",").size() == 2`)
	assert.NoError(t, iss.Err())
}
