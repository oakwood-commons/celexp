// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package marshalling

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/conversion"
	"gopkg.in/yaml.v3"
)

const (
	// maxFrontmatterNestingDepth bounds recursive container traversal when parsing
	// or formatting frontmatter metadata so cyclic or deeply nested structures
	// return an error rather than exhausting the call stack.
	maxFrontmatterNestingDepth = 64

	// maxFrontmatterVisitedNodes bounds total container and element visits when
	// normalizing, validating, or formatting frontmatter metadata so shared
	// acyclic graphs cannot expand exponentially in CPU or memory.
	maxFrontmatterVisitedNodes = 100_000

	// maxFrontmatterYAMLBytes bounds serialized YAML frontmatter output so
	// repeated shared values or large scalars cannot exhaust memory before CEL
	// runtime cost tracking runs.
	maxFrontmatterYAMLBytes = 4 << 20
)

var (
	// ErrFrontmatterNotMapping is returned by ParseFrontmatter when the YAML block
	// inside --- delimiters is a scalar or sequence rather than a mapping.
	ErrFrontmatterNotMapping = errors.New("frontmatter must be a YAML mapping")

	// ErrFrontmatterCyclicValue is returned when frontmatter metadata contains a
	// self-referential map, slice, or pointer cycle.
	ErrFrontmatterCyclicValue = errors.New("frontmatter metadata contains a cyclic value")

	// ErrFrontmatterMaxDepth is returned when frontmatter metadata exceeds
	// maxFrontmatterNestingDepth nested containers.
	ErrFrontmatterMaxDepth = errors.New("frontmatter metadata exceeds maximum nesting depth")

	// ErrFrontmatterBudgetExceeded is returned when frontmatter metadata exceeds
	// maxFrontmatterVisitedNodes or maxFrontmatterYAMLBytes during traversal or
	// serialization.
	ErrFrontmatterBudgetExceeded = errors.New("frontmatter metadata exceeds traversal or output size budget")

	// ErrFrontmatterDuplicateKey is returned when distinct native map keys
	// normalize to the same YAML mapping key.
	ErrFrontmatterDuplicateKey = errors.New("frontmatter metadata contains duplicate normalized map key")

	// ErrFrontmatterTopLevelKeyType is returned when a top-level YAML frontmatter
	// mapping key (or a key merged into top-level via <<) is not a string.
	ErrFrontmatterTopLevelKeyType = errors.New("frontmatter top-level keys must be strings")
)

// ParseFrontmatter extracts an optional leading YAML frontmatter block fenced
// by --- (closed by --- or ...) from doc using a single O(n) line scan,
// supporting an optional leading UTF-8 BOM and both LF and CRLF line endings.
//
// When doc does not begin with a valid opening fence and matching closing
// fence, ParseFrontmatter returns hasFrontmatter=false, an empty meta map, and
// doc unchanged as body without returning an error.
func ParseFrontmatter(doc string) (hasFrontmatter bool, meta map[string]any, body string, err error) {
	hasFM, parsedMeta, parsedBody, _, _, parseErr := parseFrontmatterWithWork(doc)
	return hasFM, parsedMeta, parsedBody, parseErr
}

func parseFrontmatterWithWork(doc string) (hasFrontmatter bool, meta map[string]any, body string, visitedNodes, visitedBytes uint64, err error) {
	trimmedDoc := strings.TrimPrefix(doc, utf8BOM)

	firstLine, afterFirstNewline, hasNewline := cutLine(trimmedDoc)
	if !hasNewline || !isFenceMarker(firstLine, "---") {
		return false, map[string]any{}, doc, 0, 0, nil
	}

	// Scan line by line for the closing --- or ... fence.
	offset := 0
	rem := afterFirstNewline
	for {
		line, nextRem, foundNewline := cutLine(rem)
		if isFenceMarker(line, "---") || isFenceMarker(line, "...") {
			yamlBlock := afterFirstNewline[:offset]
			parsedMeta, nodes, bytesVisited, parseErr := decodeFrontmatterYAML(yamlBlock)
			if parseErr != nil {
				return false, nil, "", nodes, bytesVisited, parseErr
			}
			return true, parsedMeta, nextRem, nodes, bytesVisited, nil
		}
		if !foundNewline {
			// Reached EOF without a closing fence.
			return false, map[string]any{}, doc, 0, 0, nil
		}
		offset += len(rem) - len(nextRem)
		rem = nextRem
	}
}

// cutLine splits s at the first '\n', stripping a preceding '\r' from line if
// present. When no '\n' is present, cutLine returns (s, "", false) with a
// trailing '\r' stripped from line for CR-before-EOF robustness.
func cutLine(s string) (line, rest string, hasNewline bool) {
	idx := strings.IndexByte(s, '\n')
	if idx < 0 {
		return strings.TrimSuffix(s, "\r"), "", false
	}
	line = strings.TrimSuffix(s[:idx], "\r")
	return line, s[idx+1:], true
}

// isFenceMarker reports whether line starts with marker at column 0 followed
// only by optional horizontal whitespace (' ' or '\t').
func isFenceMarker(line, marker string) bool {
	if !strings.HasPrefix(line, marker) {
		return false
	}
	trailing := line[len(marker):]
	for i := 0; i < len(trailing); i++ {
		if trailing[i] != ' ' && trailing[i] != '\t' {
			return false
		}
	}
	return true
}

type containerKey struct {
	kind reflect.Kind
	ptr  uintptr
	len  int
}

