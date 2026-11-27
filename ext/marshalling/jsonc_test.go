// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package marshalling

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/conversion"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONStripCommentsFunc_Metadata(t *testing.T) {
	t.Parallel()

	fn := JSONStripCommentsFunc()
	assert.Equal(t, "json.stripComments", fn.Name)
	assert.Equal(t, "json.stripComments(string) -> string", fn.Signature)
	assert.NotEmpty(t, fn.Description)
	assert.Equal(t, []string{"json.stripComments"}, fn.FunctionNames)
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

func TestJSONUnmarshalJSONCFunc_Metadata(t *testing.T) {
	t.Parallel()

	fn := JSONUnmarshalJSONCFunc()
	assert.Equal(t, "json.unmarshalJSONC", fn.Name)
	assert.Equal(t, "json.unmarshalJSONC(string) -> dyn", fn.Signature)
	assert.NotEmpty(t, fn.Description)
	assert.Equal(t, []string{"json.unmarshalJSONC"}, fn.FunctionNames)
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

func TestStripJSONC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantParse any
		wantErr   error
		badJSON   bool
	}{
		{
			name:      "standard json object",
			input:     `{"host": "localhost", "port": 8080}`,
			wantParse: map[string]any{"host": "localhost", "port": float64(8080)},
		},
		{
			name:      "utf8 bom and line comments",
			input:     "\uFEFF// top-level file comment\n{\n  // port comment\n  \"port\": 9000 // inline comment\n}",
			wantParse: map[string]any{"port": float64(9000)},
		},
		{
			name:      "crlf line endings with comments and trailing commas",
			input:     "// header\r\n{\r\n  \"items\": [\r\n    \"a\",\r\n    \"b\", // trailing comma with crlf\r\n  ],\r\n}\r\n",
			wantParse: map[string]any{"items": []any{"a", "b"}},
		},
		{
			name:      "block comments inline empty and multiline",
			input:     "/**/{\n  /* multi\n     line\n     comment */\n  \"enabled\": /***/ true,\n}",
			wantParse: map[string]any{"enabled": true},
		},
		{
			name:      "trailing comma separated from closing brace by comments",
			input:     "{\n  \"a\": 1, // comment after comma\n  /* another\n     comment */\n}",
			wantParse: map[string]any{"a": float64(1)},
		},
		{
			name:      "nested trailing commas in arrays and objects",
			input:     `[{"a": [1, 2,], "b": {"c": true,},},]`,
			wantParse: []any{map[string]any{"a": []any{float64(1), float64(2)}, "b": map[string]any{"c": true}}},
		},
		{
			name:  "url and comment tokens inside string literals preserved",
			input: `{"url": "https://example.com//path/*keep*/,comma", "glob": "src/**/*.go"}`,
			wantParse: map[string]any{
				"url":  "https://example.com//path/*keep*/,comma",
				"glob": "src/**/*.go",
			},
		},
		{
			name:  "escaped quotes and escaped backslashes inside string literals",
			input: "{\"quote\": \"say \\\"hello // not comment /* still string */\\\"\", \"win\": \"C:\\\\Users\\\\app\\\\\" // real comment\n}",
			wantParse: map[string]any{
				"quote": `say "hello // not comment /* still string */"`,
				"win":   `C:\Users\app\`,
			},
		},
		{
			name:      "quotes inside line and block comments do not enter string state",
			input:     "// unmatched \" quote in line comment\n/* unmatched \" quote in block comment */\n{\"ok\": true,}",
			wantParse: map[string]any{"ok": true},
		},
		{
			name:    "unterminated block comment",
			input:   `{"a": 1 /* missing end`,
			wantErr: ErrUnterminatedBlockComment,
		},
		{
			name:    "slash star slash is unterminated",
			input:   `/*/`,
			wantErr: ErrUnterminatedBlockComment,
		},
		{
			name:    "leading comma in array stays invalid json",
			input:   `[,]`,
			badJSON: true,
		},
		{
			name:    "leading comma in object stays invalid json",
			input:   `{,}`,
			badJSON: true,
		},
		{
			name:    "double comma in array stays invalid json",
			input:   `[1,,]`,
			badJSON: true,
		},
		{
			name:    "comma after colon stays invalid json",
			input:   `{"a":,}`,
			badJSON: true,
		},
		{
			name:    "top level trailing comma stays invalid json",
			input:   `1,`,
			badJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cleaned, err := StripJSONC(tt.input)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			var got any
			unmarshalErr := json.Unmarshal([]byte(cleaned), &got)
			if tt.badJSON {
				require.Error(t, unmarshalErr, "expected stripped output %q to remain invalid JSON", cleaned)
				return
			}
			require.NoError(t, unmarshalErr, "stripped output was %q", cleaned)
			assert.Equal(t, tt.wantParse, got)
		})
	}
}

func TestStripJSONC_PreservesSyntaxErrorLineAndOffset(t *testing.T) {
	t.Parallel()

	withBlockComment := "{\n/* line 2\n   line 3\n*/\n  \"bad\": tru\n}"
	withSpaces := "{\n         \n         \n  \n  \"bad\": tru\n}"

	cleaned, err := StripJSONC(withBlockComment)
	require.NoError(t, err)

	var v1, v2 any
	err1 := json.Unmarshal([]byte(cleaned), &v1)
	err2 := json.Unmarshal([]byte(withSpaces), &v2)
	require.Error(t, err1)
	require.Error(t, err2)

	var syn1, syn2 *json.SyntaxError
	require.True(t, errors.As(err1, &syn1))
	require.True(t, errors.As(err2, &syn2))
	assert.Equal(t, syn2.Offset, syn1.Offset)
	assert.Equal(t, strings.Count(cleaned[:syn1.Offset], "\n"), strings.Count(withSpaces[:syn2.Offset], "\n"))
}

func TestJSONUnmarshalJSONCFunc_CELIntegration(t *testing.T) {
	t.Parallel()

	unmarshalFn := JSONUnmarshalJSONCFunc()
	stripFn := JSONStripCommentsFunc()
	stdUnmarshalFn := JSONUnmarshalFunc()
	env, err := cel.NewEnv(
		unmarshalFn.EnvOptions[0],
		stripFn.EnvOptions[0],
		stdUnmarshalFn.EnvOptions[0],
		cel.Variable("doc", cel.StringType),
	)
	require.NoError(t, err)

	tsconfigSample := "\uFEFF{\n" +
		"  // TypeScript compiler configuration\n" +
		"  \"compilerOptions\": {\n" +
		"    \"target\": \"ES2022\",\n" +
		"    \"baseUrl\": \"https://cdn.example.com//assets/*v1*/\",\n" +
		"    \"paths\": {\n" +
		"      \"@app/*\": [\"src/*\",], /* trailing comma */\n" +
		"    },\n" +
		"    \"nullable\": null,\n" +
		"  },\n" +
		"}\n"

	tests := []struct {
		name    string
		expr    string
		doc     string
		want    any
		wantErr string
	}{
		{
			name: "access nested url and array element",
			expr: `json.unmarshalJSONC(doc).compilerOptions.baseUrl`,
			doc:  tsconfigSample,
			want: "https://cdn.example.com//assets/*v1*/",
		},
		{
			name: "stripComments piped to standard json.unmarshal",
			expr: `json.unmarshal(json.stripComments(doc)).compilerOptions.target`,
			doc:  tsconfigSample,
			want: "ES2022",
		},
		{
			name: "null field preserved in jsonc",
			expr: `has(json.unmarshalJSONC(doc).compilerOptions.nullable) && json.unmarshalJSONC(doc).compilerOptions.nullable == null`,
			doc:  tsconfigSample,
			want: true,
		},
		{
			name:    "unterminated block comment surfaces function error",
			expr:    `json.unmarshalJSONC(doc)`,
			doc:     `{"a": /* oops`,
			wantErr: "json.unmarshalJSONC: unterminated block comment",
		},
		{
			name:    "stripComments unterminated block comment error",
			expr:    `json.stripComments(doc)`,
			doc:     `{"a": /* oops`,
			wantErr: "json.stripComments: unterminated block comment",
		},
		{
			name:    "malformed json syntax error",
			expr:    `json.unmarshalJSONC(doc)`,
			doc:     `{"a": }`,
			wantErr: "json.unmarshalJSONC:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ast, issues := env.Compile(tt.expr)
			require.NoError(t, issues.Err())
			prog, progErr := env.Program(ast)
			require.NoError(t, progErr)

			out, _, evalErr := prog.Eval(map[string]any{"doc": tt.doc})
			if tt.wantErr != "" {
				require.Error(t, evalErr)
				assert.Contains(t, evalErr.Error(), tt.wantErr)
				return
			}
			require.NoError(t, evalErr)
			assert.Equal(t, tt.want, conversion.CelValueToGo(out))
		})
	}
}

func TestJSONC_CostTrackingAndEstimation(t *testing.T) {
	t.Parallel()

	unmarshalFn := JSONUnmarshalJSONCFunc()
	stripFn := JSONStripCommentsFunc()
	opts := []cel.EnvOption{
		unmarshalFn.EnvOptions[0],
		stripFn.EnvOptions[0],
		cel.Variable("doc", cel.StringType),
	}

	env, err := cel.NewEnv(opts...)
	require.NoError(t, err)

	for _, exprStr := range []string{
		`json.unmarshalJSONC('{"a": 1, /* comment */}')`,
		`json.stripComments('{"a": 1, /* comment */}')`,
		`json.unmarshalJSONC('{"emoji": "\U0001F600\U0001F600\U0001F600"}')`,
		`json.stripComments('{"emoji": "\U0001F600\U0001F600"} /* \U0001F600 */')`,
	} {
		ast, iss := env.Compile(exprStr)
		require.NoError(t, iss.Err())
		est, estErr := env.EstimateCost(ast, checker.CostEstimator(nil))
		require.NoError(t, estErr)
		assert.Greater(t, est.Max, uint64(1))

		prog, progErr := env.Program(ast, cel.CostTracking(nil))
		require.NoError(t, progErr)
		_, details, evalErr := prog.Eval(cel.NoVars())
		require.NoError(t, evalErr)
		require.NotNil(t, details)
		require.NotNil(t, details.ActualCost())
		assert.LessOrEqual(t, est.Min, *details.ActualCost())
		assert.GreaterOrEqual(t, est.Max, *details.ActualCost())
	}

	// Runtime cost scales linearly with input size and trips a tight limit on a large payload.
	var sb strings.Builder
	sb.WriteString("{\n")
	for i := 0; i < 400; i++ {
		sb.WriteString("  // repeated configuration comment line\n")
	}
	sb.WriteString("  \"ok\": true,\n}\n")
	largeDoc := sb.String()

	for _, exprStr := range []string{`json.unmarshalJSONC(doc)`, `json.stripComments(doc)`} {
		expr := celexp.Expression(exprStr)
		tightProg, compileErr := expr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(50))
		require.NoError(t, compileErr)
		_, evalErr := tightProg.Eval(map[string]any{"doc": largeDoc})
		require.Error(t, evalErr)
		assert.Contains(t, evalErr.Error(), "cost limit exceeded")

		generousProg, compileErr := expr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(100_000))
		require.NoError(t, compileErr)
		val, evalErr := generousProg.Eval(map[string]any{"doc": largeDoc})
		require.NoError(t, evalErr)
		require.NotNil(t, val)
	}
}

