// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package marshalling

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker"
	celext "github.com/google/cel-go/ext"
	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/conversion"
	celmap "github.com/oakwood-commons/celexp/ext/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrontmatterFuncs_Metadata(t *testing.T) {
	t.Parallel()

	parseFn := FrontmatterParseFunc()
	formatFn := FrontmatterFormatFunc()

	assert.Equal(t, "frontmatter.parse", parseFn.Name)
	assert.Equal(t, "frontmatter.parse(string) -> map<string, dyn>", parseFn.Signature)
	assert.NotEmpty(t, parseFn.Description)
	assert.Equal(t, []string{"frontmatter.parse"}, parseFn.FunctionNames)
	assert.True(t, parseFn.Custom)
	assert.NotEmpty(t, parseFn.Examples)
	assert.Len(t, parseFn.EnvOptions, 1)

	assert.Equal(t, "frontmatter.format", formatFn.Name)
	assert.Equal(t, "frontmatter.format(map<string, dyn>, string) -> string", formatFn.Signature)
	assert.NotEmpty(t, formatFn.Description)
	assert.Equal(t, []string{"frontmatter.format"}, formatFn.FunctionNames)
	assert.True(t, formatFn.Custom)
	assert.NotEmpty(t, formatFn.Examples)
	assert.Len(t, formatFn.EnvOptions, 1)

	env, err := cel.NewEnv(
		parseFn.EnvOptions[0],
		formatFn.EnvOptions[0],
		celmap.AddFunc().EnvOptions[0],
		celext.Bindings(celext.BindingsVersion(1)),
	)
	require.NoError(t, err)

	for _, ex := range append(parseFn.Examples, formatFn.Examples...) {
		ast, iss := env.Compile(ex.Expression)
		require.NoError(t, iss.Err(), "example %q failed to compile", ex.Expression)
		prog, progErr := env.Program(ast)
		require.NoError(t, progErr)
		_, _, evalErr := prog.Eval(cel.NoVars())
		require.NoError(t, evalErr, "example %q failed to evaluate", ex.Expression)
	}
}

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		doc      string
		wantHas  bool
		wantMeta map[string]any
		wantBody string
		wantErr  string
	}{
		{
			name:     "lf frontmatter and markdown body",
			doc:      "---\ntitle: Skill Guide\ntags:\n  - cel\n  - yaml\n---\n# Heading\n\nParagraph.\n",
			wantHas:  true,
			wantMeta: map[string]any{"title": "Skill Guide", "tags": []any{"cel", "yaml"}},
			wantBody: "# Heading\n\nParagraph.\n",
		},
		{
			name:     "crlf frontmatter and body",
			doc:      "---\r\ntitle: Windows Doc\r\ndraft: false\r\n---\r\n\r\n# Body\r\n",
			wantHas:  true,
			wantMeta: map[string]any{"title": "Windows Doc", "draft": false},
			wantBody: "\r\n# Body\r\n",
		},
		{
			name:     "utf8 bom with frontmatter",
			doc:      "\uFEFF---\ntitle: BOM Doc\n---\nBody text",
			wantHas:  true,
			wantMeta: map[string]any{"title": "BOM Doc"},
			wantBody: "Body text",
		},
		{
			name:     "dot dot dot closing fence and trailing fence spaces",
			doc:      "---   \ntitle: Dots\n...\t \nTrailing body",
			wantHas:  true,
			wantMeta: map[string]any{"title": "Dots"},
			wantBody: "Trailing body",
		},
		{
			name:     "closing fence at eof without trailing newline",
			doc:      "---\ntitle: Only Meta\n---",
			wantHas:  true,
			wantMeta: map[string]any{"title": "Only Meta"},
			wantBody: "",
		},
		{
			name:     "empty frontmatter block",
			doc:      "---\n---\n# Empty Meta\n",
			wantHas:  true,
			wantMeta: map[string]any{},
			wantBody: "# Empty Meta\n",
		},
		{
			name:     "empty frontmatter block crlf",
			doc:      "---\r\n---\r\n# Empty Meta\r\n",
			wantHas:  true,
			wantMeta: map[string]any{},
			wantBody: "# Empty Meta\r\n",
		},
		{
			name:     "comment only frontmatter block",
			doc:      "---\n# comment only\n---\nBody\n",
			wantHas:  true,
			wantMeta: map[string]any{},
			wantBody: "Body\n",
		},
		{
			name:     "indented dashes inside multiline yaml scalar are not closing fence",
			doc:      "---\nsummary: |\n  first line\n  ---\n  second line\n---\nBody\n",
			wantHas:  true,
			wantMeta: map[string]any{"summary": "first line\n---\nsecond line\n"},
			wantBody: "Body\n",
		},
		{
			name:     "plain markdown without frontmatter",
			doc:      "# Plain Heading\n\n---\nSection break\n",
			wantHas:  false,
			wantMeta: map[string]any{},
			wantBody: "# Plain Heading\n\n---\nSection break\n",
		},
		{
			name:     "empty document",
			doc:      "",
			wantHas:  false,
			wantMeta: map[string]any{},
			wantBody: "",
		},
		{
			name:     "four dashes thematic break at top is not frontmatter",
			doc:      "----\ntitle: no\n----\nBody",
			wantHas:  false,
			wantMeta: map[string]any{},
			wantBody: "----\ntitle: no\n----\nBody",
		},
		{
			name:     "leading space before dashes is not frontmatter",
			doc:      " ---\ntitle: no\n---\nBody",
			wantHas:  false,
			wantMeta: map[string]any{},
			wantBody: " ---\ntitle: no\n---\nBody",
		},
		{
			name:     "unclosed opening fence treated as plain document",
			doc:      "---\ntitle: unclosed\n# Body without closing fence",
			wantHas:  false,
			wantMeta: map[string]any{},
			wantBody: "---\ntitle: unclosed\n# Body without closing fence",
		},
		{
			name:    "scalar yaml in frontmatter returns clear error",
			doc:     "---\njust a string\n---\nBody",
			wantErr: "frontmatter must be a YAML mapping",
		},
		{
			name:    "sequence yaml in frontmatter returns clear error",
			doc:     "---\n- item1\n- item2\n---\nBody",
			wantErr: "frontmatter must be a YAML mapping",
		},
		{
			name:    "null yaml in frontmatter returns clear error",
			doc:     "---\nnull\n---\nBody",
			wantErr: "frontmatter must be a YAML mapping",
		},
		{
			name:    "invalid yaml syntax returns error",
			doc:     "---\ntitle: [unterminated\n---\nBody",
			wantErr: "yaml:",
		},
		{
			name:     "valid yaml merge mapping and sequence of string-keyed mappings",
			doc:      "---\ndefaults: &def\n  env: prod\nextra: &ext\n  region: us\n<<: [*def, *ext]\nname: svc\n---\nBody",
			wantHas:  true,
			wantMeta: map[string]any{"defaults": map[string]any{"env": "prod"}, "extra": map[string]any{"region": "us"}, "env": "prod", "region": "us", "name": "svc"},
			wantBody: "Body",
		},
		{
			name:    "top-level integer key rejected before merge coercion",
			doc:     "---\n1: numeric\n<<: {\"1\": text}\n---\nBody",
			wantErr: "frontmatter top-level keys must be strings",
		},
		{
			name:    "merged non-string top-level key rejected",
			doc:     "---\n\"1\": text\n<<: {1: numeric}\n---\nBody",
			wantErr: "frontmatter top-level keys must be strings",
		},
		{
			name:    "sequence merge with non-string top-level key rejected",
			doc:     "---\n<<: [{\"ok\": 1}, {2: numeric}]\n---\nBody",
			wantErr: "frontmatter top-level keys must be strings",
		},
		{
			name:    "nested nan yaml map key rejected at parse time",
			doc:     "---\nvalues:\n  .nan: value\n---\nBody",
			wantErr: "map key cannot be NaN",
		},
		{
			name:    "non-scalar nested yaml mapping key rejected cleanly",
			doc:     "---\nnested:\n  ? [1, 2]\n  : value\n---\nBody",
			wantErr: "unsupported non-scalar YAML mapping key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotHas, gotMeta, gotBody, err := ParseFrontmatter(tt.doc)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantHas, gotHas)
			assert.Equal(t, tt.wantMeta, gotMeta)
			assert.Equal(t, tt.wantBody, gotBody)
		})
	}
}

