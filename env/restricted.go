// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/oakwood-commons/celexp/ext"
)

// SafePredicate is a named allowlist of namespaces suitable for evaluating
// semi-untrusted expressions such as user-supplied validation predicates. It
// deliberately excludes any namespace that performs I/O, touches the host
// filesystem/environment, or can stall a goroutine:
//
//   - "filepath", "host": filesystem/host information disclosure
//   - "debug": debug.sleep can block a goroutine indefinitely; debug.throw
//     can be used to trigger arbitrary panics
//   - "time": wall-clock reads are a side channel and a source of
//     non-determinism in a predicate that is meant to be pure
//   - "guid": non-deterministic output, not I/O per se but not appropriate
//     for a value that is meant to be evaluated repeatedly and compared
//   - "url", "marshalling", "encoders", "protos": not currently needed by a
//     pure boolean predicate; omitted to keep the allowlist minimal rather
//     than because each is independently dangerous
//
// This is a starting point, not a guarantee: any namespace added to
// SafePredicate in the future must be re-audited against this same bar.
// Callers with different requirements should build their own allowlist with
// NewRestricted directly instead of extending this one in place.
var SafePredicate = []string{
	"strings",
	"lists",
	"math",
	"sets",
	"regex",
	"lang",
	"arrays",
	"map",
	"sort",
	"out",
}

// NewRestricted creates a CEL environment scoped to an explicit allowlist of
// extension namespaces (see ext.Namespaces for the full set), plus any
// caller-supplied declarations. Unlike New, it never loads the full
// ext.All() extension set and is unaffected by bootstrap.Default() having
// been called elsewhere in the process -- it talks to cel.NewEnv directly,
// bypassing the package-level base-environment cache entirely.
//
// An unknown namespace in allow is a hard error (wrapping
// ext.ErrUnknownNamespace): a typo must not silently narrow the sandbox
// further than intended, nor silently fail to narrow it at all.
//
// Restricted environments built from different allowlists are not
// distinguished by the traditional-mode compilation cache key beyond their
// function set (see ProgramCache), so callers evaluating expressions under
// more than one allowlist -- or mixing a restricted env with the default one
// in the same process -- should pass a dedicated *ProgramCache via
// celexp.WithCache(...) per allowlist rather than relying on the shared
// package-level default cache.
//
// Example:
//
//	e, err := env.NewRestricted(ctx, env.SafePredicate, cel.Variable("x", cel.IntType))
//	if err != nil {
//	    return err
//	}
//	// e can compile `x > 2` and `"a,b".split(",")` but not `host.configDir("x")`.
func NewRestricted(ctx context.Context, allow []string, declarations ...cel.EnvOption) (*cel.Env, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	baseOpts, err := ext.SelectOptions(allow...)
	if err != nil {
		return nil, fmt.Errorf("failed to select restricted namespaces: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	envOpts := make([]cel.EnvOption, 0, len(baseOpts)+len(declarations))
	envOpts = append(envOpts, baseOpts...)
	envOpts = append(envOpts, declarations...)

	return cel.NewEnv(envOpts...)
}
