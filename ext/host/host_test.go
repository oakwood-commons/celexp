// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"os"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDirFunc_Metadata(t *testing.T) {
	fn := ConfigDirFunc()
	assert.Equal(t, "host.configDir", fn.Name)
	assert.True(t, fn.Custom)
	assert.NotEmpty(t, fn.EnvOptions)
	assert.NotEmpty(t, fn.Examples)
	// Registered under both the namespaced and portable (template-matching) name.
	assert.Contains(t, fn.FunctionNames, "host.configDir")
	assert.Contains(t, fn.FunctionNames, "hostConfigDir")
}

func TestConfigDirFunc_DefaultProvider(t *testing.T) {
	// With no override, the library default resolves via os.UserConfigDir().
	SetConfigDirProvider(nil)
	t.Cleanup(func() { SetConfigDirProvider(nil) })

	want := defaultConfigDir()
	fn := ConfigDirFunc()
	env, err := cel.NewEnv(fn.EnvOptions...)
	require.NoError(t, err)

	for _, expr := range []string{`host.configDir()`, `hostConfigDir()`} {
		assert.Equal(t, want, evalString(t, env, expr), expr)
	}

	// Sanity: the default matches os.UserConfigDir() (empty only on error).
	if osDir, err := os.UserConfigDir(); err == nil {
		assert.Equal(t, osDir, want)
	}
}

func TestConfigDirFunc_InjectedProvider(t *testing.T) {
	// An embedder can override the resolver (e.g. to honor a branded app name).
	const branded = "/xdg/config/mycli"
	SetConfigDirProvider(func() string { return branded })
	t.Cleanup(func() { SetConfigDirProvider(nil) })

	fn := ConfigDirFunc()
	env, err := cel.NewEnv(fn.EnvOptions...)
	require.NoError(t, err)

	// Both the namespaced and the portable alias must reflect the injection.
	for _, expr := range []string{`host.configDir()`, `hostConfigDir()`} {
		assert.Equal(t, branded, evalString(t, env, expr), expr)
	}
}

func TestSetConfigDirProvider_NilResets(t *testing.T) {
	SetConfigDirProvider(func() string { return "/custom" })
	SetConfigDirProvider(nil)
	assert.Equal(t, defaultConfigDir(), resolveConfigDir())
}

func evalString(t *testing.T, env *cel.Env, expr string) string {
	t.Helper()
	ast, iss := env.Compile(expr)
	require.NoError(t, iss.Err(), expr)
	prg, err := env.Program(ast)
	require.NoError(t, err, expr)
	out, _, err := prg.Eval(map[string]any{})
	require.NoError(t, err, expr)
	s, ok := out.Value().(string)
	require.True(t, ok, "expected string result for %q", expr)
	return s
}
