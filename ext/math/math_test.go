// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celmath

import (
	"math"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/oakwood-commons/celexp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSumFunc_Metadata(t *testing.T) {
	t.Parallel()

	fn := SumFunc()
	assert.Equal(t, "math.sum", fn.Name)
	assert.Equal(t, "math.sum(list<int>) -> int | math.sum(list<double>) -> double", fn.Signature)
	assert.NotEmpty(t, fn.Description)
	assert.Equal(t, []string{"math.sum"}, fn.FunctionNames)
	assert.True(t, fn.Custom)
	assert.NotEmpty(t, fn.Examples)
	assert.Len(t, fn.EnvOptions, 1)

	env, err := cel.NewEnv(fn.EnvOptions...)
	require.NoError(t, err)
	for _, ex := range fn.Examples {
		ast, iss := env.Compile(ex.Expression)
		require.NoError(t, iss.Err(), "example %q failed to compile", ex.Expression)
		prog, progErr := env.Program(ast)
		require.NoError(t, progErr)
		_, _, evalErr := prog.Eval(cel.NoVars())
		require.NoError(t, evalErr, "example %q failed to evaluate", ex.Expression)
	}
}

func TestSumFunc_CELIntegration(t *testing.T) {
	t.Parallel()

	fn := SumFunc()
	env, err := cel.NewEnv(
		fn.EnvOptions[0],
		cel.Variable("ints", cel.ListType(cel.IntType)),
		cel.Variable("doubles", cel.ListType(cel.DoubleType)),
		cel.Variable("dynList", cel.DynType),
	)
	require.NoError(t, err)

	tests := []struct {
		name    string
		expr    string
		vars    map[string]any
		want    any
		wantErr string
	}{
		{
			name: "sum int literal list",
			expr: `math.sum([1, 2, 3])`,
			want: int64(6),
		},
		{
			name: "sum empty literal list equals zero",
			expr: `math.sum([])`,
			want: int64(0),
		},
		{
			name: "sum empty literal list equality",
			expr: `math.sum([]) == 0`,
			want: true,
		},
		{
			name: "sum double literal list",
			expr: `math.sum([1.5, 2.5])`,
			want: float64(4.0),
		},
		{
			name: "sum negative and positive ints",
			expr: `math.sum([-10, 4, 6])`,
			want: int64(0),
		},
		{
			name: "sum typed empty int slice",
			expr: `math.sum(ints)`,
			vars: map[string]any{"ints": []int64{}},
			want: int64(0),
		},
		{
			name: "sum typed empty double slice",
			expr: `math.sum(doubles)`,
			vars: map[string]any{"doubles": []float64{}},
			want: float64(0.0),
		},
		{
			name: "sum typed empty double generic slice",
			expr: `math.sum(doubles)`,
			vars: map[string]any{"doubles": []any{}},
			want: float64(0.0),
		},
		{
			name: "sum dyn list of doubles",
			expr: `math.sum(dynList)`,
			vars: map[string]any{"dynList": []any{float64(1.25), float64(2.75)}},
			want: float64(4.0),
		},
		{
			name: "sum dyn list of ints",
			expr: `math.sum(dynList)`,
			vars: map[string]any{"dynList": []any{int64(10), int64(20), int64(30)}},
			want: int64(60),
		},
		{
			name: "sum dyn empty float64 slice",
			expr: `math.sum(dynList)`,
			vars: map[string]any{"dynList": []float64{}},
			want: float64(0.0),
		},
		{
			name: "sum dyn empty float32 slice",
			expr: `math.sum(dynList)`,
			vars: map[string]any{"dynList": []float32{}},
			want: float64(0.0),
		},
		{
			name: "max int64 without overflow",
			expr: `math.sum(ints)`,
			vars: map[string]any{"ints": []int64{math.MaxInt64 - 1, 1}},
			want: int64(math.MaxInt64),
		},
		{
			name: "min int64 without underflow",
			expr: `math.sum(ints)`,
			vars: map[string]any{"ints": []int64{math.MinInt64 + 1, -1}},
			want: int64(math.MinInt64),
		},
		{
			name: "canceling extremes without overflow",
			expr: `math.sum(ints)`,
			vars: map[string]any{"ints": []int64{math.MaxInt64, -math.MaxInt64, 42}},
			want: int64(42),
		},
		{
			name:    "positive int64 overflow returns error",
			expr:    `math.sum(ints)`,
			vars:    map[string]any{"ints": []int64{math.MaxInt64, 1}},
			wantErr: "math.sum: integer overflow",
		},
		{
			name:    "negative int64 overflow returns error",
			expr:    `math.sum(ints)`,
			vars:    map[string]any{"ints": []int64{math.MinInt64, -1}},
			wantErr: "math.sum: integer overflow",
		},
		{
			name:    "mixed int and double in dyn list returns error",
			expr:    `math.sum(dynList)`,
			vars:    map[string]any{"dynList": []any{int64(1), float64(2.5)}},
			wantErr: "math.sum: expected int element at index 1",
		},
		{
			name:    "mixed double and int in dyn list returns error",
			expr:    `math.sum(dynList)`,
			vars:    map[string]any{"dynList": []any{float64(1.5), int64(2)}},
			wantErr: "math.sum: expected double element at index 1",
		},
		{
			name:    "non numeric elements in dyn list returns error",
			expr:    `math.sum(dynList)`,
			vars:    map[string]any{"dynList": []any{"a", "b"}},
			wantErr: "no such overload: math.sum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ast, issues := env.Compile(tt.expr)
			require.NoError(t, issues.Err())
			prog, progErr := env.Program(ast)
			require.NoError(t, progErr)

			vars := tt.vars
			if vars == nil {
				vars = map[string]any{}
			}
			out, _, evalErr := prog.Eval(vars)
			if tt.wantErr != "" {
				require.Error(t, evalErr)
				assert.Contains(t, evalErr.Error(), tt.wantErr)
				return
			}
			require.NoError(t, evalErr)
			assert.Equal(t, tt.want, out.Value())
		})
	}
}