func TestFrontmatter_CELRoundTripAndEdgeCases(t *testing.T) {
	t.Parallel()

	parseFn := FrontmatterParseFunc()
	formatFn := FrontmatterFormatFunc()
	env, err := cel.NewEnv(
		parseFn.EnvOptions[0],
		formatFn.EnvOptions[0],
		celext.Bindings(celext.BindingsVersion(1)),
		cel.Variable("doc", cel.StringType),
		cel.Variable("meta", cel.DynType),
		cel.Variable("body", cel.StringType),
	)
	require.NoError(t, err)

	roundTripCases := []struct {
		name string
		meta map[string]any
		body string
	}{
		{
			name: "standard metadata and lf body",
			meta: map[string]any{"name": "my-skill", "version": int64(2), "active": true},
			body: "\n# Overview\n\nDetails here.\n",
		},
		{
			name: "crlf body preserved across round trip",
			meta: map[string]any{"title": "CRLF Doc"},
			body: "\r\n# Section\r\nLine two.\r\n",
		},
		{
			name: "empty metadata map round trips with hasFrontmatter true",
			meta: map[string]any{},
			body: "# Body Only\n",
		},
		{
			name: "empty metadata prevents body fence smuggling",
			meta: map[string]any{},
			body: "---\nsmuggled: true\n---\nReal body\n",
		},
		{
			name: "metadata values containing fence strings and nulls",
			meta: map[string]any{
				"divider":   "---",
				"multiline": "before\n---\nafter\n...\nend",
				"optional":  nil,
			},
			body: "Content\n",
		},
	}

	for _, tc := range roundTripCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ast, iss := env.Compile(`frontmatter.parse(frontmatter.format(meta, body))`)
			require.NoError(t, iss.Err())
			prog, progErr := env.Program(ast)
			require.NoError(t, progErr)

			out, _, evalErr := prog.Eval(map[string]any{
				"meta": tc.meta,
				"body": tc.body,
			})
			require.NoError(t, evalErr)

			got, ok := conversion.CelValueToGo(out).(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, got["hasFrontmatter"])
			assert.Equal(t, tc.body, got["body"])

			gotMeta, ok := got["meta"].(map[string]any)
			require.True(t, ok)
			assert.Len(t, gotMeta, len(tc.meta))
			for k, wantVal := range tc.meta {
				if wantInt, isInt64 := wantVal.(int64); isInt64 {
					// yaml.v3 decodes small integers as int in Go; both adapt to CEL int64.
					assert.EqualValues(t, wantInt, gotMeta[k])
				} else {
					assert.Equal(t, wantVal, gotMeta[k])
				}
			}
		})
	}

	t.Run("parse then format crlf input round-trips", func(t *testing.T) {
		t.Parallel()

		crlfDoc := "---\r\nname: agent\r\n---\r\n# Instructions\r\nDo work.\r\n"
		ast, iss := env.Compile(`cel.bind(p, frontmatter.parse(doc), frontmatter.parse(frontmatter.format(p.meta, p.body)))`)
		require.NoError(t, iss.Err())
		prog, progErr := env.Program(ast)
		require.NoError(t, progErr)

		out, _, evalErr := prog.Eval(map[string]any{"doc": crlfDoc})
		require.NoError(t, evalErr)
		got := conversion.CelValueToGo(out).(map[string]any)
		assert.Equal(t, true, got["hasFrontmatter"])
		assert.Equal(t, map[string]any{"name": "agent"}, got["meta"])
		assert.Equal(t, "# Instructions\r\nDo work.\r\n", got["body"])
	})

	t.Run("nested non-string yaml map keys survive parse and format round-trip", func(t *testing.T) {
		t.Parallel()

		nestedDoc := "---\nports:\n  80: http\n  443: https\n  \"8080\": alt\nflags:\n  true: enabled\n---\n# Service\n"
		ast, iss := env.Compile(`cel.bind(p, frontmatter.parse(doc), frontmatter.format(p.meta, p.body))`)
		require.NoError(t, iss.Err())
		prog, progErr := env.Program(ast)
		require.NoError(t, progErr)

		out, _, evalErr := prog.Eval(map[string]any{"doc": nestedDoc})
		require.NoError(t, evalErr)
		formatted, ok := out.Value().(string)
		require.True(t, ok)

		hasFM, rtMeta, rtBody, err := ParseFrontmatter(formatted)
		require.NoError(t, err)
		assert.True(t, hasFM)
		assert.Equal(t, "# Service\n", rtBody)

		anyPorts, anyOK := rtMeta["ports"].(map[any]any)
		require.True(t, anyOK, "expected ports map[any]any, got %T", rtMeta["ports"])
		assert.Equal(t, "http", anyPorts[int64(80)])
		assert.Equal(t, "https", anyPorts[int64(443)])
		assert.Equal(t, "alt", anyPorts["8080"])

		// Verify CEL can directly index integer keys in parsed frontmatter metadata.
		idxAST, idxIss := env.Compile(`frontmatter.parse(doc).meta.ports[80] == "http"`)
		require.NoError(t, idxIss.Err())
		idxProg, idxProgErr := env.Program(idxAST)
		require.NoError(t, idxProgErr)
		idxOut, _, idxEvalErr := idxProg.Eval(map[string]any{"doc": nestedDoc})
		require.NoError(t, idxEvalErr)
		assert.Equal(t, true, idxOut.Value())

		// Also verify direct CEL map literal with integer keys inside meta.
		litAST, litIss := env.Compile(`frontmatter.format({"ports": {80: "http", 443: "https"}}, "# Direct\n")`)
		require.NoError(t, litIss.Err())
		litProg, litProgErr := env.Program(litAST)
		require.NoError(t, litProgErr)
		litOut, _, litEvalErr := litProg.Eval(cel.NoVars())
		require.NoError(t, litEvalErr)
		assert.Contains(t, litOut.Value().(string), "80: http")
		assert.Contains(t, litOut.Value().(string), "443: https")

		// Verify EvalAs[map[string]any] preserves nested non-string keys.
		compiledParse, compErr := celexp.Expression(`frontmatter.parse(doc)`).Compile(
			[]cel.EnvOption{parseFn.EnvOptions[0], cel.Variable("doc", cel.StringType)},
			celexp.WithCache(celexp.NewProgramCache(10)),
		)
		require.NoError(t, compErr)
		evalAsMap, evalAsErr := celexp.EvalAs[map[string]any](compiledParse, map[string]any{"doc": nestedDoc})
		require.NoError(t, evalAsErr)
		evalAsMeta, metaOK := evalAsMap["meta"].(map[string]any)
		require.True(t, metaOK)
		assert.Equal(t, map[any]any{
			int64(80):  "http",
			int64(443): "https",
			"8080":     "alt",
		}, evalAsMeta["ports"])
		assert.Equal(t, map[any]any{true: "enabled"}, evalAsMeta["flags"])
	})

	t.Run("non-string map key via dyn returns error", func(t *testing.T) {
		t.Parallel()

		ast, iss := env.Compile(`frontmatter.format(meta, "body")`)
		require.NoError(t, iss.Err())
		prog, progErr := env.Program(ast)
		require.NoError(t, progErr)

		_, _, evalErr := prog.Eval(map[string]any{
			"meta": map[int64]any{1: "bad"},
		})
		require.Error(t, evalErr)
		assert.Contains(t, evalErr.Error(), "frontmatter.format")
	})
}

