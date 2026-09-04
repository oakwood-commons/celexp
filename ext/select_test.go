// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllEntriesHaveNamespace guards against a future extension silently
// landing outside the namespace/allowlist model. Without this test, a new
// entry added to BuiltIn() or Custom() with an empty Namespace would be
// dropped by every Select/SelectOptions call and NEVER reachable by any
// restricted environment -- i.e. it would "fail closed" for restricted
// callers, but worse, would do so silently. This test forces the omission to
// be caught in CI instead of discovered by a confused caller.
func TestAllEntriesHaveNamespace(t *testing.T) {
	for _, f := range All() {
		assert.NotEmptyf(t, f.Namespace, "ExtFunction %q has no Namespace assigned", f.Name)
	}
}

func TestNamespaces(t *testing.T) {
	names := Namespaces()
	require.NotEmpty(t, names)

	// Sorted
	for i := 1; i < len(names); i++ {
		assert.LessOrEqual(t, names[i-1], names[i], "Namespaces() must be sorted")
	}

	// De-duplicated
	seen := make(map[string]bool)
	for _, n := range names {
		assert.False(t, seen[n], "duplicate namespace %q", n)
		seen[n] = true
	}

	// Spot-check known namespaces are present
	for _, want := range []string{"strings", "regex", "lists", "math", "debug", "host", "filepath"} {
		assert.Contains(t, names, want)
	}
}

func TestSelect(t *testing.T) {
	t.Run("known namespace returns matching entries only", func(t *testing.T) {
		funcs, err := Select("regex")
		require.NoError(t, err)
		require.NotEmpty(t, funcs)
		for _, f := range funcs {
			assert.Equal(t, "regex", f.Namespace)
		}
	})

	t.Run("multiple namespaces", func(t *testing.T) {
		funcs, err := Select("strings", "regex")
		require.NoError(t, err)
		var sawStrings, sawRegex bool
		for _, f := range funcs {
			switch f.Namespace {
			case "strings":
				sawStrings = true
			case "regex":
				sawRegex = true
			default:
				t.Fatalf("unexpected namespace %q in result", f.Namespace)
			}
		}
		assert.True(t, sawStrings)
		assert.True(t, sawRegex)
	})

	t.Run("excludes dangerous namespaces when not requested", func(t *testing.T) {
		funcs, err := Select("strings")
		require.NoError(t, err)
		for _, f := range funcs {
			assert.NotEqual(t, "host", f.Namespace)
			assert.NotEqual(t, "debug", f.Namespace)
			assert.NotEqual(t, "filepath", f.Namespace)
		}
	})

	t.Run("duplicate names in input are harmless", func(t *testing.T) {
		funcs, err := Select("strings", "strings")
		require.NoError(t, err)
		once, err := Select("strings")
		require.NoError(t, err)
		assert.Len(t, funcs, len(once))
	})

	t.Run("empty allowlist returns empty, not error", func(t *testing.T) {
		funcs, err := Select()
		require.NoError(t, err)
		assert.Empty(t, funcs)
	})

	t.Run("unknown namespace is a hard error", func(t *testing.T) {
		funcs, err := Select("not-a-real-namespace")
		require.Error(t, err)
		assert.Nil(t, funcs)
		assert.True(t, errors.Is(err, ErrUnknownNamespace))
		assert.Contains(t, err.Error(), "not-a-real-namespace")
	})

	t.Run("one unknown namespace fails the whole call, not a partial result", func(t *testing.T) {
		funcs, err := Select("strings", "not-a-real-namespace")
		require.Error(t, err)
		assert.Nil(t, funcs)
	})
}

func TestSelectOptions(t *testing.T) {
	t.Run("flattens EnvOptions", func(t *testing.T) {
		opts, err := SelectOptions("strings")
		require.NoError(t, err)
		assert.NotEmpty(t, opts)
	})

	t.Run("propagates unknown namespace error", func(t *testing.T) {
		opts, err := SelectOptions("not-a-real-namespace")
		require.Error(t, err)
		assert.Nil(t, opts)
		assert.True(t, errors.Is(err, ErrUnknownNamespace))
	})
}