func containerKeyOf(raw any) (containerKey, bool) {
	if raw == nil {
		return containerKey{}, false
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() { //nolint:exhaustive // Only reference kinds that can form cycles are tracked.
	case reflect.Map:
		if rv.IsNil() || rv.Len() == 0 {
			return containerKey{}, false
		}
		return containerKey{kind: reflect.Map, ptr: rv.Pointer()}, true
	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return containerKey{}, false
		}
		return containerKey{kind: reflect.Slice, ptr: rv.Pointer(), len: rv.Len()}, true
	case reflect.Pointer:
		if rv.IsNil() {
			return containerKey{}, false
		}
		return containerKey{kind: reflect.Pointer, ptr: rv.Pointer()}, true
	default:
		return containerKey{}, false
	}
}

func refValContainerKey(val ref.Val) (containerKey, bool) {
	if val == nil {
		return containerKey{}, false
	}
	if ck, ok := containerKeyOf(val.Value()); ok {
		return ck, true
	}
	return containerKeyOf(val)
}

func canContainContainers(t reflect.Type) bool {
	switch t.Kind() { //nolint:exhaustive // Scalar kinds cannot hold nested containers.
	case reflect.Interface, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Struct:
		return true
	default:
		return false
	}
}

type frontmatterBudget struct {
	nodes int
	bytes int
}

func (b *frontmatterBudget) chargeNodes(n int) error {
	if n <= 0 {
		return nil
	}
	if n > maxFrontmatterVisitedNodes-b.nodes {
		b.nodes = maxFrontmatterVisitedNodes + 1
		return ErrFrontmatterBudgetExceeded
	}
	b.nodes += n
	return nil
}

func (b *frontmatterBudget) chargeBytes(n int) error {
	if n <= 0 {
		return nil
	}
	if n > maxFrontmatterYAMLBytes-b.bytes {
		b.bytes = maxFrontmatterYAMLBytes + 1
		return ErrFrontmatterBudgetExceeded
	}
	b.bytes += n
	return nil
}

type limitedYAMLBuffer struct {
	buf      bytes.Buffer
	maxBytes int
	exceeded bool
}

func (b *limitedYAMLBuffer) Write(p []byte) (int, error) {
	if len(p) > b.maxBytes-b.buf.Len() {
		b.exceeded = true
		return 0, ErrFrontmatterBudgetExceeded
	}
	return b.buf.Write(p)
}

