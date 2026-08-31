# celexp

A standalone Go library for compiling and evaluating
[Common Expression Language (CEL)](https://github.com/google/cel-spec)
expressions, with a compiled-program cache and a rich set of extension-function
namespaces.

`celexp` was extracted from a larger CLI so that any application can embed the
CEL engine without pulling in unrelated code. It is **dependency-light**: its
only non-test third-party dependencies are `cel-go`, `go-logr`, `google/uuid`,
`golang.org/x/text`, and `gopkg.in/yaml.v3`.

## Install

```bash
go get github.com/oakwood-commons/celexp
```

## Quick start

Call `bootstrap.Default()` once at startup so the full built-in **and** custom
extension set is registered, then evaluate expressions:

```go
package main

import (
 "context"
 "fmt"

 "github.com/oakwood-commons/celexp"
 "github.com/oakwood-commons/celexp/bootstrap"
)

func main() {
 bootstrap.Default() // register env + cache factories (once)

 ctx := context.Background()

 // Root data is bound to `_`; additional variables are top-level.
 out, _ := celexp.EvaluateExpression(ctx,
  `prefix + " " + _.name`,
  map[string]any{"name": "world"},   // rootData -> _
  map[string]any{"prefix": "Hello"}, // additional vars
 )
 fmt.Println(out) // Hello world

 // Custom extension functions are available after bootstrap.Default().
 slug, _ := celexp.EvaluateExpression(ctx, `strings.slugify("Hello, World!")`, nil, nil)
 fmt.Println(slug) // hello-world
}
```

## Why `bootstrap.Default()`?

The root `celexp` package cannot import the `env` package directly (that would
be an import cycle: `env` imports the root for `ExtFunction`/`ProgramCache`).
Instead, `env` registers itself with the core via `SetEnvFactory` /
`SetCacheFactory`. `bootstrap.Default()` performs that registration. Without it,
the core falls back to a bare `cel.NewEnv()` with no custom extensions, and
expressions using `strings.*`, `arrays.*`, etc. will fail to compile.

## Injection seams

Application-specific concerns are injected, not imported, keeping the library
free of any application dependency:

- **Logging** -- attach a `logr.Logger` with `celexp.WithLogger(ctx, l)`, or
  register a process-wide bridge with `celexp.SetLoggerProvider(...)`. Absent
  either, logging is a no-op (`logr.Discard()`).
- **Debug output** -- the `debug.out` CEL function writes through the one-method
  `debug.Sink` interface. Provide one via `env.WithSink(ctx, sink)` or
  `env.SetSinkProvider(...)`. Any type with `DebugOutf(format string, args ...any)`
  satisfies it.
- **Config directory** -- `host.configDir()` resolves through
  `host.SetConfigDirProvider(func() string)`; the default uses
  `os.UserConfigDir()`.

## Package layout

- `celexp` (root) -- `Expression`, `ExtFunction`, `ProgramCache`,
  `EvaluateExpression`, compile/evaluate/validate, the logger seam.
- `conversion` -- Go <-> CEL value conversion helpers.
- `detail` -- function detail/documentation builders.
- `env` -- CEL environment construction (`New`, `NewWithSink`), factory
  registration, and the debug-sink seam.
- `ext` -- the extension registry (`All`/`BuiltIn`/`Custom`) and namespace
  subpackages.
- `bootstrap` -- convenience `Default()` wiring.

## Extension namespaces

`arrays`, `debug`, `filepath`, `guid`, `host`, `map`, `marshalling` (`json.*` /
`yaml.*`), `out`, `regex`, `sort`, `strings`, `time`, `url` -- plus cel-go's
own standard extensions.

## Development

```bash
task test        # run tests
task test:race   # race detector
task lint        # golangci-lint (pinned)
task bench       # benchmarks
task ci          # full pipeline
```

## License

Apache-2.0. See [LICENSE](./LICENSE).
