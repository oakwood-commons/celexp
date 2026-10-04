# celexp

A standalone Go library for compiling and evaluating
[Common Expression Language (CEL)](https://github.com/google/cel-spec)
expressions, with a compiled-program cache and a rich set of extension-function
namespaces.

`celexp` was extracted from a larger CLI so that any application can embed the
CEL engine without pulling in unrelated code. It is **dependency-light**: its
only non-test third-party dependencies are `cel-go`, `go-logr`, `google/uuid`,
`golang.org/x/text`, `gopkg.in/yaml.v3`, and the CEL protobuf types from
`google.golang.org/genproto` and `google.golang.org/protobuf`.

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

- `celexp` (root) -- `Expression`, `Condition`, `LazyMap`, `ExtFunction`, `ProgramCache`,
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
own standard extensions (`lists`, `math`, `sets`, `encoders`, `protos`,
`lang`, ...). Call `ext.Namespaces()` for the authoritative, sorted list.

Every `ExtFunction` returned by `ext.All()`/`BuiltIn()`/`Custom()` carries a
`Namespace` -- this is the allowlist unit consumed by `ext.Select`,
`ext.SelectOptions`, and `env.NewRestricted` (below). It is assigned
explicitly per entry rather than derived from `Name`, because `Name` is not a
uniform namespace: a `BuiltIn()` entry names a cel-go extension/option (e.g.
`"strings"`, `"optionalTypes"`), while a `Custom()` entry names a
fully-qualified function (e.g. `"regex.match"`). The overlap between cel-go's
built-in `strings` extension and celexp's `strings.*` custom functions
sharing the `strings` namespace is deliberate: allowlisting `"strings"` gets
both.

## Restricted environments

`bootstrap.Default()` and `env.New` load the **full** extension set,
including namespaces that touch the filesystem/host (`filepath`, `host`),
can block or panic (`debug`), or are non-deterministic (`time`, `guid`).
That's the right default for a trusted embedding, but wrong for evaluating
semi-untrusted input such as a user-supplied validation predicate.

`env.NewRestricted` builds a CEL environment from an explicit namespace
allowlist instead, bypassing the full extension set and the package-level
base-environment cache entirely -- it is unaffected by whether
`bootstrap.Default()` has been called elsewhere in the process, so a single
process can safely run both a full and a restricted environment side by side.

```go
e, err := env.NewRestricted(ctx, env.SafePredicate, cel.Variable("x", cel.IntType))
if err != nil {
    return err
}
// e compiles `x > 2` and `"a,b".split(",")`, but NOT `host.configDir("x")`,
// `debug.sleep(1)`, `filepath.exists(...)`, or `time.now()`.
```

`env.SafePredicate` is a pre-built allowlist (`strings`, `lists`, `math`,
`sets`, `regex`, `lang`, `arrays`, `map`, `sort`, `out`) suited to pure boolean
predicates. Build a custom allowlist with `env.NewRestricted(ctx, []string{...})`
directly, or inspect/validate namespace names with `ext.Namespaces()` /
`ext.Select()` / `ext.SelectOptions()`.

An unknown namespace name is a hard error -- a typo must not silently narrow
(or fail to narrow) the sandbox.

**Cache isolation:** `celexp.GlobalCache`/the default `ProgramCache` is shared
process-wide and keyed (in its traditional mode) on the compiled function set,
so a restricted and a full environment do not collide. Still, if a process
evaluates expressions under more than one restricted allowlist, pair each
one with its own cache via `celexp.WithCache(...)` rather than relying on the
shared default cache.

## Conditions

`celexp.Condition` is a boolean CEL expression written in YAML or JSON. It
accepts four forms:

```yaml
when: true                                  # boolean literal
when: "_.env == 'prod'"                     # string shorthand
when: { expr: "_.env == 'prod'" }           # explicit object
when: { expression: "_.env == 'prod'" }     # alias
```

Anything else (numbers, lists, empty strings, objects with neither or both of
`expr`/`expression`, unknown keys, null values) is rejected; YAML errors carry
the line and column. Marshalling emits the canonical form: a bare boolean for
`true`/`false`, otherwise the string shorthand.

Conditions fail closed. A null/absent condition (nil `Condition` or nil
`Expr`) is not "true": `Expression()` and the `Evaluate*` helpers return
`celexp.ErrNoCondition`. Callers that want "absent = run" check for nil (or
`errors.Is(err, celexp.ErrNoCondition)`) themselves.

Compile once with your own declarations and options, then evaluate. A
non-boolean result is an error, never a silent `false`. Variables may be lazy
-- `func() any` or `func() (any, error)` -- and are computed only if the
expression reads them, at most once per evaluation. A fact's error fails the
evaluation and is wrapped, so `errors.Is`/`errors.As` work on the result. The
caller's vars map is never modified, so it can be reused:

```go
expr, err := cond.Expression() // celexp.ErrNoCondition if absent
if err != nil {
    return err
}
prog, err := expr.CompileWithVarDecls(
    []celexp.VarDecl{celexp.NewVarDecl("user", cel.DynType)},
    celexp.WithCostLimit(10_000),
)
if err != nil {
    return err
}
ok, err := prog.EvalBool(ctx, map[string]any{
    "user": func() (any, error) { return loadUser() }, // skipped if short-circuited
})
```

For one-shot evaluation with root data bound to `_`, use `cond.Evaluate`.

### Lazy maps

cel-go defers only top-level variables: a plain map of lazy funcs bound to
`_` is resolved as a whole as soon as `_.a` is read. `celexp.LazyMap`
defers each entry instead. Its values may be `func() (any, error)`,
`func() any`, or plain values; declare the variable as `map(string, dyn)`
(or pass it as `rootData` to `cond.Evaluate`). LazyMaps may be nested, inside
each other or inside ordinary `map[string]any` values.

```go
facts := celexp.LazyMap{
    "env":   func() (any, error) { return detectEnv() },
    "quota": func() (any, error) { return fetchQuota(ctx) }, // never runs below
}
ok, err := cond.Evaluate(ctx, facts) // when: "has(_.quota) && _.env == 'prod'"
```

- Reading `_.a` (or `_["a"]`) runs only `a`'s func, at most once per
  evaluation; an entry that is never read never runs.
- `has(_.a)`, `'a' in _`, and `size(_)` never run a func.
- A func's error fails the evaluation, wrapped (`errors.Is`/`errors.As`).
- Resolved values are adapted like eager vars (maps, lists, nested LazyMaps).
- **Iterating** the map with a single-variable macro (`all`/`exists`/
  `exists_one`/`map`/`filter`) resolves every entry. `==` is pointer
  identity of the per-evaluation view (`_ == _` is true; a LazyMap never
  equals a map literal) and resolves nothing. Converting or returning the
  whole map is an error and resolves nothing.
- **Limitation:** the view is not a full CEL map, so passing the LazyMap
  itself to a function with a map parameter (the `map.*` extension
  functions, `format`), two-variable comprehensions, and `transformMap`
  fail with an evaluation error. Pass its entries (`_.a`) or an ordinary
  map instead. (Making it a full map would make `has(_.a)` resolve the
  entry.)
- A LazyMap holds no state. Each evaluation wraps it in a fresh, concurrency-
  safe resolver whose memo lives only for that evaluation, so one LazyMap can
  be reused across evaluations and goroutines without leaking an earlier
  result (its funcs must then be safe to call concurrently).

LazyMap is honored by `EvalBool` and the `Condition` `Evaluate*` helpers. The
design follows Kubernetes' `lazy.MapValue` (`k8s.io/apiserver/pkg/cel/lazy`),
with stricter defaults: presence checks do not resolve, memoization is
per-evaluation, and resolution is concurrency-safe.

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