func validateYAMLValue(rv reflect.Value, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) error {
	for rv.IsValid() && rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	if err := budget.chargeNodes(1); err != nil {
		return err
	}

	switch rv.Kind() { //nolint:exhaustive // Other scalar kinds have bounded fixed size and no children.
	case reflect.String:
		return budget.chargeBytes(rv.Len())

	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		ck := containerKey{kind: reflect.Pointer, ptr: rv.Pointer()}
		if _, seen := active[ck]; seen {
			return ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
		return validateYAMLValue(rv.Elem(), depth+1, active, budget)

	case reflect.Map:
		if rv.IsNil() || rv.Len() == 0 {
			return nil
		}
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		ck := containerKey{kind: reflect.Map, ptr: rv.Pointer()}
		if _, seen := active[ck]; seen {
			return ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
		if err := budget.chargeNodes(rv.Len()); err != nil {
			return err
		}
		keyType := rv.Type().Key()
		elemType := rv.Type().Elem()
		keyKind := keyType.Kind()
		switch keyKind { //nolint:exhaustive // Non-listed kinds (arrays, structs, pointers, funcs) are invalid YAML map keys.
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64, reflect.Interface:
		default:
			return fmt.Errorf("unsupported nested map key type %s", keyType)
		}
		checkInterfaceKeys := keyKind == reflect.Interface
		checkFloatKeys := keyKind == reflect.Float32 || keyKind == reflect.Float64
		hasKeyWalk := canContainContainers(keyType) || keyKind == reflect.String
		hasValWalk := canContainContainers(elemType) || elemType.Kind() == reflect.String
		if !checkInterfaceKeys && !checkFloatKeys && !hasKeyWalk && !hasValWalk {
			return nil
		}
		var seenKeys map[any]struct{}
		if checkInterfaceKeys {
			seenKeys = make(map[any]struct{}, rv.Len())
		}
		iter := rv.MapRange()
		for iter.Next() {
			if checkFloatKeys && math.IsNaN(iter.Key().Float()) {
				return errors.New("map key cannot be NaN")
			}
			if checkInterfaceKeys {
				rawKey := iter.Key().Interface()
				nk := normalizeYAMLKey(rawKey)
				if err := validateNormalizedNestedKey(rawKey, nk); err != nil {
					return err
				}
				if _, dup := seenKeys[nk]; dup {
					return fmt.Errorf("%w: %v", ErrFrontmatterDuplicateKey, nk)
				}
				seenKeys[nk] = struct{}{}
			}
			if hasKeyWalk {
				if err := validateYAMLValue(iter.Key(), depth+1, active, budget); err != nil {
					return err
				}
			}
			if hasValWalk {
				if err := validateYAMLValue(iter.Value(), depth+1, active, budget); err != nil {
					return err
				}
			}
		}
		return nil

	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return nil
		}
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		ck := containerKey{kind: reflect.Slice, ptr: rv.Pointer(), len: rv.Len()}
		if _, seen := active[ck]; seen {
			return ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
		if err := budget.chargeNodes(rv.Len()); err != nil {
			return err
		}
		elemType := rv.Type().Elem()
		if !canContainContainers(elemType) && elemType.Kind() != reflect.String {
			return nil
		}
		for i := 0; i < rv.Len(); i++ {
			if err := validateYAMLValue(rv.Index(i), depth+1, active, budget); err != nil {
				return err
			}
		}
		return nil

	case reflect.Array:
		if rv.Len() == 0 {
			return nil
		}
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		if err := budget.chargeNodes(rv.Len()); err != nil {
			return err
		}
		elemType := rv.Type().Elem()
		if !canContainContainers(elemType) && elemType.Kind() != reflect.String {
			return nil
		}
		for i := 0; i < rv.Len(); i++ {
			if err := validateYAMLValue(rv.Index(i), depth+1, active, budget); err != nil {
				return err
			}
		}
		return nil

	case reflect.Struct:
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() || (!canContainContainers(sf.Type) && sf.Type.Kind() != reflect.String) {
				continue
			}
			if err := validateYAMLValue(rv.Field(i), depth+1, active, budget); err != nil {
				return err
			}
		}
		return nil

	default:
		return nil
	}
}

func accumulateBudgets(a, b *frontmatterBudget) (nodes, visitedBytes uint64) {
	if a != nil {
		if a.nodes > 0 {
			nodes = safeAddUint64(nodes, uint64(a.nodes))
		}
		if a.bytes > 0 {
			visitedBytes = safeAddUint64(visitedBytes, uint64(a.bytes))
		}
	}
	if b != nil {
		if b.nodes > 0 {
			nodes = safeAddUint64(nodes, uint64(b.nodes))
		}
		if b.bytes > 0 {
			visitedBytes = safeAddUint64(visitedBytes, uint64(b.bytes))
		}
	}
	return nodes, visitedBytes
}

func decodeFrontmatterYAML(yamlBlock string) (meta map[string]any, visitedNodes, visitedBytes uint64, err error) {
	var docNode yaml.Node
	if err := yaml.Unmarshal([]byte(yamlBlock), &docNode); err != nil {
		return nil, 0, 0, err
	}
	if docNode.Kind == 0 || len(docNode.Content) == 0 {
		return map[string]any{}, 0, 0, nil
	}
	root := unwrapYAMLAlias(docNode.Content[0])
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, 1, 0, ErrFrontmatterNotMapping
	}

	var nodeBudget frontmatterBudget
	if err := validateYAMLNodeStructure(root, true, 0, make(map[*yaml.Node]struct{}, 8), &nodeBudget); err != nil {
		n, b := accumulateBudgets(&nodeBudget, nil)
		return nil, n, b, err
	}

	var rawMeta map[any]any
	if err := root.Decode(&rawMeta); err != nil {
		n, b := accumulateBudgets(&nodeBudget, nil)
		return nil, n, b, err
	}
	if len(rawMeta) == 0 {
		n, b := accumulateBudgets(&nodeBudget, nil)
		return map[string]any{}, n, b, nil
	}

	active := make(map[containerKey]struct{}, 8)
	var budget frontmatterBudget
	if err := budget.chargeNodes(len(rawMeta)); err != nil {
		n, b := accumulateBudgets(&nodeBudget, &budget)
		return nil, n, b, err
	}
	if ck, ok := containerKeyOf(rawMeta); ok {
		active[ck] = struct{}{}
		defer delete(active, ck)
	}
	meta = make(map[string]any, len(rawMeta))
	for rawKey, v := range rawMeta {
		k, ok := rawKey.(string)
		if !ok {
			n, b := accumulateBudgets(&nodeBudget, &budget)
			return nil, n, b, fmt.Errorf("%w: got %T", ErrFrontmatterTopLevelKeyType, rawKey)
		}
		if err := budget.chargeBytes(len(k)); err != nil {
			n, b := accumulateBudgets(&nodeBudget, &budget)
			return nil, n, b, err
		}
		normVal, normErr := normalizeYAMLValue(v, 1, active, &budget)
		if normErr != nil {
			n, b := accumulateBudgets(&nodeBudget, &budget)
			return nil, n, b, normErr
		}
		meta[k] = normVal
	}
	n, b := accumulateBudgets(&nodeBudget, &budget)
	return meta, n, b, nil
}

