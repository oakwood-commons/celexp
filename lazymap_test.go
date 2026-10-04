// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// counted returns a fact func that counts its calls.
func counted(n *atomic.Int32, v any) func() (any, error) {
	return func() (any, error) { n.Add(1); return v, nil }
}

func lazyRootDecls() []VarDecl {
	return []VarDecl{NewVarDecl("_", cel.MapType(cel.StringType, cel.DynType))}
}

func lazyRootEnv(extra ...cel.EnvOption) []cel.EnvOption {
	return append([]cel.EnvOption{cel.Variable("_", cel.MapType(cel.StringType, cel.DynType))}, extra...)
}

func evalLazy(t *testing.T, expr string, root any) (bool, error) {
	t.Helper()
	prog, err := Expression(expr).CompileWithVarDecls(lazyRootDecls())
	require.NoError(t, err)
	return prog.EvalBool(context.Background(), map[string]any{"_": root})
}

func TestLazyMap_PerKeyLaziness(t *testing.T) {
	tests := []struct {
		expr     string
		want     bool
		wantA    int32
		wantB    int32
		wantMore int32
	}{
		{expr: `_.a == 1`, want: true, wantA: 1},
		{expr: `_["b"] == "x"`, want: true, wantB: 1},
		{expr: `_.a == 1 && _.a + 1 == 2`, want: true, wantA: 1}, // memoized
		{expr: `_.a == 2 && _.b == "x"`, want: false, wantA: 1},  // short-circuit
		{expr: `_.a == 1 || _.b == "x"`, want: true, wantA: 1},
		{expr: `has(_.a) && 'b' in _ && !has(_.zzz) && size(_) == 3`, want: true},
		{expr: `_.more.list[1] == 2 && _.more.m.k == "v"`, want: true, wantMore: 1},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			var a, b, more atomic.Int32
			root := LazyMap{
				"a":    counted(&a, 1),
				"b":    counted(&b, "x"),
				"more": counted(&more, map[string]any{"list": []int{1, 2}, "m": map[string]string{"k": "v"}}),
			}
			got, err := evalLazy(t, tt.expr, root)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantA, a.Load(), "a calls")
			assert.Equal(t, tt.wantB, b.Load(), "b calls")
			assert.Equal(t, tt.wantMore, more.Load(), "more calls")
		})
	}
}

func TestLazyMap_ValueKinds(t *testing.T) {
	root := LazyMap{
		"plain": 5,
		"any":   func() any { return "s" },
		"nested": LazyMap{
			"deep": func() (any, error) { return true, nil },
		},
		"inMap": map[string]any{"lm": LazyMap{"k": func() any { return 7 }}},
	}
	got, err := evalLazy(t, `_.plain == 5 && _.any == "s" && _.nested.deep && _.inMap.lm.k == 7`, root)
	require.NoError(t, err)
	assert.True(t, got)
}

func TestLazyMap_CyclicVarMap(t *testing.T) {
	m := map[string]any{"a": 1, "lm": LazyMap{"k": func() any { return 2 }}}
	m["self"] = m
	prog, err := Expression(`m.a == 1 && m.self.lm.k == 2 && m.self.self.lm.k == 2`).CompileWithVarDecls([]VarDecl{NewVarDecl("m", cel.DynType)})
	require.NoError(t, err)
	got, err := prog.EvalBool(context.Background(), map[string]any{"m": m})
	require.NoError(t, err)
	assert.True(t, got)
	assert.IsType(t, LazyMap{}, m["lm"], "caller's map not modified")
}

func TestLazyMap_SharedAcrossAliases(t *testing.T) {
	var x atomic.Int32
	lm := LazyMap{"x": counted(&x, 1)}
	prog, err := Expression(`a.x == b.x && c.lm.x == 1`).CompileWithVarDecls([]VarDecl{
		NewVarDecl("a", cel.DynType), NewVarDecl("b", cel.DynType), NewVarDecl("c", cel.DynType),
	})
	require.NoError(t, err)
	got, err := prog.EvalBool(context.Background(), map[string]any{"a": lm, "b": lm, "c": map[string]any{"lm": lm}})
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(1), x.Load())
}

type customFact struct{ s string }

// prefixAdapter adapts customFact to a string with its prefix.
type prefixAdapter string