func TestFrontmatter_CyclesAndNestingDepth(t *testing.T) {
	t.Parallel()

	formatFn := FrontmatterFormatFunc()
	parseFn := FrontmatterParseFunc()
	env, err := cel.NewEnv(
		formatFn.EnvOptions[0],
		parseFn.EnvOptions[0],
		cel.Variable("meta", cel.DynType),
	)
	require.NoError(t, err)

	ast, iss := env.Compile(`frontmatter.format(meta, "")`)
	require.NoError(t, iss.Err())
	prog, err := env.Program(ast)
	require.NoError(t, err)

	t.Run("self-referential top-level map rejected", func(t *testing.T) {
		t.Parallel()

		m := map[string]any{}
		m["self"] = m

		_, _, evalErr := prog.Eval(map[string]any{"meta": m})
		require.Error(t, evalErr)
		assert.Contains(t, evalErr.Error(), ErrFrontmatterCyclicValue.Error())

		_, goErr := FormatFrontmatter(m, "")
		require.ErrorIs(t, goErr, ErrFrontmatterCyclicValue)
	})

	t.Run("indirect map cycle and map-slice-map cycle rejected", func(t *testing.T) {
		t.Parallel()

		a := map[string]any{}
		b := map[string]any{"back": a}
		a["next"] = b
		_, _, evalErr := prog.Eval(map[string]any{"meta": a})
		require.Error(t, evalErr)
		assert.Contains(t, evalErr.Error(), ErrFrontmatterCyclicValue.Error())

		m := map[string]any{}
		m["items"] = []any{m}
		_, _, listMapErr := prog.Eval(map[string]any{"meta": m})
		require.Error(t, listMapErr)
		assert.Contains(t, listMapErr.Error(), ErrFrontmatterCyclicValue.Error())
	})

	t.Run("self-referential slice and map[any]any cycle rejected", func(t *testing.T) {
		t.Parallel()

		s := make([]any, 1)
		s[0] = s
		_, _, sliceErr := prog.Eval(map[string]any{"meta": map[string]any{"items": s}})
		require.Error(t, sliceErr)
		assert.Contains(t, sliceErr.Error(), ErrFrontmatterCyclicValue.Error())

		anyM := map[any]any{}
		anyM[int64(1)] = anyM
		_, _, anyMapErr := prog.Eval(map[string]any{"meta": map[string]any{"ports": anyM}})
		require.Error(t, anyMapErr)
		assert.Contains(t, anyMapErr.Error(), ErrFrontmatterCyclicValue.Error())
	})

	t.Run("shared acyclic maps and slices succeed", func(t *testing.T) {
		t.Parallel()

		sharedMap := map[string]any{"host": "localhost"}
		sharedAnyMap := map[any]any{int64(80): "http"}
		sharedSlice := []any{"cel", "yaml"}
		meta := map[string]any{
			"primary":   sharedMap,
			"secondary": sharedMap,
			"portsA":    sharedAnyMap,
			"portsB":    sharedAnyMap,
			"tagsA":     sharedSlice,
			"tagsB":     sharedSlice,
		}

		out, _, evalErr := prog.Eval(map[string]any{"meta": meta})
		require.NoError(t, evalErr)
		formatted, ok := out.Value().(string)
		require.True(t, ok)

		hasFM, parsedMeta, _, parseErr := ParseFrontmatter(formatted)
		require.NoError(t, parseErr)
		assert.True(t, hasFM)
		assert.Equal(t, map[string]any{"host": "localhost"}, parsedMeta["primary"])
		assert.Equal(t, map[string]any{"host": "localhost"}, parsedMeta["secondary"])
		assert.Equal(t, map[any]any{int64(80): "http"}, parsedMeta["portsA"])
		assert.Equal(t, map[any]any{int64(80): "http"}, parsedMeta["portsB"])
		assert.Equal(t, []any{"cel", "yaml"}, parsedMeta["tagsA"])
		assert.Equal(t, []any{"cel", "yaml"}, parsedMeta["tagsB"])
	})

	t.Run("excessive nesting depth rejected across maps and lists", func(t *testing.T) {
		t.Parallel()

		deepMap := map[string]any{"leaf": "val"}
		for range maxFrontmatterNestingDepth + 5 {
			deepMap = map[string]any{"nested": deepMap}
		}
		_, _, mapDepthErr := prog.Eval(map[string]any{"meta": deepMap})
		require.Error(t, mapDepthErr)
		assert.Contains(t, mapDepthErr.Error(), ErrFrontmatterMaxDepth.Error())
		_, goMapErr := FormatFrontmatter(deepMap, "")
		require.ErrorIs(t, goMapErr, ErrFrontmatterMaxDepth)

		var deepList any = "leaf"
		for range maxFrontmatterNestingDepth + 5 {
			deepList = []any{deepList}
		}
		_, _, listDepthErr := prog.Eval(map[string]any{"meta": map[string]any{"items": deepList}})
		require.Error(t, listDepthErr)
		assert.Contains(t, listDepthErr.Error(), ErrFrontmatterMaxDepth.Error())

		var deepAnyMap any = "leaf"
		for range maxFrontmatterNestingDepth + 5 {
			deepAnyMap = map[any]any{int64(1): deepAnyMap}
		}
		_, _, anyDepthErr := prog.Eval(map[string]any{"meta": map[string]any{"items": deepAnyMap}})
		require.Error(t, anyDepthErr)
		assert.Contains(t, anyDepthErr.Error(), ErrFrontmatterMaxDepth.Error())

		deepYAML := "---\na: " + strings.Repeat("[", maxFrontmatterNestingDepth+5) + "1" + strings.Repeat("]", maxFrontmatterNestingDepth+5) + "\n---\n"
		_, _, _, yamlDepthErr := ParseFrontmatter(deepYAML)
		require.ErrorIs(t, yamlDepthErr, ErrFrontmatterMaxDepth)
	})

	t.Run("exponential shared dag and large shared string output rejected by budget", func(t *testing.T) {
		t.Parallel()

		dag := map[string]any{"leaf": "val"}
		for range 25 {
			dag = map[string]any{"left": dag, "right": dag}
		}
		_, _, celDAGErr := prog.Eval(map[string]any{"meta": dag})
		require.Error(t, celDAGErr)
		assert.Contains(t, celDAGErr.Error(), ErrFrontmatterBudgetExceeded.Error())
		_, goDAGErr := FormatFrontmatter(dag, "")
		require.ErrorIs(t, goDAGErr, ErrFrontmatterBudgetExceeded)

		bigChunk := strings.Repeat("x", 100_000)
		ampMeta := make(map[string]any, 60)
		for i := range 60 {
			ampMeta[strings.Repeat("k", 1)+string(rune('A'+(i%26)))+string(rune('0'+(i/26)))] = bigChunk
		}
		_, _, celBytesErr := prog.Eval(map[string]any{"meta": ampMeta})
		require.Error(t, celBytesErr)
		assert.Contains(t, celBytesErr.Error(), ErrFrontmatterBudgetExceeded.Error())
		_, goBytesErr := FormatFrontmatter(ampMeta, "")
		require.ErrorIs(t, goBytesErr, ErrFrontmatterBudgetExceeded)
	})

	t.Run("duplicate normalized metadata keys rejected", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name   string
			nested map[any]any
		}{
			{
				name:   "int and int64",
				nested: map[any]any{int(1): "a", int64(1): "b"},
			},
			{
				name:   "uint and uint64",
				nested: map[any]any{uint(1): "a", uint64(1): "b"},
			},
			{
				name:   "float32 and float64",
				nested: map[any]any{float32(1.5): "a", float64(1.5): "b"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				meta := map[string]any{"ports": tc.nested}
				_, _, celErr := prog.Eval(map[string]any{"meta": meta})
				require.Error(t, celErr)
				assert.Contains(t, celErr.Error(), ErrFrontmatterDuplicateKey.Error())

				_, goErr := FormatFrontmatter(meta, "")
				require.ErrorIs(t, goErr, ErrFrontmatterDuplicateKey)
			})
		}
	})
}

