// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package host provides CEL extension functions that expose the host's resolved
// configuration directory to in-process resolver expressions. The directory is
// resolved through an injectable provider (see SetConfigDirProvider) so an
// embedder can make the path branding-correct (e.g. honor its own app name)
// instead of hardcoding a path. The library default uses os.UserConfigDir().
package host

import (
	"os"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/oakwood-commons/celexp"
)

var (
	configDirMu sync.RWMutex
	// configDirProvider resolves the host configuration directory. Defaults to
	// defaultConfigDir; an embedder may override via SetConfigDirProvider (e.g.
	// to honor a branded app name).
	configDirProvider = defaultConfigDir
)

// defaultConfigDir is the library's built-in resolver: the OS user config dir
// (e.g. ~/.config on Linux), or "" if it cannot be determined.
func defaultConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}

// SetConfigDirProvider overrides how host.configDir() resolves the configuration
// directory. Intended to be called once at startup by an adapter/embedder (e.g.
// to inject a branding-aware path). A nil fn resets to the library default.
func SetConfigDirProvider(fn func() string) {
	configDirMu.Lock()
	defer configDirMu.Unlock()
	if fn == nil {
		configDirProvider = defaultConfigDir
		return
	}
	configDirProvider = fn
}

func resolveConfigDir() string {
	configDirMu.RLock()
	fn := configDirProvider
	configDirMu.RUnlock()
	return fn()
}

// ConfigDirFunc returns the host's resolved configuration directory. It is
// registered under two names: the namespaced, CEL-idiomatic host.configDir()
// and the portable hostConfigDir() that matches the Go template function name,
// so the same identifier works in resolver expressions and templates alike.
func ConfigDirFunc() celexp.ExtFunction {
	const dotted = "host.configDir"
	const portable = "hostConfigDir"
	binding := cel.FunctionBinding(func(_ ...ref.Val) ref.Val {
		return types.String(resolveConfigDir())
	})
	return celexp.ExtFunction{
		Name:          dotted,
		Signature:     "host.configDir() -> string",
		Description:   "Returns the host's resolved configuration directory (branding-aware, honors the embedder's app name). Use host.configDir() (or the portable alias hostConfigDir(), which matches the Go template function) to build paths under the config dir without hardcoding a branded path",
		FunctionNames: []string{dotted, portable},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Build a config.d drop-in path",
				Expression:  `host.configDir() + "/config.d/clusters.yaml"`,
			},
			{
				Description: "Portable alias matching the Go template function name",
				Expression:  `hostConfigDir() + "/config.d/clusters.yaml"`,
			},
		},
		EnvOptions: []cel.EnvOption{
			cel.Function(dotted,
				cel.Overload(strings.ReplaceAll(dotted, ".", "_"),
					[]*cel.Type{},
					cel.StringType,
					binding,
				),
			),
			cel.Function(portable,
				cel.Overload(portable,
					[]*cel.Type{},
					cel.StringType,
					binding,
				),
			),
		},
	}
}