// validateYAMLNodeStructure inspects the parsed yaml.Node tree prior to
// yaml.Node.Decode so non-string top-level keys (including keys merged via <<)
// cannot be coerced or silently overwritten during YAML merge processing, and
// non-scalar nested mapping keys cannot trigger unhashable-key panics in yaml.v3.
func validateYAMLNodeStructure(node *yaml.Node, topLevel bool, depth int, active map[*yaml.Node]struct{}, budget *frontmatterBudget) error {
	for node != nil && node.Kind == yaml.AliasNode {
		if err := budget.chargeNodes(1); err != nil {
			return err
		}
		if _, seen := active[node]; seen {
			return ErrFrontmatterCyclicValue
		}
		active[node] = struct{}{}
		defer delete(active, node)
		node = node.Alias
	}
	if node == nil {
		return nil
	}
	if err := budget.chargeNodes(1); err != nil {
		return err
	}
	if depth > maxFrontmatterNestingDepth {
		return ErrFrontmatterMaxDepth
	}
	if _, seen := active[node]; seen {
		return ErrFrontmatterCyclicValue
	}
	active[node] = struct{}{}
	defer delete(active, node)

	switch node.Kind { //nolint:exhaustive // Empty/document kinds have no child nodes.
	case yaml.ScalarNode:
		return budget.chargeBytes(len(node.Value))
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if err := budget.chargeNodes(1); err != nil {
				return err
			}
			kNode := unwrapYAMLAlias(node.Content[i])
			vNode := node.Content[i+1]
			if kNode == nil {
				continue
			}
			if kNode.Kind == yaml.ScalarNode && kNode.ShortTag() == "!!merge" {
				if err := validateYAMLMergeTarget(vNode, topLevel, depth+1, active, budget); err != nil {
					return err
				}
				continue
			}
			if kNode.Kind != yaml.ScalarNode {
				if topLevel {
					return ErrFrontmatterTopLevelKeyType
				}
				return errors.New("unsupported non-scalar YAML mapping key")
			}
			if err := budget.chargeBytes(len(kNode.Value)); err != nil {
				return err
			}
			if topLevel && kNode.ShortTag() != "!!str" {
				return fmt.Errorf("%w: got %s", ErrFrontmatterTopLevelKeyType, kNode.ShortTag())
			}
			if err := validateYAMLNodeStructure(vNode, false, depth+1, active, budget); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, elem := range node.Content {
			if err := validateYAMLNodeStructure(elem, false, depth+1, active, budget); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateYAMLMergeTarget(vNode *yaml.Node, topLevel bool, depth int, active map[*yaml.Node]struct{}, budget *frontmatterBudget) error {
	target := unwrapYAMLAlias(vNode)
	if target == nil {
		return nil
	}
	switch target.Kind { //nolint:exhaustive // Other kinds are invalid merge targets handled by yaml.v3 Decode.
	case yaml.MappingNode:
		return validateYAMLNodeStructure(vNode, topLevel, depth, active, budget)
	case yaml.SequenceNode:
		if depth > maxFrontmatterNestingDepth {
			return ErrFrontmatterMaxDepth
		}
		if err := budget.chargeNodes(len(target.Content)); err != nil {
			return err
		}
		for _, item := range target.Content {
			if err := validateYAMLNodeStructure(item, topLevel, depth+1, active, budget); err != nil {
				return err
			}
		}
	}
	return nil
}

func unwrapYAMLAlias(n *yaml.Node) *yaml.Node {
	var seen map[*yaml.Node]struct{}
	for n != nil && n.Kind == yaml.AliasNode {
		if seen == nil {
			seen = make(map[*yaml.Node]struct{}, 4)
		}
		if _, dup := seen[n]; dup {
			return nil
		}
		seen[n] = struct{}{}
		n = n.Alias
	}
	return n
}

// normalizeYAMLValue widens Go integer and float32 map keys inside map[any]any
// values produced by yaml.v3 to CEL's canonical int64/uint64/float64 types so
// CEL map indexing and iteration match the underlying Go map keys, while
// rejecting cyclic, excessively nested, over-budget, or key-colliding values.
func normalizeYAMLValue(v any, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) (any, error) {
	if err := budget.chargeNodes(1); err != nil {
		return nil, err
	}
	switch val := v.(type) {
	case string:
		if err := budget.chargeBytes(len(val)); err != nil {
			return nil, err
		}
		return val, nil
	case map[string]any:
		if depth > maxFrontmatterNestingDepth {
			return nil, ErrFrontmatterMaxDepth
		}
		if ck, ok := containerKeyOf(val); ok {
			if _, seen := active[ck]; seen {
				return nil, ErrFrontmatterCyclicValue
			}
			active[ck] = struct{}{}
			defer delete(active, ck)
		}
		if err := budget.chargeNodes(len(val)); err != nil {
			return nil, err
		}
		for k, elem := range val {
			if err := budget.chargeBytes(len(k)); err != nil {
				return nil, err
			}
			normElem, err := normalizeYAMLValue(elem, depth+1, active, budget)
			if err != nil {
				return nil, err
			}
			val[k] = normElem
		}
		return val, nil
	case map[any]any:
		if depth > maxFrontmatterNestingDepth {
			return nil, ErrFrontmatterMaxDepth
		}
		if ck, ok := containerKeyOf(val); ok {
			if _, seen := active[ck]; seen {
				return nil, ErrFrontmatterCyclicValue
			}
			active[ck] = struct{}{}
			defer delete(active, ck)
		}
		if err := budget.chargeNodes(len(val)); err != nil {
			return nil, err
		}
		out := make(map[any]any, len(val))
		for k, elem := range val {
			if ks, isStr := k.(string); isStr {
				if err := budget.chargeBytes(len(ks)); err != nil {
					return nil, err
				}
			}
			normElem, err := normalizeYAMLValue(elem, depth+1, active, budget)
			if err != nil {
				return nil, err
			}
			nk := normalizeYAMLKey(k)
			if err := validateNormalizedNestedKey(k, nk); err != nil {
				return nil, err
			}
			if _, exists := out[nk]; exists {
				return nil, fmt.Errorf("%w: %v", ErrFrontmatterDuplicateKey, nk)
			}
			out[nk] = normElem
		}
		return out, nil
	case []any:
		if depth > maxFrontmatterNestingDepth {
			return nil, ErrFrontmatterMaxDepth
		}
		if ck, ok := containerKeyOf(val); ok {
			if _, seen := active[ck]; seen {
				return nil, ErrFrontmatterCyclicValue
			}
			active[ck] = struct{}{}
			defer delete(active, ck)
		}
		if err := budget.chargeNodes(len(val)); err != nil {
			return nil, err
		}
		for i, elem := range val {
			normElem, err := normalizeYAMLValue(elem, depth+1, active, budget)
			if err != nil {
				return nil, err
			}
			val[i] = normElem
		}
		return val, nil
	default:
		return v, nil
	}
}

func normalizeYAMLKey(k any) any {
	switch kv := k.(type) {
	case int:
		return int64(kv)
	case int8:
		return int64(kv)
	case int16:
		return int64(kv)
	case int32:
		return int64(kv)
	case uint:
		return uint64(kv)
	case uint8:
		return uint64(kv)
	case uint16:
		return uint64(kv)
	case uint32:
		return uint64(kv)
	case float32:
		return float64(kv)
	default:
		return k
	}
}

func validateNormalizedNestedKey(rawKey, nk any) error {
	if f, isFloat := nk.(float64); isFloat && math.IsNaN(f) {
		return errors.New("map key cannot be NaN")
	}
	switch nk.(type) {
	case nil, string, bool, int64, uint64, float64:
		return nil
	default:
		return fmt.Errorf("unsupported nested map key type %T", rawKey)
	}
}

// FormatFrontmatter marshals meta as YAML wrapped in --- fences followed by
// body. An empty meta map emits "---\n---\n" + body so ParseFrontmatter
// round-trips both empty and non-empty metadata maps without ambiguity.
func FormatFrontmatter(meta map[string]any, body string) (string, error) {
	if len(meta) == 0 {
		return "---\n---\n" + body, nil
	}
	active := make(map[containerKey]struct{}, 8)
	var budget frontmatterBudget
	if err := validateYAMLValue(reflect.ValueOf(meta), 0, active, &budget); err != nil {
		return "", err
	}
	return marshalFrontmatterYAML(meta, body)
}

func marshalFrontmatterYAML(meta map[string]any, body string) (string, error) {
	formatted, _, err := marshalFrontmatterYAMLWithBytes(meta, body)
	return formatted, err
}

func marshalFrontmatterYAMLWithBytes(meta map[string]any, body string) (string, uint64, error) {
	if len(meta) == 0 {
		return "---\n---\n" + body, 0, nil
	}
	limBuf := &limitedYAMLBuffer{maxBytes: maxFrontmatterYAMLBytes}
	enc := yaml.NewEncoder(limBuf)
	if err := enc.Encode(meta); err != nil {
		if limBuf.exceeded {
			return "", maxFrontmatterYAMLBytes, ErrFrontmatterBudgetExceeded
		}
		return "", uint64(max(0, limBuf.buf.Len())), err
	}
	if err := enc.Close(); err != nil {
		if limBuf.exceeded {
			return "", maxFrontmatterYAMLBytes, ErrFrontmatterBudgetExceeded
		}
		return "", uint64(max(0, limBuf.buf.Len())), err
	}
	yamlStr := limBuf.buf.String()
	if !strings.HasSuffix(yamlStr, "\n") {
		yamlStr += "\n"
	}
	return "---\n" + yamlStr + "---\n" + body, uint64(len(yamlStr)), nil
}

// trackedMapper wraps a CEL Mapper result with invocation-local traversal work
// counters so runtime cost tracking charges exact YAML work without reparsing
// or using shared mutable state.
type trackedMapper struct {
	traits.Mapper
	visitedNodes uint64
	visitedBytes uint64
}

func (m *trackedMapper) ConvertToType(typeVal ref.Type) ref.Val {
	if typeVal == types.MapType {
		return m
	}
	return m.Mapper.ConvertToType(typeVal)
}

func (m *trackedMapper) IsZeroValue() bool {
	if z, ok := m.Mapper.(traits.Zeroer); ok {
		return z.IsZeroValue()
	}
	return m.Size() == types.IntZero
}

func (m *trackedMapper) Fold(f traits.Folder) {
	if foldable, ok := m.Mapper.(traits.Foldable); ok {
		foldable.Fold(f)
		return
	}
	it := m.Iterator()
	for it.HasNext() == types.True {
		k := it.Next()
		v, _ := m.Find(k)
		if !f.FoldEntry(k.Value(), v.Value()) {
			return
		}
	}
}

func (m *trackedMapper) String() string {
	if s, ok := m.Mapper.(fmt.Stringer); ok {
		return s.String()
	}
	return types.Format(m.Mapper)
}

// trackedWorkError wraps a binding error inside a standard *types.Err with
// invocation-local traversal work counters for the runtime cost tracker.
type trackedWorkError struct {
	prefix       string
	cause        error
	visitedNodes uint64
	visitedBytes uint64
}

func (e *trackedWorkError) Error() string {
	return e.prefix + ": " + e.cause.Error()
}

func (e *trackedWorkError) Unwrap() error {
	return e.cause
}

func newTrackedWorkErr(prefix string, err error, visitedNodes, visitedBytes uint64) ref.Val {
	return types.WrapErr(&trackedWorkError{
		prefix:       prefix,
		cause:        err,
		visitedNodes: visitedNodes,
		visitedBytes: visitedBytes,
	})
}

func extractTrackedWork(val ref.Val) (visitedNodes, visitedBytes uint64, ok bool) {
	if val == nil {
		return 0, 0, false
	}
	if tm, isTM := val.(*trackedMapper); isTM {
		return tm.visitedNodes, tm.visitedBytes, true
	}
	if errVal, isErr := val.Value().(error); isErr {
		var wErr *trackedWorkError
		if errors.As(errVal, &wErr) {
			return wErr.visitedNodes, wErr.visitedBytes, true
		}
	}
	return 0, 0, false
}

// FrontmatterParseFunc returns a CEL function that splits a document into its
// optional YAML frontmatter metadata and Markdown/text body.
//
// Example usage:
//
//	frontmatter.parse("---\ntitle: Hello\n---\n# Heading")
//	// Returns: {"hasFrontmatter": true, "meta": {"title": "Hello"}, "body": "# Heading"}
func FrontmatterParseFunc() celexp.ExtFunction {
	const (
		funcName   = "frontmatter.parse"
		overloadID = "frontmatter_parse"
	)

	estOpt := cel.CostEstimatorOptions(
		checker.OverloadCostEstimate(overloadID, func(estimator checker.CostEstimator, _ *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
			if len(args) != 1 {
				return nil
			}
			sz := estimateArgSize(estimator, args[0])
			minCost := sz.MultiplyByCostFactor(2 * marshallingTraversalFactor).Add(checker.FixedCostEstimate(1))
			// YAML anchors, aliases, and merge keys can expand a short input up to
			// the runtime traversal budgets, so keep Max conservatively unbounded.
			cost := checker.CostEstimate{Min: minCost.Min, Max: math.MaxUint64}
			resSize := checker.FixedSizeEstimate(3)
			return &checker.CallEstimate{CostEstimate: cost, ResultSize: &resSize}
		}),
	)

	tracker := func(args []ref.Val, result ref.Val) *uint64 {
		if len(args) != 1 {
			return nil
		}
		docBytes := actualStringByteLen(args[0])
		baseCost := safeAddUint64(1, scaledByteCost(docBytes, 2*marshallingTraversalFactor))
		visitedNodes, visitedBytes, ok := extractTrackedWork(result)
		if !ok {
			visitedBytes = docBytes
		}
		traversalCost := safeAddUint64(visitedNodes, scaledByteCost(visitedBytes, marshallingTraversalFactor))
		cost := safeAddUint64(baseCost, traversalCost)
		return &cost
	}
	progOpt := cel.CostTrackerOptions(
		interpreter.OverloadCostTracker(overloadID, tracker),
	)

	lib := &funcLib{
		name: "cel.lib.ext.celexp.frontmatter.parse",
		compileOpts: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(overloadID,
					[]*cel.Type{cel.StringType},
					cel.MapType(cel.StringType, cel.DynType),
					cel.UnaryBinding(func(value ref.Val) ref.Val {
						doc, ok := value.Value().(string)
						if !ok {
							return newTrackedWorkErr(funcName, fmt.Errorf("expected string, got %T", value.Value()), 0, 0)
						}
						hasFM, meta, body, visitedNodes, visitedBytes, err := parseFrontmatterWithWork(doc)
						if err != nil {
							return newTrackedWorkErr(funcName, err, visitedNodes, visitedBytes)
						}
						celVal := conversion.GoToCelValue(map[string]any{
							"hasFrontmatter": hasFM,
							"meta":           meta,
							"body":           body,
						})
						if mapper, isMapper := celVal.(traits.Mapper); isMapper {
							return &trackedMapper{
								Mapper:       mapper,
								visitedNodes: visitedNodes,
								visitedBytes: visitedBytes,
							}
						}
						return celVal
					}),
				),
			),
			estOpt,
		},
		programOpts: []cel.ProgramOption{progOpt},
	}

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "frontmatter.parse(string) -> map<string, dyn>",
		Description:   "Parses optional leading YAML --- frontmatter and body text from a Markdown or text document across LF and CRLF inputs",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Parse YAML frontmatter and Markdown body",
				Expression:  `frontmatter.parse("---\ntitle: Guide\n---\n# Welcome")`,
			},
			{
				Description: "Document without frontmatter returns hasFrontmatter false",
				Expression:  `frontmatter.parse("# Plain Markdown").hasFrontmatter`,
			},
		},
		EnvOptions: []cel.EnvOption{cel.Lib(lib)},
	}
}