func TestSumFunc_CompileRejectsInvalidTypes(t *testing.T) {
	t.Parallel()

	fn := SumFunc()
	env, err := cel.NewEnv(fn.EnvOptions[0])
	require.NoError(t, err)

	for _, badExpr := range []string{
		`math.sum(["a", "b"])`,
		`math.sum(42)`,
		`math.sum()`,
		`math.sum([1], [2])`,
	} {
		_, issues := env.Compile(badExpr)
		assert.Error(t, issues.Err(), "expected %q to fail compilation", badExpr)
	}
}

func TestSumFunc_CostTrackingAndEstimation(t *testing.T) {
	t.Parallel()

	fn := SumFunc()
	opts := []cel.EnvOption{
		fn.EnvOptions[0],
		cel.Variable("nums", cel.ListType(cel.IntType)),
		cel.Variable("dbls", cel.ListType(cel.DoubleType)),
	}

	env, err := cel.NewEnv(opts...)
	require.NoError(t, err)

	for _, exprStr := range []string{
		`math.sum([1, 2, 3, 4, 5])`,
		`math.sum([1.5, 2.5, 3.5, 4.5, 5.5])`,
	} {
		ast, iss := env.Compile(exprStr)
		require.NoError(t, iss.Err())
		est, estErr := env.EstimateCost(ast, checker.CostEstimator(nil))
		require.NoError(t, estErr)
		assert.GreaterOrEqual(t, est.Min, uint64(6))
	}

	nums := make([]int64, 500)
	dbls := make([]float64, 500)
	for i := range nums {
		nums[i] = 1
		dbls[i] = 1.5
	}

	expr := celexp.Expression(`math.sum(nums)`)
	tightProg, err := expr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(50))
	require.NoError(t, err)
	_, err = tightProg.Eval(map[string]any{"nums": nums})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	dblExpr := celexp.Expression(`math.sum(dbls)`)
	tightDblProg, err := dblExpr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(50))
	require.NoError(t, err)
	_, err = tightDblProg.Eval(map[string]any{"dbls": dbls})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	generousProg, err := expr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(10_000))
	require.NoError(t, err)
	val, err := generousProg.Eval(map[string]any{"nums": nums})
	require.NoError(t, err)
	assert.Equal(t, int64(500), val)
}

func TestSumFunc_SharedProgramCacheOverloadIsolation(t *testing.T) {
	t.Parallel()

	fn := SumFunc()
	expr := celexp.Expression(`type(math.sum(nums)) == double`)

	for _, tc := range []struct {
		name  string
		cache *celexp.ProgramCache
	}{
		{name: "traditional cache", cache: celexp.NewProgramCache(16)},
		{name: "ast-based cache", cache: celexp.NewProgramCache(16, celexp.WithASTBasedCaching(true))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			intProg, err := expr.Compile(
				[]cel.EnvOption{fn.EnvOptions[0], cel.Variable("nums", cel.ListType(cel.IntType))},
				celexp.WithCache(tc.cache),
			)
			require.NoError(t, err)

			dblProg, err := expr.Compile(
				[]cel.EnvOption{fn.EnvOptions[0], cel.Variable("nums", cel.ListType(cel.DoubleType))},
				celexp.WithCache(tc.cache),
			)
			require.NoError(t, err)

			gotInt, err := intProg.Eval(map[string]any{"nums": []any{}})
			require.NoError(t, err)
			assert.Equal(t, false, gotInt)

			gotDbl, err := dblProg.Eval(map[string]any{"nums": []any{}})
			require.NoError(t, err)
			assert.Equal(t, true, gotDbl)
		})
	}
}

func BenchmarkMathSum_Int(b *testing.B) {
	fn := SumFunc()
	env, _ := cel.NewEnv(fn.EnvOptions[0], cel.Variable("nums", cel.ListType(cel.IntType)))
	ast, _ := env.Compile(`math.sum(nums)`)
	prog, _ := env.Program(ast)
	nums := make([]int64, 100)
	for i := range nums {
		nums[i] = int64(i + 1)
	}
	vars := map[string]any{"nums": nums}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = prog.Eval(vars)
	}
}

func BenchmarkMathSum_Double(b *testing.B) {
	fn := SumFunc()
	env, _ := cel.NewEnv(fn.EnvOptions[0], cel.Variable("nums", cel.ListType(cel.DoubleType)))
	ast, _ := env.Compile(`math.sum(nums)`)
	prog, _ := env.Program(ast)
	nums := make([]float64, 100)
	for i := range nums {
		nums[i] = float64(i) + 0.5
	}
	vars := map[string]any{"nums": nums}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = prog.Eval(vars)
	}
}
