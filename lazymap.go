// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// LazyMap is a map whose values are computed only when a CEL expression
// reads them. Each value may be a func() (any, error), a func() any, or a
// plain value used as-is. Declare the variable as map(string, dyn), e.g.
// NewVarDecl("_", cel.MapType(cel.StringType, cel.DynType)).
//
// cel-go only defers top-level variables, so a plain map of lazy funcs is
// resolved as a whole as soon as any key is read. A LazyMap, used as a vars
// value passed to EvalBool or the Condition Evaluate* helpers (top-level, or
// nested inside another LazyMap or a map[string]any value), instead resolves
// each entry on first access:
//
//   - _.a / _["a"] runs only a's func, never b's; an entry never read never runs.
//   - has(_.a), 'a' in _, and size(_) never run a func.
//   - A func's error fails the evaluation with an error wrapping the original.
//   - Resolved values are adapted like eager vars (maps, lists, nested
//     LazyMaps).
//
// Iterating the map with a single-variable macro (all/exists/exists_one/map/
// filter) resolves each entry as it is visited; a macro that stops early
// (all/exists) leaves later entries unresolved. Equality is pointer identity of the
// per-evaluation view (as in Kubernetes), so == never resolves entries and a
// LazyMap never equals a map literal. Converting the whole map to a native Go
// value is disallowed (an error, no funcs run), so returning or converting
// the map never forces every entry.
//
// Limitation: the view is not a full CEL map (see lazyMapVal), so passing the
// LazyMap itself to a function with a map parameter (e.g. the map.*
// extension functions, string format), two-variable comprehensions, and
// transformMap fail with an evaluation error. Pass its entries (_.a) or an
// ordinary map instead.
//
// A LazyMap holds no state: every evaluation wraps it in a fresh resolver
// that memoizes each entry for that evaluation only, so one LazyMap can be
// reused across evaluations and goroutines without leaking an earlier
// result. Its funcs must be safe to call concurrently if evaluations run
// concurrently.
//
// Entries may also be func() ref.Val; an error value it returns fails the
// evaluation like a func() (any, error) error. The design follows Kubernetes'
// lazy.MapValue (k8s.io/apiserver/pkg/cel/lazy),
// with stricter defaults: presence checks do not resolve, memoization is per
// evaluation, resolution is concurrency-safe, and errors are wrapped.
type LazyMap map[string]any

// lazyEval is the per-evaluation state shared by every binding of one
// evaluation: the type adapter and the first fact error.
type lazyEval struct {
	adapter types.Adapter

	mu    sync.Mutex
	err   error
	views map[uintptr]*lazyMapVal // one resolver per LazyMap identity
}

// lazyView returns the evaluation's resolver for m, shared by every alias of
// the same LazyMap so each entry runs at most once per evaluation.
func (ev *lazyEval) lazyView(m LazyMap) *lazyMapVal {
	p := reflect.ValueOf(m).Pointer()
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if v, ok := ev.views[p]; ok {
		return v
	}
	if ev.views == nil {
		ev.views = map[uintptr]*lazyMapVal{}
	}
	v := &lazyMapVal{spec: m, ev: ev, done: make(map[string]ref.Val, len(m))}
	ev.views[p] = v
	return v
}

// bindVars returns a shallow copy of vars for one evaluation, so cel-go's
// memoization of lazy values never writes into the caller's map. Each
// func() (any, error) becomes a lazy func() any that records its error, and
// every LazyMap (at any map depth) becomes a fresh per-evaluation resolver.
func bindVars(vars map[string]any, adapter types.Adapter) (map[string]any, *lazyEval) {
	if adapter == nil {
		adapter = types.DefaultTypeAdapter
	}
	ev := &lazyEval{adapter: adapter}
	act := make(map[string]any, len(vars))
	for name, v := range vars {
		switch f := v.(type) {
		case func() (any, error):
			act[name] = func() any { return ev.call(fmt.Sprintf("variable %q", name), f) }
		case func() ref.Val:
			act[name] = func() any { return ev.call(fmt.Sprintf("variable %q", name), refValFact(f)) }
		case func() any:
			act[name] = func() any { return ev.bind(f()) }
		default:
			act[name] = ev.bind(v)
		}
	}
	return act, ev
}