// FrontmatterFormatFunc returns a CEL function that formats a metadata map as
// YAML --- frontmatter prepended to a document body string.
//
// Example usage:
//
//	frontmatter.format({"title": "Guide"}, "# Welcome\n")
func FrontmatterFormatFunc() celexp.ExtFunction {
	const (
		funcName   = "frontmatter.format"
		overloadID = "frontmatter_format"
	)

	estOpt := cel.CostEstimatorOptions(
		checker.OverloadCostEstimate(overloadID, func(estimator checker.CostEstimator, _ *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
			if len(args) != 2 {
				return nil
			}
			metaSz := estimateArgSize(estimator, args[0])
			bodySz := estimateArgSize(estimator, args[1])
			minCost := metaSz.MultiplyByCostFactor(1.0).
				Add(bodySz.MultiplyByCostFactor(marshallingTraversalFactor)).
				Add(checker.FixedCostEstimate(1))
			// Static map size estimates count only top-level entries, not
			// nested metadata contents or serialized YAML byte length charged
			// at runtime, so keep Max conservatively unbounded.
			cost := checker.CostEstimate{Min: minCost.Min, Max: math.MaxUint64}
			resSize := checker.SizeEstimate{Min: safeAddUint64(bodySz.Min, 8), Max: math.MaxUint64}
			return &checker.CallEstimate{CostEstimate: cost, ResultSize: &resSize}
		}),
	)

	tracker := func(args []ref.Val, result ref.Val) *uint64 {
		if len(args) != 2 {
			return nil
		}
		metaEntries := actualContainerSize(args[0])
		bodyBytes := actualStringByteLen(args[1])
		if result == nil || types.IsError(result) {
			visitedNodes, visitedBytes, ok := extractTrackedWork(result)
			if !ok {
				visitedNodes = metaEntries
			}
			nodeCost := metaEntries
			if visitedNodes > nodeCost {
				nodeCost = visitedNodes
			}
			totalBytes := safeAddUint64(bodyBytes, visitedBytes)
			cost := safeAddUint64(safeAddUint64(1, nodeCost), scaledByteCost(totalBytes, marshallingTraversalFactor))
			return &cost
		}
		bytesScanned := safeAddUint64(bodyBytes, actualStringByteLen(result))
		cost := safeAddUint64(safeAddUint64(1, metaEntries), scaledByteCost(bytesScanned, marshallingTraversalFactor))
		return &cost
	}
	progOpt := cel.CostTrackerOptions(
		interpreter.OverloadCostTracker(overloadID, tracker),
	)

	lib := &funcLib{
		name: "cel.lib.ext.celexp.frontmatter.format",
		compileOpts: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(overloadID,
					[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.StringType},
					cel.StringType,
					cel.BinaryBinding(func(metaVal, bodyVal ref.Val) ref.Val {
						mapper, ok := metaVal.(traits.Mapper)
						if !ok {
							return newTrackedWorkErr(funcName, fmt.Errorf("expected map<string, dyn>, got %s", metaVal.Type()), 0, 0)
						}
						var mapBudget frontmatterBudget
						meta, convErr := frontmatterMapperToGoWithBudget(metaVal, mapper, &mapBudget)
						if convErr != nil {
							n, b := accumulateBudgets(&mapBudget, nil)
							return newTrackedWorkErr(funcName, convErr, n, b)
						}

						body, ok := bodyVal.Value().(string)
						if !ok {
							n, b := accumulateBudgets(&mapBudget, nil)
							return newTrackedWorkErr(funcName, fmt.Errorf("expected string body, got %T", bodyVal.Value()), n, b)
						}

						formatted, writtenBytes, err := marshalFrontmatterYAMLWithBytes(meta, body)
						if err != nil {
							n, b := accumulateBudgets(&mapBudget, nil)
							if writtenBytes > b {
								b = writtenBytes
							}
							return newTrackedWorkErr(funcName, err, n, b)
						}
						return types.String(formatted)
					}),
				),
			),
			estOpt,
		},
		programOpts: []cel.ProgramOption{progOpt},
	}

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "frontmatter.format(map<string, dyn>, string) -> string",
		Description:   "Formats a metadata map as YAML --- frontmatter followed by the document body",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Format metadata and Markdown body",
				Expression:  `frontmatter.format({"title": "Guide"}, "# Welcome\n")`,
			},
			{
				Description: "Round-trip parsed frontmatter after updating metadata",
				Expression:  `cel.bind(doc, frontmatter.parse("---\ntitle: Draft\n---\nBody"), frontmatter.format(map.add(doc.meta, "status", "published"), doc.body))`,
			},
		},
		EnvOptions: []cel.EnvOption{cel.Lib(lib)},
	}
}

