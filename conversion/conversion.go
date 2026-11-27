// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package conversion

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

const (
	// maxConversionDepth bounds recursive container nesting during CEL-to-Go
	// conversion so cyclic or deeply nested host structures cannot exhaust the
	// call stack.
	maxConversionDepth = 64

	// maxConversionNodes bounds total container and element visits during a
	// single CEL-to-Go conversion so shared acyclic graphs cannot expand
	// exponentially in CPU or memory.
	maxConversionNodes = 100_000
)

var (
	// ErrCyclicValue is returned by TryCelValueToGo when a container references
	// an ancestor on the active traversal path.
	ErrCyclicValue = errors.New("conversion: cyclic value detected")

	// ErrMaxDepthExceeded is returned by TryCelValueToGo when container nesting
	// exceeds maxConversionDepth.
	ErrMaxDepthExceeded = errors.New("conversion: maximum nesting depth exceeded")

	// ErrBudgetExceeded is returned by TryCelValueToGo when total visited nodes
	// exceed maxConversionNodes.
	ErrBudgetExceeded = errors.New("conversion: traversal budget exceeded")

	// ErrDuplicateMapKey is returned by TryCelValueToGo when distinct native
	// map keys normalize to the same canonical Go map key.
	ErrDuplicateMapKey = errors.New("conversion: duplicate map key after normalization")
)

// ListToStringSlice converts a CEL list to a Go string slice.
// Returns an error if the list contains non-string elements.
func ListToStringSlice(listVal ref.Val) ([]string, error) {
	// Type check the list
	list, ok := listVal.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("expected list, got %s", listVal.Type())
	}

	// Convert list to string slice
	iterator := list.Iterator()
	result := make([]string, 0)
	for iterator.HasNext() == types.True {
		item := iterator.Next()
		str, ok := item.Value().(string)
		if !ok {
			return nil, fmt.Errorf("list contains non-string element of type %s", item.Type())
		}
		result = append(result, str)
	}

	return result, nil
}

// ToObject converts a CEL map to a Go map[string]any.
// Returns an error if the input is not a map or if any key is not a string.
//
// Values are converted one level deep: each value is normalized via
// NullSafeValue (so a null value becomes Go nil, never a numeric 0), but nested
// maps/lists are returned in their immediate CEL representation. Use
// CelValueToGo when a fully-recursive conversion is required.
func ToObject(mapVal ref.Val) (map[string]any, error) {
	// Type check the map
	mapper, ok := mapVal.(traits.Mapper)
	if !ok {
		return nil, fmt.Errorf("expected map, got %s", mapVal.Type())
	}

	// Convert map to Go map
	result := make(map[string]any)
	iterator := mapper.Iterator()
	for iterator.HasNext() == types.True {
		key := iterator.Next()
		keyStr, ok := key.Value().(string)
		if !ok {
			return nil, fmt.Errorf("map contains non-string key of type %s", key.Type())
		}
		value := mapper.Get(key)
		result[keyStr] = NullSafeValue(value)
	}

	return result, nil
}

// ListToObjectSlice converts a CEL list of maps to a Go slice of map[string]any.
// Returns an error if the list contains non-map elements or if any map has non-string keys.
func ListToObjectSlice(listVal ref.Val) ([]map[string]any, error) {
	// Type check the list
	list, ok := listVal.(traits.Lister)
	if !ok {
		return nil, fmt.Errorf("expected list, got %s", listVal.Type())
	}

	// Convert list to slice of maps
	iterator := list.Iterator()
	result := make([]map[string]any, 0)
	for iterator.HasNext() == types.True {
		item := iterator.Next()
		obj, err := ToObject(item)
		if err != nil {
			return nil, fmt.Errorf("list contains non-map element: %w", err)
		}
		result = append(result, obj)
	}

	return result, nil
}

// NullSafeValue returns the native Go value of a CEL ref.Val, mapping CEL null
// to Go nil.
//
// It behaves like ref.Val.Value() for every type except null. This is required
// because cel-go's Null.Value() returns structpb.NullValue_NULL_VALUE, whose
// underlying value is the integer 0. Returning that raw value would silently
// corrupt an explicit null into a numeric 0 (for example when round-tripping
// through json.unmarshal or when serializing an evaluation result), so null is
// normalized to Go nil here. A nil ref.Val is likewise treated as nil.
//
// Note that for container values (maps, lists) this returns the value's
// immediate representation without recursing; use CelValueToGo for a deep
// conversion that normalizes nested nulls.
func NullSafeValue(val ref.Val) any {
	if val == nil {
		return nil
	}
	if _, ok := val.(types.Null); ok {
		return nil
	}
	return val.Value()
}