func TestFrontmatter_CostTrackingAndEstimation(t *testing.T) {
	t.Parallel()

	parseFn := FrontmatterParseFunc()
	formatFn := FrontmatterFormatFunc()
	opts := []cel.EnvOption{
		parseFn.EnvOptions[0],
		formatFn.EnvOptions[0],
		cel.Variable("doc", cel.StringType),
	}

	env, err := cel.NewEnv(opts...)
	require.NoError(t, err)

	for _, exprStr := range []string{
		`frontmatter.parse("---\ntitle: a\n---\nbody")`,
		`frontmatter.format({"title": "a"}, "body")`,
	} {
		ast, iss := env.Compile(exprStr)
		require.NoError(t, iss.Err())
		est, estErr := env.EstimateCost(ast, checker.CostEstimator(nil))
		require.NoError(t, estErr)
		assert.Greater(t, est.Max, uint64(1))
	}

	largeBody := strings.Repeat("markdown paragraph line\n", 500)
	largeDoc := "---\ntitle: test\n---\n" + largeBody

	parseExpr := celexp.Expression(`frontmatter.parse(doc)`)
	tightParseProg, err := parseExpr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(40))
	require.NoError(t, err)
	_, err = tightParseProg.Eval(map[string]any{"doc": largeDoc})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	formatOpts := append([]cel.EnvOption{}, opts...)
	formatOpts = append(formatOpts,
		cel.Variable("meta", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("body", cel.StringType),
	)
	formatExpr := celexp.Expression(`frontmatter.format(meta, body)`)
	tightFormatProg, err := formatExpr.Compile(formatOpts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(40))
	require.NoError(t, err)
	_, err = tightFormatProg.Eval(map[string]any{
		"meta": map[string]any{"title": "test", "owner": "docs"},
		"body": largeBody,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	generousFormatProg, err := formatExpr.Compile(formatOpts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(100_000))
	require.NoError(t, err)
	formattedVal, err := generousFormatProg.Eval(map[string]any{
		"meta": map[string]any{"title": "test", "owner": "docs"},
		"body": largeBody,
	})
	require.NoError(t, err)
	assert.Contains(t, formattedVal, "title: test")

	// Static cost estimate for frontmatter.format must never underestimate
	// runtime cost when a single-entry metadata map holds a large value.
	largeLiteralAST, largeIss := env.Compile(`frontmatter.format({"big": "` + strings.Repeat("x", 5000) + `"}, "")`)
	require.NoError(t, largeIss.Err())
	staticEst, staticErr := env.EstimateCost(largeLiteralAST, checker.CostEstimator(nil))
	require.NoError(t, staticErr)
	assert.Equal(t, uint64(math.MaxUint64), staticEst.Max)

	trackedProg, trackedErr := env.Program(largeLiteralAST, cel.CostTracking(nil))
	require.NoError(t, trackedErr)
	_, details, evalErr := trackedProg.Eval(cel.NoVars())
	require.NoError(t, evalErr)
	require.NotNil(t, details)
	require.NotNil(t, details.ActualCost())
	assert.LessOrEqual(t, staticEst.Min, *details.ActualCost())
	assert.GreaterOrEqual(t, staticEst.Max, *details.ActualCost())

	// Masked formatting errors on large shared graphs inside comprehensions must
	// still charge traversal work so || true cannot evade runtime cost limits.
	dag := map[string]any{"leaf": "val"}
	for range 25 {
		dag = map[string]any{"left": dag, "right": dag}
	}
	maskedOpts := []cel.EnvOption{
		formatFn.EnvOptions[0],
		cel.Variable("meta", cel.MapType(cel.StringType, cel.DynType)),
	}
	maskedExpr := celexp.Expression(`[1, 2, 3].all(i, frontmatter.format(meta, "") == "" || true)`)
	maskedProg, err := maskedExpr.Compile(maskedOpts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(150_000))
	require.NoError(t, err)
	_, err = maskedProg.Eval(map[string]any{"meta": dag})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	// Static cost estimate for frontmatter.parse keeps Max conservative when
	// YAML aliases expand a compact input, and brackets actual runtime cost.
	buildAliasDoc := func(levels int) string {
		var sb strings.Builder
		sb.WriteString("---\na0: &a0 [x, x]\n")
		for i := 1; i <= levels; i++ {
			_, _ = fmt.Fprintf(&sb, "a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1)
		}
		sb.WriteString("---\nbody\n")
		return sb.String()
	}

	aliasLiteralDoc := buildAliasDoc(8)
	aliasParseAST, aliasParseIss := env.Compile(fmt.Sprintf("frontmatter.parse(%q)", aliasLiteralDoc))
	require.NoError(t, aliasParseIss.Err())
	parseStaticEst, parseStaticErr := env.EstimateCost(aliasParseAST, checker.CostEstimator(nil))
	require.NoError(t, parseStaticErr)
	assert.Equal(t, uint64(math.MaxUint64), parseStaticEst.Max)

	parseTrackedProg, parseTrackedErr := env.Program(aliasParseAST, cel.CostTracking(nil))
	require.NoError(t, parseTrackedErr)
	_, parseDetails, parseEvalErr := parseTrackedProg.Eval(cel.NoVars())
	require.NoError(t, parseEvalErr)
	require.NotNil(t, parseDetails)
	require.NotNil(t, parseDetails.ActualCost())
	assert.LessOrEqual(t, parseStaticEst.Min, *parseDetails.ActualCost())
	assert.GreaterOrEqual(t, parseStaticEst.Max, *parseDetails.ActualCost())

	// Succeeding alias-expanded frontmatter documents charge expanded traversal
	// work rather than input string length alone.
	var wideAliasSB strings.Builder
	wideAliasSB.WriteString("---\nbase: &base [")
	for i := range 200 {
		if i > 0 {
			wideAliasSB.WriteString(", ")
		}
		wideAliasSB.WriteString("x")
	}
	wideAliasSB.WriteString("]\n")
	for i := range 90 {
		_, _ = fmt.Fprintf(&wideAliasSB, "r%d: *base\n", i)
	}
	wideAliasSB.WriteString("---\nbody\n")
	succeedingAliasDoc := wideAliasSB.String()

	tightAliasProg, err := parseExpr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(5_000))
	require.NoError(t, err)
	_, err = tightAliasProg.Eval(map[string]any{"doc": succeedingAliasDoc})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	generousAliasProg, err := parseExpr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(100_000))
	require.NoError(t, err)
	parsedAliasVal, err := generousAliasProg.Eval(map[string]any{"doc": succeedingAliasDoc})
	require.NoError(t, err)
	require.NotNil(t, parsedAliasVal)

	// Masked alias-bomb parse failures inside comprehensions charge visited
	// node work so || true cannot evade runtime cost limits.
	aliasBombDoc := buildAliasDoc(18)
	maskedParseOpts := []cel.EnvOption{
		parseFn.EnvOptions[0],
		cel.Variable("doc", cel.StringType),
	}
	maskedParseExpr := celexp.Expression(`[1, 2, 3].all(i, frontmatter.parse(doc).hasFrontmatter || true)`)
	maskedParseProg, err := maskedParseExpr.Compile(maskedParseOpts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(150_000))
	require.NoError(t, err)
	_, err = maskedParseProg.Eval(map[string]any{"doc": aliasBombDoc})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost limit exceeded")

	// Tracked map results preserve full CEL map semantics (equality, comprehensions,
	// Size, and native Go conversion) and remain race-free across goroutines.
	mapSemExpr := celexp.Expression(`frontmatter.parse(doc) == {"hasFrontmatter": true, "meta": {"title": "a"}, "body": "body"} && frontmatter.parse(doc).exists(k, k == "meta") && size(frontmatter.parse(doc)) == 3`)
	mapSemProg, err := mapSemExpr.Compile(opts, celexp.WithCache(celexp.NewProgramCache(10)), celexp.WithCostLimit(10_000))
	require.NoError(t, err)
	semVal, err := celexp.EvalAs[bool](mapSemProg, map[string]any{"doc": "---\ntitle: a\n---\nbody"})
	require.NoError(t, err)
	assert.True(t, semVal)

	nativeMap, err := celexp.EvalAs[map[string]any](generousAliasProg, map[string]any{"doc": "---\ntitle: a\n---\nbody"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"hasFrontmatter": true,
		"meta":           map[string]any{"title": "a"},
		"body":           "body",
	}, nativeMap)
}

func FuzzParseFrontmatter(f *testing.F) {
	seeds := []string{
		"",
		"---\ntitle: hi\n---\nbody",
		"---\r\ntitle: hi\r\n---\r\nbody",
		"---\n---\n",
		"---\nunclosed",
		"\uFEFF---\na: 1\n...\ntext",
		"---\n[1, 2]\n---\n",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, doc string) {
		hasFM, meta, body, err := ParseFrontmatter(doc)
		if err != nil {
			return
		}
		if !hasFM {
			assert.Empty(t, meta)
			assert.Equal(t, doc, body)
			return
		}
		formatted, fmtErr := FormatFrontmatter(meta, body)
		require.NoError(t, fmtErr)
		rtHas, rtMeta, rtBody, rtErr := ParseFrontmatter(formatted)
		require.NoError(t, rtErr)
		assert.True(t, rtHas)
		assert.Len(t, rtMeta, len(meta))
		assert.Equal(t, body, rtBody)
	})
}

func BenchmarkFrontmatterParse_CEL(b *testing.B) {
	fn := FrontmatterParseFunc()
	env, _ := cel.NewEnv(fn.EnvOptions[0], cel.Variable("doc", cel.StringType))
	ast, _ := env.Compile(`frontmatter.parse(doc)`)
	prog, _ := env.Program(ast)
	vars := map[string]any{
		"doc": "---\nname: my-skill\ndescription: Parse frontmatter quickly\n---\n\n# Body\nSome text.\n",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = prog.Eval(vars)
	}
}

func BenchmarkFrontmatterFormat_CEL(b *testing.B) {
	fn := FrontmatterFormatFunc()
	env, _ := cel.NewEnv(
		fn.EnvOptions[0],
		cel.Variable("meta", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("body", cel.StringType),
	)
	ast, _ := env.Compile(`frontmatter.format(meta, body)`)
	prog, _ := env.Program(ast)
	vars := map[string]any{
		"meta": map[string]any{"name": "my-skill", "version": 1},
		"body": "# Body\nSome text.\n",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = prog.Eval(vars)
	}
}
