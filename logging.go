// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
)

// SEAM 1: logging via go-logr.
//
// The library previously read scafctl's own logger context key
// (logger.FromContext). Because scafctl's logger is a thin wrapper over go-logr,
// the library depends on go-logr directly and resolves a logger through its own
// context key. An embedder that already stores a *logr.Logger under a different
// context key can bridge it via SetLoggerProvider (once, at init), so per-context
// loggers keep working without changing any call site.

type loggerCtxKey struct{}

var (
	loggerProviderMu sync.RWMutex
	// loggerProvider, when set, resolves a logger from a context. This lets an
	// embedder bridge its own context logger into the library. It is consulted
	// only when the library's own context key is absent.
	loggerProvider func(context.Context) logr.Logger
)

// WithLogger attaches a logr.Logger to the context for the library to use.
func WithLogger(ctx context.Context, l logr.Logger) context.Context {
	return context.WithValue(ctx, loggerCtxKey{}, l)
}

// SetLoggerProvider registers a fallback that resolves a logger from a context
// when the library's own context key is not present. It is intended to be called
// once during startup by an adapter/embedder (e.g. to bridge scafctl's logger).
// A nil fn clears the provider.
func SetLoggerProvider(fn func(context.Context) logr.Logger) {
	loggerProviderMu.Lock()
	defer loggerProviderMu.Unlock()
	loggerProvider = fn
}

// loggerFromContext returns the logger attached via WithLogger, else the one
// resolved by a registered provider, else a discard logger. This is the internal
// replacement for scafctl's logger.FromContext(ctx).
func loggerFromContext(ctx context.Context) logr.Logger {
	if l, ok := ctx.Value(loggerCtxKey{}).(logr.Logger); ok {
		return l
	}
	loggerProviderMu.RLock()
	fn := loggerProvider
	loggerProviderMu.RUnlock()
	if fn != nil {
		return fn(ctx)
	}
	return logr.Discard()
}