// call runs a fallible fact, recording its error (prefixed with label) as
// the evaluation's error.
func (ev *lazyEval) call(label string, f func() (any, error)) any {
	val, err := f()
	if err != nil {
		ev.mu.Lock()
		if ev.err == nil {
			ev.err = fmt.Errorf("%s: %w", label, err)
		}
		ev.mu.Unlock()
		return types.WrapErr(err)
	}
	return ev.bind(val)
}

// refValFact adapts a func() ref.Val fact so an error value it returns is
// recorded like a func() (any, error) error instead of being absorbable by
// CEL's commutative ||/&&.
func refValFact(f func() ref.Val) func() (any, error) {
	return func() (any, error) {
		v := f()
		if e, ok := v.(*types.Err); ok {
			return nil, e.Unwrap()
		}
		return v, nil
	}
}

// factErr returns the first fact error of the evaluation, if any.
func (ev *lazyEval) factErr() error {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	return ev.err
}

// bind replaces LazyMaps in v with per-evaluation resolvers, copying any
// map[string]any on the path to one rather than modifying it.
func (ev *lazyEval) bind(v any) any {
	out, _ := ev.bindChanged(v, nil)
	return out
}

// bindChanged rewrites v. seen maps each walked map to its copy, so a
// self-referential (or shared) map resolves to the same copy instead of
// recursing forever, and back-edges point at the rebound copy.
func (ev *lazyEval) bindChanged(v any, seen map[uintptr]*mapCopy) (any, bool) {
	switch t := v.(type) {
	case LazyMap:
		return ev.lazyView(t), true
	case map[string]any:
		// ponytail: walks (and copies) every nested map[string]any per
		// evaluation; fine for fact maps, revisit for very large eager trees.
		p := reflect.ValueOf(t).Pointer()
		if s, ok := seen[p]; ok {
			s.used = true
			return s.cp, true
		}
		if seen == nil {
			seen = map[uintptr]*mapCopy{}
		}
		s := &mapCopy{cp: make(map[string]any, len(t))}
		seen[p] = s
		changed := false
		for k, e := range t {
			ne, c := ev.bindChanged(e, seen)
			s.cp[k] = ne
			changed = changed || c
		}
		if changed || s.used {
			return s.cp, true
		}
	}
	return v, false
}

// mapCopy is the per-evaluation copy of a walked map; used records whether
// a cycle or shared reference already points at it.
type mapCopy struct {
	cp   map[string]any
	used bool
}

// lazyMapVal is the per-evaluation CEL view of a LazyMap. Unlike Kubernetes'
// MapValue it deliberately does not implement traits.Mapper: cel-go answers
// has() on a Mapper by calling Find, which would resolve the entry, whereas on
// an Indexer it asks FieldTester.IsSet.
type lazyMapVal struct {
	spec LazyMap
	ev   *lazyEval

	// ponytail: one lock per map, held while a func runs (at-most-once per
	// evaluation); per-key locks if slow facts are read concurrently.
	mu   sync.Mutex
	done map[string]ref.Val
}

var (
	_ ref.Val            = (*lazyMapVal)(nil)
	_ traits.Indexer     = (*lazyMapVal)(nil)
	_ traits.FieldTester = (*lazyMapVal)(nil)
	_ traits.Container   = (*lazyMapVal)(nil)
	_ traits.Sizer       = (*lazyMapVal)(nil)
	_ traits.Iterable    = (*lazyMapVal)(nil)
)

// resolve returns the adapted value for key, computing it at most once.
func (m *lazyMapVal) resolve(key string) ref.Val {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.done[key]; ok {
		return v
	}
	raw, ok := m.spec[key]
	if !ok {
		return types.NewErr("no such key: %s", key)
	}
	switch f := raw.(type) {
	case func() (any, error):
		raw = m.ev.call(fmt.Sprintf("lazy map key %q", key), f)
	case func() any:
		raw = m.ev.bind(f())
	case func() ref.Val:
		raw = m.ev.call(fmt.Sprintf("lazy map key %q", key), refValFact(f))
	default:
		raw = m.ev.bind(raw)
	}
	v := m.ev.adapter.NativeToValue(raw)
	m.done[key] = v
	return v
}

