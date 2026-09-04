// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"context"
	"errors"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/oakwood-commons/celexp/ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRestrictedSecurityAssertion is the important test in this file: a
// SafePredicate-scoped environment must compile ordinary predicate
// expressions but must FAIL TO COMPILE anything that performs I/O, touches
// the host/filesystem, can block, or is non-deterministic -- even though the
// caller never explicitly denied those namespaces. This is what makes
// "restricted" mean something rather than just being a smaller default.
func TestNewRestrictedSecurityAssertion(t *testing.T) {
	ctx := context.Background()
	e, err := NewRestricted(ctx, SafePredicate)
	require.NoError(t, err)
	require.NotNil(t, e)

	allowed := []string{
		`size("hello") > 2`,
		`"a,b,c".split(",").size() == 3`,
		`math.abs(-5) == 5`,
		`sets.contains([1, 2, 3], [2, 3])`,
		`"abc".matches("^[a-z]+$")`,
		`regex.match("abc", "^[a-z]+$")`,
	}
	for _, expr := range allowed {
		_, iss := e.Compile(expr)
		assert.NoErrorf(t, iss.Err(), "expected %q to compile under SafePredicate", expr)
	}

	denied := []string{
		`host.configDir("myapp")`,
		`debug.sleep(1)`,
		`debug.throw("boom")`,
		`filepath.exists("/etc/passwd")`,
		`filepath.dir("/a/b")`,
		`time.now()`,
		`guid.new()`,
		`json.marshal({"a": 1})`,
		`url.encode("a b")`,
	}
	for _, expr := range denied {
		_, iss := e.Compile(expr)
		assert.Errorf(t, iss.Err(), "expected %q to FAIL to compile under SafePredicate", expr)
	}
}

func TestNewRestrictedUnknownNamespace(t *testing.T) {
	ctx := context.Background()
	e, err := NewRestricted(ctx, []string{"not-a-real-namespace"})
	require.Error(t, err)
	assert.Nil(t, e)
	assert.True(t, errors.Is(err, ext.ErrUnknownNamespace))
}

func TestNewRestrictedHonoursCallerDeclarations(t *testing.T) {
	ctx := context.Background()
	e, err := NewRestricted(ctx, SafePredicate, cel.Variable("x", cel.IntType))
	require.NoError(t, err)
	require.NotNil(t, e)

	_, iss := e.Compile(`x > 2`)
	assert.NoError(t, iss.Err())
}

func TestNewRestrictedRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	e, err := NewRestricted(ctx, SafePredicate)
	assert.Error(t, err)
	assert.Nil(t, e)
}

func TestNewRestrictedEmptyAllowlistYieldsBareEnv(t *testing.T) {
	ctx := context.Background()
	e, err := NewRestricted(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, e)

	// A bare CEL environment still supports built-in operators, but none of
	// celexp's extension namespaces.
	_, iss := e.Compile(`1 + 1 == 2`)
	assert.NoError(t, iss.Err())

	_, iss = e.Compile(`"a,b".split(",")`)
	assert.Error(t, iss.Err())
}
