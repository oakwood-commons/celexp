// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package celmath provides custom mathematical extension functions for CEL expressions.
package celmath

import (
	"math"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
	"github.com/oakwood-commons/celexp"
)

const (
	sumFuncName     = "math.sum"
	sumListIntID    = "math_sum_list_int"
	sumListDoubleID = "math_sum_list_double"
	sumLibraryName  = "cel.lib.ext.celexp.math.sum"
)

type sumLib struct{}

func (*sumLib) LibraryName() string {
	return sumLibraryName
}

func (*sumLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function(sumFuncName,
			cel.Overload(sumListIntID,
				[]*cel.Type{cel.ListType(cel.IntType)},
				cel.IntType,
				cel.UnaryBinding(func(val ref.Val) ref.Val {
					return sumList(val, types.IntZero)
				}),
			),
			cel.Overload(sumListDoubleID,
				[]*cel.Type{cel.ListType(cel.DoubleType)},
				cel.DoubleType,
				cel.UnaryBinding(func(val ref.Val) ref.Val {
					return sumList(val, types.Double(0))
				}),
			),
		),
		cel.CostEstimatorOptions(
			checker.OverloadCostEstimate(sumListIntID, estimateSumListCost),
			checker.OverloadCostEstimate(sumListDoubleID, estimateSumListCost),
		),
	}
}

func (*sumLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{
		cel.CostTrackerOptions(
			interpreter.OverloadCostTracker(sumListIntID, trackSumListCost),
			interpreter.OverloadCostTracker(sumListDoubleID, trackSumListCost),
		),
	}
}

// SumFunc returns a CEL extension function that sums a list of integers or
// floating-point numbers in O(n) time, returning 0 (or 0.0) on an empty list
// and returning an error if int64 addition overflows.
//
// Example usage:
//
//	math.sum([1, 2, 3])     // Returns: 6
//	math.sum([1.5, 2.5])    // Returns: 4.0
//	math.sum([])            // Returns: 0
func SumFunc() celexp.ExtFunction {
	return celexp.ExtFunction{
		Name:          sumFuncName,
		Signature:     "math.sum(list<int>) -> int | math.sum(list<double>) -> double",
		Description:   "Returns the sum of all numbers in a list of integers or doubles, returning 0 (or 0.0) on an empty list",
		FunctionNames: []string{sumFuncName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Sum a list of integers",
				Expression:  `math.sum([1, 2, 3])`,
			},
			{
				Description: "Sum a list of doubles",
				Expression:  `math.sum([1.5, 2.5])`,
			},
			{
				Description: "Empty list returns zero",
				Expression:  `math.sum([])`,
			},
		},
		EnvOptions: []cel.EnvOption{cel.Lib(&sumLib{})},
	}
}

func sumList(val, emptyDefault ref.Val) ref.Val {
	lister, ok := val.(traits.Lister)
	if !ok {
		return types.NewErr("math.sum: expected list, got %s", val.Type())
	}

	szVal, ok := lister.Size().(types.Int)
	if !ok {
		return types.NewErr("math.sum: unexpected list size type %T", lister.Size())
	}
	if szVal <= 0 {
		switch val.Value().(type) {
		case []float64, []float32:
			return types.Double(0)
		}
		return emptyDefault
	}

	first := lister.Get(types.IntZero)
	switch v0 := first.(type) {
	case types.Int:
		sum := int64(v0)
		for i := types.IntOne; i < szVal; i++ {
			elem := lister.Get(i)
			iv, intOK := elem.(types.Int)
			if !intOK {
				if types.IsError(elem) {
					return elem
				}
				return types.NewErr("math.sum: expected int element at index %d, got %s", int64(i), elem.Type())
			}
			v := int64(iv)
			if (v > 0 && sum > math.MaxInt64-v) || (v < 0 && sum < math.MinInt64-v) {
				return types.NewErr("math.sum: integer overflow")
			}
			sum += v
		}
		return types.Int(sum)

	case types.Double:
		sum := float64(v0)
		for i := types.IntOne; i < szVal; i++ {
			elem := lister.Get(i)
			dv, dblOK := elem.(types.Double)
			if !dblOK {
				if types.IsError(elem) {
					return elem
				}
				return types.NewErr("math.sum: expected double element at index %d, got %s", int64(i), elem.Type())
			}
			sum += float64(dv)
		}
		return types.Double(sum)

	default:
		if types.IsError(first) {
			return first
		}
		return types.NewErr("math.sum: expected int or double elements, got %s", first.Type())
	}
}

func estimateSumListCost(estimator checker.CostEstimator, _ *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
	if len(args) != 1 {
		return nil
	}
	sz := checker.SizeEstimate{Min: 0, Max: math.MaxUint64}
	if l := args[0].ComputedSize(); l != nil {
		sz = *l
	} else if estimator != nil {
		if l := estimator.EstimateSize(args[0]); l != nil {
			sz = *l
		}
	}
	cost := sz.MultiplyByCostFactor(1.0).Add(checker.FixedCostEstimate(1))
	resultSize := checker.FixedSizeEstimate(1)
	return &checker.CallEstimate{CostEstimate: cost, ResultSize: &resultSize}
}

func trackSumListCost(args []ref.Val, _ ref.Val) *uint64 {
	if len(args) != 1 {
		return nil
	}
	var sz uint64
	if sizer, ok := args[0].(traits.Sizer); ok {
		if n, ok := sizer.Size().(types.Int); ok && n > 0 {
			sz = uint64(n)
		}
	}
	cost := uint64(1)
	if sz > math.MaxUint64-1 {
		cost = math.MaxUint64
	} else {
		cost += sz
	}
	return &cost
}
