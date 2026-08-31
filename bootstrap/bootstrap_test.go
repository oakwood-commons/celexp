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
}

// TestDefault_Idempotent confirms calling Default() more than once is safe
// (the underlying setters are one-shot; extra calls are no-ops).
func TestDefault_Idempotent(t *testing.T) {
	require.NotPanics(t, func() {
		bootstrap.Default()
		bootstrap.Default()
	})
}
