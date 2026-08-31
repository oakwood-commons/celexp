// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package bootstrap wires the celexp core to its environment/cache factories.
//
// The root celexp package cannot import the env package directly (env imports
// the root for ExtFunction/ProgramCache), so env registers itself with the core
// via SetEnvFactory/SetCacheFactory. Without that registration the core falls
// back to a bare cel.NewEnv() with no custom extensions, so expressions using
// strings.*, arrays.*, etc. fail to compile.
//
// Embedders should call Default() once at startup to get the full extension set.
package bootstrap

import (
	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/env"
)

// Default registers the standard environment and cache factories so the celexp
// core builds environments with all built-in and custom extensions loaded. It is
// safe to call multiple times; the underlying setters are one-shot (first call
// wins), matching celexp's SetEnvFactory/SetCacheFactory semantics.
func Default() {
	celexp.SetEnvFactory(env.New)
	celexp.SetCacheFactory(env.GlobalCache)
}
