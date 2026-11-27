// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package marshalling

import (
	"math"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"
)

type funcLib struct {
	name        string
	compileOpts []cel.EnvOption
	programOpts []cel.ProgramOption
}

func (l *funcLib) LibraryName() string {
	return l.name
}

func (l *funcLib) CompileOptions() []cel.EnvOption {
	return l.compileOpts
}

func (l *funcLib) ProgramOptions() []cel.ProgramOption {
	return l.programOpts
}

func estimateArgSize(estimator checker.CostEstimator, node checker.AstNode) checker.SizeEstimate {
	if l := node.ComputedSize(); l != nil {
		return *l
	}
	if estimator != nil {
		if l := estimator.EstimateSize(node); l != nil {
			return *l
		}
	}
	return checker.SizeEstimate{Min: 0, Max: math.MaxUint64}
}

func actualStringByteLen(val ref.Val) uint64 {
	if val == nil {
		return 0
	}
	if s, ok := val.Value().(string); ok {
		n := len(s)
		if n > 0 {
			return uint64(n)
		}
		return 0
	}
	return actualContainerSize(val)
}

func actualContainerSize(val ref.Val) uint64 {
	if val == nil {
		return 0
	}
	if sz, ok := val.(traits.Sizer); ok {
		if n, ok := sz.Size().(types.Int); ok && n > 0 {
			return uint64(n)
		}
		return 0
	}
	return 1
}

func safeAddUint64(x, y uint64) uint64 {
	if y > 0 && x > math.MaxUint64-y {
		return math.MaxUint64
	}
	return x + y
}

func scaledByteCost(byteLen uint64, factor float64) uint64 {
	scaled := math.Ceil(float64(byteLen) * factor)
	if scaled >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	if scaled <= 0 {
		return 0
	}
	return uint64(scaled)
}

func unaryStringScanCostOptions(overloadID string, factor float64) (cel.EnvOption, cel.ProgramOption) {
	estOpt := cel.CostEstimatorOptions(
		checker.OverloadCostEstimate(overloadID, func(estimator checker.CostEstimator, _ *checker.AstNode, args []checker.AstNode) *checker.CallEstimate {
			if len(args) != 1 {
				return nil
			}
			sz := estimateArgSize(estimator, args[0])
			minCost := sz.MultiplyByCostFactor(factor).Add(checker.FixedCostEstimate(1))
			maxBytes := sz.Multiply(checker.FixedSizeEstimate(utf8.UTFMax))
			maxCost := maxBytes.MultiplyByCostFactor(factor).Add(checker.FixedCostEstimate(1))
			cost := checker.CostEstimate{Min: minCost.Min, Max: maxCost.Max}
			resSize := checker.SizeEstimate{Min: 0, Max: maxBytes.Max}
			return &checker.CallEstimate{CostEstimate: cost, ResultSize: &resSize}
		}),
	)

	tracker := func(args []ref.Val, _ ref.Val) *uint64 {
		if len(args) != 1 {
			return nil
		}
		cost := safeAddUint64(1, scaledByteCost(actualStringByteLen(args[0]), factor))
		return &cost
	}
	progOpt := cel.CostTrackerOptions(
		interpreter.OverloadCostTracker(overloadID, tracker),
	)
	return estOpt, progOpt
}

const marshallingTraversalFactor = common.StringTraversalCostFactor