func frontmatterMapperToGoWithBudget(metaVal ref.Val, mapper traits.Mapper, budget *frontmatterBudget) (map[string]any, error) {
	active := make(map[containerKey]struct{}, 8)
	if err := budget.chargeNodes(1); err != nil {
		return nil, err
	}
	if ck, hasKey := refValContainerKey(metaVal); hasKey {
		active[ck] = struct{}{}
		defer delete(active, ck)
	}
	meta := make(map[string]any)
	it := mapper.Iterator()
	for it.HasNext() == types.True {
		k := it.Next()
		if types.IsError(k) {
			return nil, fmt.Errorf("%v", k)
		}
		keyStr, keyOK := k.Value().(string)
		if !keyOK {
			return nil, fmt.Errorf("map keys must be strings, got %s", k.Type())
		}
		if _, exists := meta[keyStr]; exists {
			return nil, fmt.Errorf("%w: %q", ErrFrontmatterDuplicateKey, keyStr)
		}
		if err := budget.chargeNodes(1); err != nil {
			return nil, err
		}
		if err := budget.chargeBytes(len(keyStr)); err != nil {
			return nil, err
		}
		goVal, err := celValueToYAML(mapper.Get(k), 1, active, budget)
		if err != nil {
			return nil, err
		}
		meta[keyStr] = goVal
	}
	return meta, nil
}

