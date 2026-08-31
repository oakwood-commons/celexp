// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package env

import (
	"context"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/oakwood-commons/celexp/ext"
	"github.com/oakwood-commons/celexp/ext/debug"
)

// SEAM 2: debug output via the debug.Sink interface.
//
// env.New auto-discovers a debug sink from the context. The library reads its own
// context key (WithSink); an embedder that stores its writer under a
// different key can bridge it once via SetSinkProvider, so env.New keeps
// wiring debug.out automatically without any call-site change.

type debugSinkCtxKey struct{}

var (
	debugSinkProviderMu sync.RWMutex
	debugSinkProvider   func(context.Context) debug.Sink
)

// WithSink attaches a debug.Sink to the context so env.New wires
// debug.out to it automatically.
func WithSink(ctx context.Context, sink debug.Sink) context.Context {
	return context.WithValue(ctx, debugSinkCtxKey{}, sink)
}

// SetSinkProvider registers a fallback that resolves a debug sink from a
// context when the library's own context key is absent. Intended to be called
// once at startup by an adapter/embedder. A nil fn clears the provider.
func SetSinkProvider(fn func(context.Context) debug.Sink) {
	debugSinkProviderMu.Lock()
	defer debugSinkProviderMu.Unlock()
	debugSinkProvider = fn
}

// debugSinkFromContext returns the sink attached via WithSink, else the one
// resolved by a registered provider, else nil (debug.out silently skips).
func debugSinkFromContext(ctx context.Context) debug.Sink {
	if s, ok := ctx.Value(debugSinkCtxKey{}).(debug.Sink); ok {
		return s
	}
	debugSinkProviderMu.RLock()
	fn := debugSinkProvider
	debugSinkProviderMu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

var (
	// baseEnvMu protects baseEnv initialization state.
	baseEnvMu sync.Mutex
	// baseEnvInitialized tracks whether base environment options have been created.
	baseEnvInitialized bool
	// baseEnvOpts contains all extension function options (BuiltIn + Custom), cached for reuse.
	// Note: debug.DebugOutFunc is NOT included here because it requires a debug.Sink parameter.
	// It is added separately via NewWithSink() or by callers using DebugOutEnvOptions().
	baseEnvOpts []cel.EnvOption
	// baseEnvErr stores any error from base environment initialization
	baseEnvErr error
)

// getBaseEnvOptions returns cached extension options, creating them once.
// This optimizes repeated calls to New() by avoiding repeated ext.All() calls.
// Context cancellation is checked before and after extension loading, but the
// loading itself is not context-dependent and will complete once started.
//
// Note: debug.DebugOutFunc is NOT included in the cached options because it
// requires a debug.Sink parameter. Use DebugOutEnvOptions() to add it separately.
func getBaseEnvOptions(ctx context.Context) ([]cel.EnvOption, error) {
	// Check context before potentially waiting on mutex
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	baseEnvMu.Lock()
	if !baseEnvInitialized {
		// Get all CEL extension functions (both built-in and custom)
		// Note: ext.All() excludes debug.DebugOutFunc which requires a debug.Sink
		extFuncs := ext.All()

		// Pre-allocate based on typical extension count
		baseEnvOpts = make([]cel.EnvOption, 0, len(extFuncs)*2)

		// Add all extension function EnvOptions
		for _, extFunc := range extFuncs {
			baseEnvOpts = append(baseEnvOpts, extFunc.EnvOptions...)
		}
		// baseEnvErr is intentionally left nil on success
		baseEnvInitialized = true
	}
	baseEnvMu.Unlock()

	// If initialization encountered an error, return it
	if baseEnvErr != nil {
		return nil, baseEnvErr
	}

	// Check context after initialization completes - this allows callers with cancelled
	// contexts to get an error even though the extensions were successfully cached
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return baseEnvOpts, nil
}

// resetBaseEnvForTesting resets the base environment state for testing.
// This is safe for use in tests as it acquires the mutex before resetting.
// WARNING: This should only be called from tests.
func resetBaseEnvForTesting() {
	baseEnvMu.Lock()
	defer baseEnvMu.Unlock()
	baseEnvInitialized = false
	baseEnvOpts = nil
	baseEnvErr = nil
}

// DebugOutEnvOptions returns the CEL environment options for debug.out with the
// given sink. This is useful for adding debug.out support to environments created
// via New(). If sink is nil, debug.out will silently skip output.
func DebugOutEnvOptions(sink debug.Sink) []cel.EnvOption {
	return debug.DebugOutFunc(sink).EnvOptions
}

// New creates a new CEL environment with the provided declarations and all
// registered CEL extension functions from the ext package.
// It accepts variadic EnvOptions to allow for multiple declarations and other options.
//
// The function caches base extension options for performance, so repeated calls
// are much faster than the first call. The context is checked for cancellation
// before and during environment construction.
//
// debug.out is automatically included if a debug sink is found in the context via
// WithSink (or a provider registered with SetSinkProvider). If no sink
// is available, debug.out is a no-op. To explicitly control debug.out, use
// NewWithSink() instead.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	env, err := env.New(ctx, cel.Variable("x", cel.IntType))
func New(ctx context.Context, declarations ...cel.EnvOption) (*cel.Env, error) {
	// Get cached base extension options (excludes debug.DebugOutFunc)
	baseOpts, err := getBaseEnvOptions(ctx)
	if err != nil {
		return nil, err
	}

	// Check if a debug sink is available in context for debug.out support
	sink := debugSinkFromContext(ctx)
	debugOpts := DebugOutEnvOptions(sink) // nil-safe: debug.out silently skips if sink is nil

	// Combine base options, debug.out options, and user declarations
	envOpts := make([]cel.EnvOption, 0, len(baseOpts)+len(debugOpts)+len(declarations))
	envOpts = append(envOpts, baseOpts...)
	envOpts = append(envOpts, debugOpts...)
	envOpts = append(envOpts, declarations...)

	// Final context check before creating environment
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return cel.NewEnv(envOpts...)
}

// NewWithSink creates a new CEL environment with debug.out support.
// This is a convenience wrapper around New() that includes debug.DebugOutFunc.
//
// The sink is used by debug.out for debug output. Pass nil if debug output should
// be silently skipped.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	env, err := env.NewWithSink(ctx, sink, cel.Variable("x", cel.IntType))
func NewWithSink(ctx context.Context, sink debug.Sink, declarations ...cel.EnvOption) (*cel.Env, error) {
	// Prepend debug.out options to user declarations
	debugOpts := DebugOutEnvOptions(sink)
	allDeclarations := make([]cel.EnvOption, 0, len(debugOpts)+len(declarations))
	allDeclarations = append(allDeclarations, debugOpts...)
	allDeclarations = append(allDeclarations, declarations...)

	return New(ctx, allDeclarations...)
}
