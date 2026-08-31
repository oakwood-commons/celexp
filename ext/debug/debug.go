// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package debug

import (
	"reflect"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/oakwood-commons/celexp"
)

// Sink receives output from the debug.out CEL function. It is the seam that
// decouples the CEL library from any concrete writer: scafctl's *writer.Writer
// already has this exact method, so it satisfies Sink with no changes, and
// any other embedder can provide its own implementation.
type Sink interface {
	DebugOutf(format string, args ...any)
}

// sinkUsable reports whether the sink can be written to. It guards against the
// typed-nil interface hazard: a nil concrete pointer (e.g. (*writer.Writer)(nil))
// boxed into a Sink is a non-nil interface value, so a plain `sink != nil`
// check would pass and then panic on the method call. Callers should normalize
// nil pointers to a nil interface, but this is belt-and-braces.
func sinkUsable(sink Sink) bool {
	if sink == nil {
		return false
	}
	v := reflect.ValueOf(sink)
	//nolint:exhaustive // only the nilable kinds need an IsNil guard; default covers the rest
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return !v.IsNil()
	default:
		return true
	}
}

//nolint:revive // DebugOutFunc is descriptive and matches the pattern of other Func names in the codebase
func DebugOutFunc(sink Sink) celexp.ExtFunction {
	funcName := "debug.out"

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "debug.out(dyn) -> null | debug.out(dyn, dyn) -> dyn",
		Description:   "Outputs a debug message to the console. Use debug.out(message) to print a message (returns null), or debug.out(message, value) to print a message and return a value for inline debugging",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Print a debug message (returns null)",
				Expression:  `debug.out("Checkpoint reached")`,
			},
			{
				Description: "Debug while returning a value for inline use",
				Expression:  `debug.out("Processing item", item.name)`,
			},
			{
				Description: "Debug in a list operation with passthrough",
				Expression:  `items.map(x, debug.out("item", x))`,
			},
		},
		EnvOptions: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(strings.ReplaceAll(funcName, ".", "_"),
					[]*cel.Type{cel.AnyType},
					cel.NullType,
					cel.FunctionBinding(func(args ...ref.Val) ref.Val {
						if sinkUsable(sink) {
							sink.DebugOutf("CEL DEBUG OUTPUT: %v", args[0].Value())
						}
						// Single argument version returns null (side effect only)
						return types.NullValue
					},
					),
				),
				cel.Overload(strings.ReplaceAll(funcName, ".", "_")+"_with_value",
					[]*cel.Type{cel.AnyType, cel.AnyType},
					cel.AnyType,
					cel.FunctionBinding(func(args ...ref.Val) ref.Val {
						if sinkUsable(sink) {
							sink.DebugOutf("CEL DEBUG OUTPUT: %v", args[0].Value())
						}
						// Return the value (second argument) for two argument version
						return args[1]
					},
					),
				),
			),
		},
	}
}

//nolint:revive // DebugThrowFunc is descriptive and matches the pattern of other Func names in the codebase
func DebugThrowFunc() celexp.ExtFunction {
	funcName := "debug.throw"

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "debug.throw(dyn) -> dyn",
		Description:   "Throws an error with the provided message, immediately halting CEL expression evaluation. Use debug.throw(message) to stop execution and return an error with the specified message",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Throw an error unconditionally",
				Expression:  `debug.throw("Configuration is invalid")`,
			},
			{
				Description: "Throw an error conditionally",
				Expression:  `value < 0 ? debug.throw("Value must be positive") : value * 2`,
			},
		},
		EnvOptions: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(strings.ReplaceAll(funcName, ".", "_"),
					[]*cel.Type{cel.AnyType},
					cel.AnyType,
					cel.FunctionBinding(func(args ...ref.Val) ref.Val {
						return types.NewErr("%v", args[0].Value())
					},
					),
				),
			),
		},
	}
}

//nolint:revive // DebugSleepFunc is descriptive and matches the pattern of other Func names in the codebase
func DebugSleepFunc() celexp.ExtFunction {
	funcName := "debug.sleep"

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "debug.sleep(int) -> int | debug.sleep(int, dyn) -> dyn",
		Description:   "Pauses execution for the specified duration in milliseconds and returns the value for inline debugging. Use debug.sleep(duration) to sleep and return the duration value, or debug.sleep(duration, value) to sleep and return a different value",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Sleep for 1 second (1000ms)",
				Expression:  `debug.sleep(1000)`,
			},
			{
				Description: "Sleep and return a specific value",
				Expression:  `debug.sleep(500, "Ready")`,
			},
			{
				Description: "Use in expression for timing",
				Expression:  `debug.sleep(100) + 5`,
			},
		},
		EnvOptions: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(strings.ReplaceAll(funcName, ".", "_"),
					[]*cel.Type{cel.IntType},
					cel.IntType,
					cel.FunctionBinding(func(args ...ref.Val) ref.Val {
						s, ok := args[0].Value().(int64)
						if !ok {
							return types.NewErr("failed to convert object to int64. The type is %T", args[0].Value())
						}
						if s < 0 {
							s = 0
						}
						time.Sleep(time.Duration(s) * time.Millisecond)
						return args[0]
					},
					),
				),

				cel.Overload(strings.ReplaceAll(funcName, ".", "_")+"_with_value",
					[]*cel.Type{cel.IntType, cel.AnyType},
					cel.AnyType,
					cel.FunctionBinding(func(args ...ref.Val) ref.Val {
						s, ok := args[0].Value().(int64)
						if !ok {
							return types.NewErr("failed to convert object to int64. The type is %T", args[0].Value())
						}
						if s < 0 {
							s = 0
						}
						time.Sleep(time.Duration(s) * time.Millisecond)
						return args[1]
					},
					),
				),
			),
		},
	}
}