// celValueToYAML recursively converts a CEL ref.Val into a Go value suitable
// for YAML encoding, preserving nested maps with comparable scalar keys
// (strings, booleans, integers, and non-NaN floats) while rejecting cyclic,
// excessively nested, over-budget, or key-colliding containers.
func celValueToYAML(val ref.Val, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) (any, error) {
	if types.IsError(val) {
		return nil, fmt.Errorf("%v", val)
	}
	if err := budget.chargeNodes(1); err != nil {
		return nil, err
	}
	if rawAnyMap, isRawAny := val.Value().(map[any]any); isRawAny {
		return rawAnyMapToYAML(rawAnyMap, depth, active, budget)
	}
	if mapper, ok := val.(traits.Mapper); ok {
		return nestedMapperToYAML(val, mapper, depth, active, budget)
	}
	if lister, ok := val.(traits.Lister); ok {
		return listerToYAML(val, lister, depth, active, budget)
	}
	leaf := conversion.NullSafeValue(val)
	switch v := leaf.(type) {
	case nil, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return leaf, nil
	case string:
		if err := budget.chargeBytes(len(v)); err != nil {
			return nil, err
		}
		return leaf, nil
	case []byte:
		if err := budget.chargeNodes(len(v)); err != nil {
			return nil, err
		}
		if err := budget.chargeBytes(len(v)); err != nil {
			return nil, err
		}
		return leaf, nil
	default:
		if err := validateYAMLValue(reflect.ValueOf(leaf), depth, active, budget); err != nil {
			return nil, err
		}
		return leaf, nil
	}
}