func (p prefixAdapter) NativeToValue(v any) ref.Val {
	if c, ok := v.(customFact); ok {
		return types.String(string(p) + c.s)
	}
	return types.DefaultTypeAdapter.NativeToValue(v)
}

// A cache hit built with another adapter must adapt lazy and eager values
// with the same (cached program's) adapter.
func TestLazyMap_CachedAdapterConsistency(t *testing.T) {
	cache := NewProgramCache(10)
	decls := func(a prefixAdapter) []cel.EnvOption {
		return lazyRootEnv(cel.Variable("x", cel.DynType), cel.CustomTypeAdapter(a))
	}
	vars := map[string]any{"x": customFact{"v"}, "_": LazyMap{"f": func() any { return customFact{"v"} }}}
	for _, a := range []prefixAdapter{"A:", "B:"} {
		prog, err := Expression(`x == _.f && _.f == "`+string(a)+`v"`).Compile(decls(a), WithCache(cache))
		require.NoError(t, err)
		got, err := prog.EvalBool(context.Background(), vars)
		require.NoError(t, err)
		assert.True(t, got, "adapter %s", a)
	}
	// Same expression with a second adapter must not reuse the first's program.
	for _, a := range []prefixAdapter{"A:", "B:"} {
		prog, err := Expression(`_.f == "A:v"`).Compile(decls(a), WithCache(cache))
		require.NoError(t, err)
		got, err := prog.EvalBool(context.Background(), vars)
		require.NoError(t, err)
		assert.Equal(t, a == "A:", got, "adapter %s honored", a)
	}
}

func TestLazyMap_CacheSkipsCustomAdapters(t *testing.T) {
	cache := NewProgramCache(10)
	compile := func(opts ...cel.EnvOption) {
		t.Helper()
		_, err := Expression(`_.f == 1`).Compile(lazyRootEnv(opts...), WithCache(cache))
		require.NoError(t, err)
	}
	// A separate types.Registry is a custom adapter too, as is any other type.
	reg, err := types.NewRegistry()
	require.NoError(t, err)
	compile(cel.CustomTypeAdapter(reg))
	compile(cel.CustomTypeAdapter(prefixAdapter("A:")))
	assert.Equal(t, 0, cache.Stats().Size, "custom adapters are never cached")

	compile()
	compile()
	st := cache.Stats()
	assert.Equal(t, 1, st.Size, "default adapter is cached")
	assert.Equal(t, uint64(1), st.Hits)
}

func TestLazyMap_NilAliases(t *testing.T) {
	var lm LazyMap
	prog, err := Expression(`a == b && size(a) == 0`).CompileWithVarDecls([]VarDecl{NewVarDecl("a", cel.DynType), NewVarDecl("b", cel.DynType)})
	require.NoError(t, err)
	got, err := prog.EvalBool(context.Background(), map[string]any{"a": lm, "b": lm})
	require.NoError(t, err)
	assert.True(t, got)
}

type customAdapter struct{}

func (customAdapter) NativeToValue(v any) ref.Val {
	if c, ok := v.(customFact); ok {
		return types.String("custom:" + c.s)
	}
	return types.DefaultTypeAdapter.NativeToValue(v)
}

