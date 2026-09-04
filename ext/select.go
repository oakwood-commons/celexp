// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package ext

import (
	"errors"
	"fmt"
	"sort"

	"github.com/google/cel-go/cel"
	"github.com/oakwood-commons/celexp"
)

// ErrUnknownNamespace is returned by Select and SelectOptions when a requested
// namespace does not match any ExtFunction.Namespace known to All(). A typo in
// a caller's allowlist must fail loudly rather than silently narrow (or
// silently fail to narrow) the resulting environment.
var ErrUnknownNamespace = errors.New("unknown extension namespace")

// Namespaces returns the sorted, de-duplicated set of all namespaces present
// in All() (both BuiltIn() and Custom() extension functions). Use this to
// discover valid inputs for Select/SelectOptions, or to validate a
// configuration-supplied allowlist before it reaches NewRestricted.
//
// Example usage:
//
//	names := ext.Namespaces()
//	// names == []string{"arrays", "astValidators"... "url"}
func Namespaces() []string {
	seen := make(map[string]struct{})
	for _, f := range All() {
		if f.Namespace == "" {
			continue
		}
		seen[f.Namespace] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Select returns the subset of All() whose Namespace matches one of the given
// names, in the order namespaces are declared within All() (BuiltIn() then
// Custom()). Every requested name must exist in Namespaces(); otherwise Select
// returns ErrUnknownNamespace naming the first offending entry, and no
// partial/best-effort result. Duplicate names in the input are harmless.
//
// Example usage:
//
//	funcs, err := ext.Select("strings", "regex", "lists")
func Select(names ...string) (celexp.ExtFunctionList, error) {
	all := All()

	valid := make(map[string]struct{}, len(all))
	for _, f := range all {
		if f.Namespace != "" {
			valid[f.Namespace] = struct{}{}
		}
	}

	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		if _, ok := valid[n]; !ok {
			return nil, fmt.Errorf("%w: %q (known namespaces: %v)", ErrUnknownNamespace, n, Namespaces())
		}
		want[n] = struct{}{}
	}

	selected := make(celexp.ExtFunctionList, 0, len(all))
	for _, f := range all {
		if _, ok := want[f.Namespace]; ok {
			selected = append(selected, f)
		}
	}
	return selected, nil
}

// SelectOptions is a convenience wrapper around Select that flattens the
// matched ExtFunction.EnvOptions into a single slice, ready to pass to
// cel.NewEnv or env.NewRestricted.
//
// Example usage:
//
//	opts, err := ext.SelectOptions("strings", "regex")
//	if err != nil {
//	    return err
//	}
//	e, err := cel.NewEnv(opts...)
func SelectOptions(names ...string) ([]cel.EnvOption, error) {
	funcs, err := Select(names...)
	if err != nil {
		return nil, err
	}
	opts := make([]cel.EnvOption, 0, len(funcs)*2)
	for _, f := range funcs {
		opts = append(opts, f.EnvOptions...)
	}
	return opts, nil
}