func lazyKey(k ref.Val) (string, ref.Val) {
	s, ok := k.(types.String)
	if !ok {
		return "", types.MaybeNoSuchOverloadErr(k)
	}
	return string(s), nil
}

// Get implements traits.Indexer; it resolves only the requested entry.
func (m *lazyMapVal) Get(index ref.Val) ref.Val {
	k, errVal := lazyKey(index)
	if errVal != nil {
		return errVal
	}
	return m.resolve(k)
}

// IsSet implements traits.FieldTester (has()) without resolving the entry.
func (m *lazyMapVal) IsSet(field ref.Val) ref.Val {
	k, errVal := lazyKey(field)
	if errVal != nil {
		return errVal
	}
	_, ok := m.spec[k]
	return types.Bool(ok)
}

// Contains implements traits.Container ('key' in map) without resolving.
func (m *lazyMapVal) Contains(index ref.Val) ref.Val {
	s, ok := index.(types.String)
	if !ok {
		return types.False
	}
	_, found := m.spec[string(s)]
	return types.Bool(found)
}

// Size implements traits.Sizer without resolving.
func (m *lazyMapVal) Size() ref.Val { return types.Int(len(m.spec)) }

// Iterator implements traits.Iterable. It yields the keys in sorted order and
// resolves each entry as it is visited (like Kubernetes' MapValue), so a fact
// error surfaces during iteration.
func (m *lazyMapVal) Iterator() traits.Iterator {
	keys := make([]string, 0, len(m.spec))
	for k := range m.spec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return &lazyMapIter{m: m, keys: keys}
}

// ConvertToNative is disallowed: it would force every entry.
func (m *lazyMapVal) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, fmt.Errorf("disallowed conversion from lazy map to %v", typeDesc)
}

// ConvertToType allows only its own type and TypeType.
func (m *lazyMapVal) ConvertToType(typeVal ref.Type) ref.Val {
	switch typeVal {
	case types.MapType:
		return m
	case types.TypeType:
		return types.MapType
	}
	return types.NewErr("disallowed conversion from lazy map to %q", typeVal.TypeName())
}

// Equal is pointer identity, so == never resolves entries; a lazy map never
// equals any other value.
func (m *lazyMapVal) Equal(other ref.Val) ref.Val {
	if types.IsUnknownOrError(other) {
		return other
	}
	o, ok := other.(*lazyMapVal)
	return types.Bool(ok && m == o)
}

// Type implements ref.Val.
func (m *lazyMapVal) Type() ref.Type { return types.MapType }

// Value is not available: the map is never materialized.
func (m *lazyMapVal) Value() any { return types.NoSuchOverloadErr() }

// lazyMapIter iterates a lazyMapVal's keys, resolving each visited entry.
type lazyMapIter struct {
	m    *lazyMapVal
	keys []string
	i    int
}

var _ traits.Iterator = (*lazyMapIter)(nil)

func (it *lazyMapIter) HasNext() ref.Val { return types.Bool(it.i < len(it.keys)) }

func (it *lazyMapIter) Next() ref.Val {
	if it.i >= len(it.keys) {
		return types.NewErr("no more elements")
	}
	k := it.keys[it.i]
	it.i++
	if v := it.m.resolve(k); types.IsError(v) {
		return v
	}
	return types.String(k)
}

func (it *lazyMapIter) ConvertToNative(typeDesc reflect.Type) (any, error) {
	return nil, fmt.Errorf("disallowed conversion from iterator to %v", typeDesc)
}

func (it *lazyMapIter) ConvertToType(typeVal ref.Type) ref.Val {
	return types.NewErr("disallowed conversion from iterator to %q", typeVal.TypeName())
}

func (it *lazyMapIter) Equal(other ref.Val) ref.Val {
	o, ok := other.(*lazyMapIter)
	if !ok {
		return types.MaybeNoSuchOverloadErr(other)
	}
	return types.Bool(it == o)
}

func (it *lazyMapIter) Type() ref.Type { return types.IteratorType }

func (it *lazyMapIter) Value() any { return nil }