type convContainerKey struct {
	kind reflect.Kind
	ptr  uintptr
	len  int
}

func convContainerKeyOf(raw any) (convContainerKey, bool) {
	if raw == nil {
		return convContainerKey{}, false
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() { //nolint:exhaustive // Only reference kinds that can form cycles are tracked.
	case reflect.Map:
		if rv.IsNil() || rv.Len() == 0 {
			return convContainerKey{}, false
		}
		return convContainerKey{kind: reflect.Map, ptr: rv.Pointer()}, true
	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return convContainerKey{}, false
		}
		return convContainerKey{kind: reflect.Slice, ptr: rv.Pointer(), len: rv.Len()}, true
	case reflect.Pointer:
		if rv.IsNil() {
			return convContainerKey{}, false
		}
		return convContainerKey{kind: reflect.Pointer, ptr: rv.Pointer()}, true
	default:
		return convContainerKey{}, false
	}
}

func convRefValContainerKey(val ref.Val) (convContainerKey, bool) {
	if val == nil {
		return convContainerKey{}, false
	}
	if ck, ok := convContainerKeyOf(val.Value()); ok {
		return ck, true
	}
	return convContainerKeyOf(val)
}

func convCanHoldContainers(t reflect.Type) bool {
	switch t.Kind() { //nolint:exhaustive // Scalar kinds cannot hold nested containers.
	case reflect.Interface, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Struct:
		return true
	default:
		return false
	}
}

type celToGoState struct {
	active map[convContainerKey]struct{}
	nodes  int
	strict bool
}

func (s *celToGoState) stepNode() error {
	if s.nodes >= maxConversionNodes {
		return ErrBudgetExceeded
	}
	s.nodes++
	return nil
}

func (s *celToGoState) stepNodes(n int) error {
	if n <= 0 {
		return nil
	}
	if n > maxConversionNodes-s.nodes {
		s.nodes = maxConversionNodes + 1
		return ErrBudgetExceeded
	}
	s.nodes += n
	return nil
}

// TryCelValueToGo recursively converts a CEL ref.Val to a native Go value while
// enforcing active-path cycle detection, a maximum nesting depth, a total
// traversal node budget, and normalized map-key uniqueness.
//
// Maps whose keys are all strings convert to map[string]any; maps containing
// any non-string comparable key convert to map[any]any with integer and
// floating-point keys widened to CEL's canonical int64, uint64, and float64
// types.
func TryCelValueToGo(val ref.Val) (any, error) {
	state := celToGoState{
		active: make(map[convContainerKey]struct{}, 8),
		strict: true,
	}
	return state.convert(val, 0)
}

// CelValueToGo recursively converts a CEL ref.Val to a native Go value.
// Maps whose keys are all strings convert to map[string]any; maps containing
// any non-string comparable key convert to map[any]any. Cyclic or over-budget
// branches terminate safely as nil, and colliding width-normalized native keys
// retain their distinct raw types rather than overwriting entries. Use
// TryCelValueToGo when callers should fail with an explicit error on cycles,
// excessive depth, budget exhaustion, or normalized key collisions.
func CelValueToGo(val ref.Val) any {
	state := celToGoState{
		active: make(map[convContainerKey]struct{}, 8),
		strict: false,
	}
	out, _ := state.convert(val, 0)
	return out
}

func (s *celToGoState) convert(val ref.Val, depth int) (any, error) {
	if val == nil {
		return nil, nil
	}
	if err := s.stepNode(); err != nil {
		if s.strict {
			return nil, err
		}
		return nil, nil
	}
	if s.strict && types.IsError(val) {
		return nil, fmt.Errorf("conversion: %v", val)
	}

	if rawAnyMap, ok := val.Value().(map[any]any); ok {
		return s.convertRawAnyMap(rawAnyMap, depth)
	}
	if mapper, ok := val.(traits.Mapper); ok {
		return s.convertMapper(val, mapper, depth)
	}
	if lister, ok := val.(traits.Lister); ok {
		return s.convertLister(val, lister, depth)
	}

	leaf := NullSafeValue(val)
	switch leaf.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, []byte:
		return leaf, nil
	}
	if err := s.validateNativeValue(reflect.ValueOf(leaf), depth); err != nil {
		if s.strict {
			return nil, err
		}
		return nil, nil
	}
	return leaf, nil
}

