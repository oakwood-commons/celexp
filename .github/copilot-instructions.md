# celexp - AI Agent Instructions

## Overview

Standalone Go library for compiling and evaluating [Common Expression Language
(CEL)](https://github.com/google/cel-spec) expressions, with a compiled-program
cache and a rich set of extension-function namespaces (`strings.*`, `arrays.*`,
`map.*`, `regex.*`, `time.*`, `filepath.*`, and more).

The library is **dependency-light by design**: its only non-test third-party
dependencies are `github.com/google/cel-go`, `github.com/go-logr/logr`,
`github.com/google/uuid`, `golang.org/x/text`, and `gopkg.in/yaml.v3`. It must
never depend on any application/CLI module. Application-specific concerns
(logging destination, debug output sink, config-directory resolution) are
injected via seams, not imported.

## Key Patterns

- **Logging**: Use `logr.Logger` via the library's context helper -- never
  `log.Printf`/`fmt.Printf`. Absent an injected logger, logging is a no-op
  (`logr.Discard()`).
- **Debug output**: The `debug.out` CEL function writes through a one-method
  `Sink` interface, injected by the embedder. Never import a concrete
  writer.
- **Config dir**: `host.configDir()` resolves via an injected provider so the
  embedder controls branding; the library default uses `os.UserConfigDir()`.
- **Errors**: wrap with `fmt.Errorf("context: %w", err)`; CEL functions return
  `types.NewErr(...)` (a `ref.Val`), never panic.
- **Thread safety**: the program cache and env construction are safe for
  concurrent use.

## Build & Test Commands

```bash
go build ./...     # verify it compiles
task test          # run tests
task test:race     # race detector
task lint          # golangci-lint (pinned version)
task bench         # benchmarks
task ci            # full pipeline
```

## Critical Rules

- **No dependency on any application/CLI module.** `go list -deps ./...` must
  never contain a downstream consumer's module path. This is the whole point of
  the extraction.
- **Injection over import for app concerns.** Logger, `Sink`, and
  config-dir provider are injected. Do not import a concrete logger/writer/paths
  package.
- **Extension registry uses factory indirection.** The root package cannot
  import `env` (env imports the root for `ExtFunction`/`ProgramCache`), so `env`
  registers itself via `SetEnvFactory`/`SetCacheFactory`. Embedders that want
  the full extension set must call these (use the `bootstrap` package). These
  setters are intentionally one-shot (first call wins).
- **CEL function names are stable API.** A Go package rename (e.g. `celstrings`)
  must NOT change the CEL-visible function name (`strings.slugify`), which is a
  string literal in `ExtFunction.Name`.
- **Test coverage**: every new/changed file needs tests. Target 70%+ patch
  coverage; never ship a new file at 0%.
- **Breaking changes**: allowed -- pre-1.0. Note them.
- **Git safety**: never run `git commit`, `git push`, or `git commit --amend`
  unless the user explicitly asks.

## Conventions

- **Commits**: [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/#specification).
- **Squash-merge**: PRs squash to one commit built from the PR title/body.
- **Signing**: all commits GPG/SSH signed (`-S`) and DCO signed-off (`-s`).
- **File headers**: `// Copyright 2025-2026 Oakwood Commons` +
  `// SPDX-License-Identifier: Apache-2.0`.

## Layout (flat root -- not `pkg/`)

- `celexp.go`, `cache.go`, `context.go`, `data.go`, `validation.go`, `refs.go`,
  `refs_position.go`, `equality.go`, `helpers.go`, `appconfig.go`, `logging.go`
  -- root `celexp` package: `Expression`, `ExtFunction`, `ProgramCache`,
  compile/evaluate/validate, the logger seam.
- `conversion/` -- Go <-> CEL value conversion helpers.
- `detail/` -- function detail/documentation builders.
- `env/` -- CEL environment construction (`New`, `NewWithSink`), the
  factory registration, and the debug-sink seam.
- `ext/` -- the extension registry (`All`/`BuiltIn`/`Custom`) and namespace
  subpackages (`arrays`, `debug`, `celfilepath`, `guid`, `host`, `map`,
  `marshalling`, `out`, `celregex`, `sort`, `celstrings`, `time`, `celurl`).
- `bootstrap/` -- convenience `Default()` that wires the env/cache factories.
- `internal/` -- moved leaf helpers (`compare`, `arrays`, `dnslabel`,
  `pathnorm`) with no external-consumer value.