func TestLazyMap_CustomAdapter(t *testing.T) {
	opts := lazyRootEnv(cel.CustomTypeAdapter(customAdapter{}))
	for _, pass := range []string{"cache miss", "cache hit"} {
		t.Run(pass, func(t *testing.T) {
			prog, err := Expression(`_.f == "custom:x"`).Compile(opts)
			require.NoError(t, err)
			got, err := prog.EvalBool(context.Background(), map[string]any{"_": LazyMap{"f": func() any { return customFact{"x"} }}})
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}

func TestLazyMap_NestedLaziness(t *testing.T) {
	var outer, x, y atomic.Int32
	inner := LazyMap{"x": counted(&x, 1), "y": counted(&y, 2)}
	root := LazyMap{"inner": func() (any, error) { outer.Add(1); return inner, nil }}
	got, err := evalLazy(t, `has(_.inner.y) && _.inner.x == 1`, root)
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(1), outer.Load())
	assert.Equal(t, int32(1), x.Load())
	assert.Equal(t, int32(0), y.Load())

	// A LazyMap nested in an ordinary map, as a top-level var.
	x.Store(0)
	y.Store(0)
	prog, err := Expression(`facts.lm.y == 2`).CompileWithVarDecls([]VarDecl{NewVarDecl("facts", cel.DynType)})
	require.NoError(t, err)
	vars := map[string]any{"facts": map[string]any{"lm": inner}}
	got, err = prog.EvalBool(context.Background(), vars)
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(0), x.Load())
	assert.Equal(t, int32(1), y.Load())
	assert.IsType(t, LazyMap{}, vars["facts"].(map[string]any)["lm"], "caller's map not modified")
}

func TestLazyMap_Errors(t *testing.T) {
	sentinel := errors.New("boom")
	var ok atomic.Int32
	root := LazyMap{
		"bad": func() (any, error) { return nil, &factError{fact: "bad"} },
		"ok":  counted(&ok, true),
		"s":   func() (any, error) { return nil, sentinel },
	}

	_, err := evalLazy(t, `_.bad || _.ok`, root)
	require.Error(t, err, "error not absorbed by ||")
	var fe *factError
	require.ErrorAs(t, err, &fe)
	assert.Equal(t, "bad", fe.fact)

	_, err = evalLazy(t, `_.ok && _.s`, root)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), `lazy map key "s"`)

	got, err := evalLazy(t, `_.ok || _.bad`, root)
	require.NoError(t, err, "unread erroring entry never runs")
	assert.True(t, got)

	got, err = evalLazy(t, `has(_.bad) && 'bad' in _`, root)
	require.NoError(t, err, "presence never runs the func")
	assert.True(t, got)
}

func TestLazyMap_ReuseAcrossEvaluations(t *testing.T) {
	prog, err := Expression(`_.v == want`).CompileWithVarDecls([]VarDecl{
		NewVarDecl("_", cel.MapType(cel.StringType, cel.DynType)),
		NewVarDecl("want", cel.IntType),
	})
	require.NoError(t, err)

	var v atomic.Int64
	root := LazyMap{"v": func() any { return v.Load() }}
	for i := int64(1); i <= 3; i++ {
		v.Store(i)
		got, err := prog.EvalBool(context.Background(), map[string]any{"_": root, "want": i})
		require.NoError(t, err)
		assert.True(t, got, "evaluation %d must see the current value", i)
	}
}

func TestLazyMap_Concurrent(t *testing.T) {
	prog, err := Expression(`_.a == n && _.b == n * 2`).CompileWithVarDecls([]VarDecl{
		NewVarDecl("_", cel.MapType(cel.StringType, cel.DynType)),
		NewVarDecl("n", cel.IntType),
	})
	require.NoError(t, err)

	var calls atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Add(1)
		go func(n int64) {
			defer wg.Done()
			// A fresh LazyMap per goroutine: each evaluation resolves its
			// own entries exactly once.
			root := LazyMap{
				"a": func() (any, error) { calls.Add(1); return n, nil },
				"b": func() (any, error) { calls.Add(1); return n * 2, nil },
			}
			got, err := prog.EvalBool(context.Background(), map[string]any{"_": root, "n": n})
			if err == nil && !got {
				err = fmt.Errorf("n=%d: got false", n)
			}
			errs <- err
		}(int64(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(100), calls.Load())

	// One LazyMap shared by concurrent evaluations, with the map itself read
	// concurrently inside each (exists over the same entries).
	var shared atomic.Int32
	root := LazyMap{"a": counted(&shared, int64(1)), "b": counted(&shared, int64(2))}
	prog2, err := Expression(`_.exists(k, _[k] == 2) && _.a == 1`).CompileWithVarDecls(lazyRootDecls())
	require.NoError(t, err)
	var wg2 sync.WaitGroup
	for range 20 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			got, err := prog2.EvalBool(context.Background(), map[string]any{"_": root})
			assert.NoError(t, err)
			assert.True(t, got)
		}()
	}
	wg2.Wait()
	assert.Equal(t, int32(40), shared.Load(), "each evaluation resolves each entry once")
}