func FuzzStripJSONC(f *testing.F) {
	seeds := []string{
		"",
		"\uFEFF{}",
		`{"url": "https://example.com//a/*b*/",}`,
		`[1, 2, /* comment */]`,
		`{"escaped": "a\\\"b\\"}`,
		`/* unterminated`,
		`[,]`,
		`[1,,]`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		cleaned, err := StripJSONC(input)
		if err != nil {
			assert.ErrorIs(t, err, ErrUnterminatedBlockComment)
			return
		}
		// Any already-valid compact JSON value must parse identically after StripJSONC.
		var orig any
		if json.Unmarshal([]byte(input), &orig) == nil {
			var after any
			require.NoError(t, json.Unmarshal([]byte(cleaned), &after))
			assert.Equal(t, orig, after)
		}
	})
}

func BenchmarkStripJSONC(b *testing.B) {
	sample := "{\n  // server settings\n  \"url\": \"https://example.com//v1/*api*/\",\n  \"ports\": [8080, 8443,], /* trailing */\n}\n"
	b.ReportAllocs()
	for b.Loop() {
		_, _ = StripJSONC(sample)
	}
}

func BenchmarkJSONUnmarshalJSONC_CEL(b *testing.B) {
	fn := JSONUnmarshalJSONCFunc()
	env, _ := cel.NewEnv(fn.EnvOptions[0], cel.Variable("doc", cel.StringType))
	ast, _ := env.Compile(`json.unmarshalJSONC(doc)`)
	prog, _ := env.Program(ast)
	vars := map[string]any{
		"doc": "{\n  // server settings\n  \"url\": \"https://example.com//v1/*api*/\",\n  \"ports\": [8080, 8443,],\n}\n",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = prog.Eval(vars)
	}
}