func rawAnyMapToYAML(rawAnyMap map[any]any, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) (any, error) {
	if depth > maxFrontmatterNestingDepth {
		return nil, ErrFrontmatterMaxDepth
	}
	if ck, hasKey := containerKeyOf(rawAnyMap); hasKey {
		if _, seen := active[ck]; seen {
			return nil, ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
	}
	if err := budget.chargeNodes(len(rawAnyMap)); err != nil {
		return nil, err
	}
	anyMap := make(map[any]any, len(rawAnyMap))
	for k, v := range rawAnyMap {
		nk := normalizeYAMLKey(k)
		if err := validateNormalizedNestedKey(k, nk); err != nil {
			return nil, err
		}
		if kt, isStr := nk.(string); isStr {
			if err := budget.chargeBytes(len(kt)); err != nil {
				return nil, err
			}
		}
		if _, exists := anyMap[nk]; exists {
			return nil, fmt.Errorf("%w: %v", ErrFrontmatterDuplicateKey, nk)
		}
		elemVal, err := celValueToYAML(conversion.GoToCelValue(v), depth+1, active, budget)
		if err != nil {
			return nil, err
		}
		anyMap[nk] = elemVal
	}
	return anyMap, nil
}

func nestedMapperToYAML(val ref.Val, mapper traits.Mapper, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) (any, error) {
	if depth > maxFrontmatterNestingDepth {
		return nil, ErrFrontmatterMaxDepth
	}
	if ck, hasKey := refValContainerKey(val); hasKey {
		if _, seen := active[ck]; seen {
			return nil, ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
	}
	strMap := make(map[string]any)
	var anyMap map[any]any
	it := mapper.Iterator()
	for it.HasNext() == types.True {
		k := it.Next()
		if types.IsError(k) {
			return nil, fmt.Errorf("%v", k)
		}
		if err := budget.chargeNodes(1); err != nil {
			return nil, err
		}
		elemVal, err := celValueToYAML(mapper.Get(k), depth+1, active, budget)
		if err != nil {
			return nil, err
		}
		kv := normalizeYAMLKey(conversion.NullSafeValue(k))
		if f, isFloat := kv.(float64); isFloat && math.IsNaN(f) {
			return nil, errors.New("map key cannot be NaN")
		}
		if ks, isStr := kv.(string); isStr && anyMap == nil {
			if err := budget.chargeBytes(len(ks)); err != nil {
				return nil, err
			}
			if _, exists := strMap[ks]; exists {
				return nil, fmt.Errorf("%w: %q", ErrFrontmatterDuplicateKey, ks)
			}
			strMap[ks] = elemVal
			continue
		}
		if anyMap == nil {
			anyMap = make(map[any]any, len(strMap)+1)
			for existingK, existingV := range strMap {
				anyMap[existingK] = existingV
			}
		}
		switch kt := kv.(type) {
		case string:
			if err := budget.chargeBytes(len(kt)); err != nil {
				return nil, err
			}
			if _, exists := anyMap[kv]; exists {
				return nil, fmt.Errorf("%w: %v", ErrFrontmatterDuplicateKey, kv)
			}
			anyMap[kv] = elemVal
		case nil, bool, int64, uint64, float64:
			if _, exists := anyMap[kv]; exists {
				return nil, fmt.Errorf("%w: %v", ErrFrontmatterDuplicateKey, kv)
			}
			anyMap[kv] = elemVal
		default:
			return nil, fmt.Errorf("unsupported nested map key type %s", k.Type())
		}
	}
	if anyMap != nil {
		return anyMap, nil
	}
	return strMap, nil
}

func listerToYAML(val ref.Val, lister traits.Lister, depth int, active map[containerKey]struct{}, budget *frontmatterBudget) (any, error) {
	if depth > maxFrontmatterNestingDepth {
		return nil, ErrFrontmatterMaxDepth
	}
	if ck, hasKey := refValContainerKey(val); hasKey {
		if _, seen := active[ck]; seen {
			return nil, ErrFrontmatterCyclicValue
		}
		active[ck] = struct{}{}
		defer delete(active, ck)
	}
	var capHint int
	if sz, szOK := lister.Size().(types.Int); szOK && sz > 0 {
		if int(sz) > maxFrontmatterVisitedNodes-budget.nodes {
			return nil, ErrFrontmatterBudgetExceeded
		}
		capHint = int(sz)
	}
	out := make([]any, 0, capHint)
	it := lister.Iterator()
	for it.HasNext() == types.True {
		elemVal, err := celValueToYAML(it.Next(), depth+1, active, budget)
		if err != nil {
			return nil, err
		}
		out = append(out, elemVal)
	}
	return out, nil
}