func TestLazyMap_IterationAndEquality(t *testing.T) {
	var a, b atomic.Int32
	root := LazyMap{"a": counted(&a, 1), "b": counted(&b, 2)}

	got, err := evalLazy(t, `_.all(k, _[k] > 0)`, root)
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(1), a.Load(), "iteration resolves each entry once")
	assert.Equal(t, int32(1), b.Load())

	got, err = evalLazy(t, `_ == _ && _ != {} && {} != _ && !(_ == {"a": 1}) && [_] == [_]`, root)
	require.NoError(t, err)
	assert.True(t, got, "pointer identity, symmetric with map literals")
	assert.Equal(t, int32(1), a.Load(), "== resolves nothing further")

	bad := LazyMap{"e": func() (any, error) { return nil, errors.New("iter boom") }}
	_, err = evalLazy(t, `_.exists(k, k == "zzz")`, bad)
	require.ErrorContains(t, err, "iter boom", "iteration surfaces fact errors")
}

func TestLazyMap_OptionalAccess(t *testing.T) {
	var a, b atomic.Int32
	root := LazyMap{"a": counted(&a, 1), "b": counted(&b, 2)}
	prog, err := Expression(`_.?a.orValue(0) == 1 && !_[?"zz"].hasValue()`).Compile(lazyRootEnv(cel.OptionalTypes()))
	require.NoError(t, err)
	got, err := prog.EvalBool(context.Background(), map[string]any{"_": root})
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(1), a.Load())
	assert.Equal(t, int32(0), b.Load())
}

// TestLazyMap_MapArgumentLimitation pins the documented limitation: passing
// the lazy map itself where cel-go needs a full map is an error, not a panic.
func TestLazyMap_MapArgumentLimitation(t *testing.T) {
	var a atomic.Int32
	root := LazyMap{"a": counted(&a, 1)}
	for _, expr := range []string{
		`_.exists(k, v, v == 1)`,
		`_.transformMap(k, v, v) == {}`,
	} {
		t.Run(expr, func(t *testing.T) {
			prog, err := Expression(expr).Compile(lazyRootEnv(ext.TwoVarComprehensions()))
			require.NoError(t, err)
			_, err = prog.EvalBool(context.Background(), map[string]any{"_": root})
			require.Error(t, err)
		})
	}
}

func TestLazyMap_NoConversion(t *testing.T) {
	var a atomic.Int32
	root := LazyMap{"a": counted(&a, 1)}
	ev := &lazyEval{adapter: types.DefaultTypeAdapter}
	mv := ev.bind(root).(*lazyMapVal)

	_, err := mv.ConvertToNative(reflect.TypeFor[map[string]any]())
	require.Error(t, err)
	assert.True(t, types.IsError(mv.ConvertToType(types.StringType)))
	assert.Equal(t, mv, mv.ConvertToType(types.MapType))
	assert.Equal(t, types.MapType, mv.ConvertToType(types.TypeType))
	assert.Equal(t, types.False, mv.Equal(types.NewStringInterfaceMap(types.DefaultTypeAdapter, map[string]any{"a": 1})))

	// Returning the whole map from an expression errors without resolving it.
	prog, err := Expression(`_`).CompileWithVarDecls(lazyRootDecls())
	require.NoError(t, err)
	_, err = prog.EvalBool(context.Background(), map[string]any{"_": root})
	require.Error(t, err)
	assert.Equal(t, int32(0), a.Load(), "no callbacks run")
}

func TestLazyMap_ConditionEvaluate(t *testing.T) {
	var a, b atomic.Int32
	root := LazyMap{"a": counted(&a, "prod"), "b": counted(&b, 1)}
	c := &Condition{Expr: exprPtr(`_.a == "prod" || _.b == 1`)}
	got, err := c.Evaluate(context.Background(), root)
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, int32(1), a.Load())
	assert.Equal(t, int32(0), b.Load())

	got, err = c.EvaluateWithSelf(context.Background(), root, LazyMap{"x": counted(&b, 1)})
	require.NoError(t, err)
	assert.True(t, got)

	got, err = (&Condition{Expr: exprPtr(`size(_) == 2 && has(_.b) && __self.x == 1`)}).
		EvaluateWithSelf(context.Background(), root, LazyMap{"x": counted(&b, 1)})
	require.NoError(t, err, "dyn-declared root dispatches size/has at runtime")
	assert.True(t, got)
}