func (s *celToGoState) convertRawAnyMap(raw map[any]any, depth int) (any, error) {
	if depth > maxConversionDepth {
		if s.strict {
			return nil, ErrMaxDepthExceeded
		}
		return nil, nil
	}
	if ck, hasKey := convContainerKeyOf(raw); hasKey {
		if _, seen := s.active[ck]; seen {
			if s.strict {
				return nil, ErrCyclicValue
			}
			return nil, nil
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
	}

	if err := s.stepNodes(len(raw)); err != nil {
		if s.strict {
			return nil, err
		}
		return nil, nil
	}
	hasNormCollision := rawMapHasNormalizationCollision(raw)
	if hasNormCollision && s.strict {
		return nil, ErrDuplicateMapKey
	}

	strResult := make(map[string]any)
	var anyResult map[any]any
	for rawKey, rawVal := range raw {
		goVal, err := s.convert(GoToCelValue(rawVal), depth+1)
		if err != nil {
			return nil, err
		}
		if keyStr, isStr := rawKey.(string); isStr && anyResult == nil && !hasNormCollision {
			strResult[keyStr] = goVal
			continue
		}
		if anyResult == nil {
			anyResult = make(map[any]any, len(strResult)+1)
			for existingK, existingV := range strResult {
				anyResult[existingK] = existingV
			}
		}
		kv := rawKey
		if !hasNormCollision {
			kv = normalizeGoMapKey(rawKey)
		}
		if kv != nil && !reflect.TypeOf(kv).Comparable() {
			if s.strict {
				return nil, fmt.Errorf("conversion: uncomparable map key type %T", kv)
			}
			continue
		}
		anyResult[kv] = goVal
	}
	if anyResult != nil {
		return anyResult, nil
	}
	return strResult, nil
}

func rawMapHasNormalizationCollision(raw map[any]any) bool {
	if len(raw) <= 1 {
		return false
	}
	seen := make(map[any]struct{}, len(raw))
	for rawKey := range raw {
		nk := normalizeGoMapKey(rawKey)
		if nk != nil && !reflect.TypeOf(nk).Comparable() {
			continue
		}
		if _, exists := seen[nk]; exists {
			return true
		}
		seen[nk] = struct{}{}
	}
	return false
}

func (s *celToGoState) convertMapper(val ref.Val, mapper traits.Mapper, depth int) (any, error) {
	if depth > maxConversionDepth {
		if s.strict {
			return nil, ErrMaxDepthExceeded
		}
		return nil, nil
	}
	if ck, hasKey := convRefValContainerKey(val); hasKey {
		if _, seen := s.active[ck]; seen {
			if s.strict {
				return nil, ErrCyclicValue
			}
			return nil, nil
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
	}

	strResult := make(map[string]any)
	var anyResult map[any]any
	var origKeys map[any]any
	iterator := mapper.Iterator()
	for iterator.HasNext() == types.True {
		key := iterator.Next()
		if s.strict && types.IsError(key) {
			return nil, fmt.Errorf("conversion: %v", key)
		}
		goVal, err := s.convert(mapper.Get(key), depth+1)
		if err != nil {
			return nil, err
		}
		if keyStr, isStr := key.Value().(string); isStr && anyResult == nil {
			if _, exists := strResult[keyStr]; exists && s.strict {
				return nil, fmt.Errorf("%w: %q", ErrDuplicateMapKey, keyStr)
			}
			strResult[keyStr] = goVal
			continue
		}
		if anyResult == nil {
			anyResult = make(map[any]any, len(strResult)+1)
			origKeys = make(map[any]any, len(strResult)+1)
			for existingK, existingV := range strResult {
				anyResult[existingK] = existingV
				origKeys[existingK] = existingK
			}
		}
		rawKV := NullSafeValue(key)
		kv := normalizeGoMapKey(rawKV)
		if kv != nil && !reflect.TypeOf(kv).Comparable() {
			if s.strict {
				return nil, fmt.Errorf("conversion: uncomparable map key type %T", kv)
			}
			continue
		}
		if prevVal, exists := anyResult[kv]; exists {
			if s.strict {
				return nil, fmt.Errorf("%w: %v", ErrDuplicateMapKey, kv)
			}
			if prevOrig, hasOrig := origKeys[kv]; hasOrig && prevOrig != kv {
				delete(anyResult, kv)
				anyResult[prevOrig] = prevVal
				origKeys[prevOrig] = prevOrig
			}
			if rawKV == nil || reflect.TypeOf(rawKV).Comparable() {
				anyResult[rawKV] = goVal
				origKeys[rawKV] = rawKV
			}
			continue
		}
		anyResult[kv] = goVal
		origKeys[kv] = rawKV
	}
	if anyResult != nil {
		return anyResult, nil
	}
	return strResult, nil
}

func (s *celToGoState) convertLister(val ref.Val, lister traits.Lister, depth int) (any, error) {
	if depth > maxConversionDepth {
		if s.strict {
			return nil, ErrMaxDepthExceeded
		}
		return nil, nil
	}
	if ck, hasKey := convRefValContainerKey(val); hasKey {
		if _, seen := s.active[ck]; seen {
			if s.strict {
				return nil, ErrCyclicValue
			}
			return nil, nil
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
	}

	result := make([]any, 0)
	iterator := lister.Iterator()
	for iterator.HasNext() == types.True {
		item, err := s.convert(iterator.Next(), depth+1)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *celToGoState) validateNativeValue(rv reflect.Value, depth int) error {
	for rv.IsValid() && rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return nil
	}
	if err := s.stepNode(); err != nil {
		return err
	}

	switch rv.Kind() { //nolint:exhaustive // Scalar kinds cannot contain nested containers or cycles.
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		if depth > maxConversionDepth {
			return ErrMaxDepthExceeded
		}
		ck := convContainerKey{kind: reflect.Pointer, ptr: rv.Pointer()}
		if _, seen := s.active[ck]; seen {
			return ErrCyclicValue
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
		return s.validateNativeValue(rv.Elem(), depth+1)

	case reflect.Map:
		if rv.IsNil() || rv.Len() == 0 {
			return nil
		}
		if depth > maxConversionDepth {
			return ErrMaxDepthExceeded
		}
		ck := convContainerKey{kind: reflect.Map, ptr: rv.Pointer()}
		if _, seen := s.active[ck]; seen {
			return ErrCyclicValue
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
		if err := s.stepNodes(rv.Len()); err != nil {
			return err
		}
		hasKeyC := convCanHoldContainers(rv.Type().Key())
		hasValC := convCanHoldContainers(rv.Type().Elem())
		if !hasKeyC && !hasValC {
			return nil
		}
		iter := rv.MapRange()
		for iter.Next() {
			if hasKeyC {
				if err := s.validateNativeValue(iter.Key(), depth+1); err != nil {
					return err
				}
			}
			if hasValC {
				if err := s.validateNativeValue(iter.Value(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil

	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return nil
		}
		if depth > maxConversionDepth {
			return ErrMaxDepthExceeded
		}
		ck := convContainerKey{kind: reflect.Slice, ptr: rv.Pointer(), len: rv.Len()}
		if _, seen := s.active[ck]; seen {
			return ErrCyclicValue
		}
		s.active[ck] = struct{}{}
		defer delete(s.active, ck)
		if err := s.stepNodes(rv.Len()); err != nil {
			return err
		}
		if !convCanHoldContainers(rv.Type().Elem()) {
			return nil
		}
		for i := 0; i < rv.Len(); i++ {
			if err := s.validateNativeValue(rv.Index(i), depth+1); err != nil {
				return err
			}
		}
		return nil

	case reflect.Array:
		if rv.Len() == 0 {
			return nil
		}
		if depth > maxConversionDepth {
			return ErrMaxDepthExceeded
		}
		if err := s.stepNodes(rv.Len()); err != nil {
			return err
		}
		if !convCanHoldContainers(rv.Type().Elem()) {
			return nil
		}
		for i := 0; i < rv.Len(); i++ {
			if err := s.validateNativeValue(rv.Index(i), depth+1); err != nil {
				return err
			}
		}
		return nil

	case reflect.Struct:
		if depth > maxConversionDepth {
			return ErrMaxDepthExceeded
		}
		rt := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() || !convCanHoldContainers(sf.Type) {
				continue
			}
			if err := s.validateNativeValue(rv.Field(i), depth+1); err != nil {
				return err
			}
		}
		return nil

	default:
		return nil
	}
}

func normalizeGoMapKey(k any) any {
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

// GoToCelValue converts a native Go value to a CEL ref.Val.
// This uses the default type adapter to ensure proper conversion.
func GoToCelValue(val any) ref.Val {
	return types.DefaultTypeAdapter.NativeToValue(val)
}
