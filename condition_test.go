// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package celexp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// condDoc embeds a Condition below the document root so YAML errors carry a
// non-trivial position.
type condDoc struct {
	Name string    `json:"name" yaml:"name"`
	When Condition `json:"when" yaml:"when"`
}

func decodeCond(t *testing.T, format, input string) (condDoc, error) {
	t.Helper()
	var d condDoc
	var err error
	switch format {
	case "yaml":
		err = yaml.Unmarshal([]byte(input), &d)
	case "json":
		err = json.Unmarshal([]byte(input), &d)
	default:
		t.Fatalf("unknown format %q", format)
	}
	return d, err
}

func TestCondition_Unmarshal(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		input    string
		wantExpr *string // nil = want nil Expr
		wantErr  []string
	}{
		// YAML accepted forms.
		{name: "yaml bool true", format: "yaml", input: "name: a\nwhen: true", wantExpr: new("true")},
		{name: "yaml bool capitalized normalizes", format: "yaml", input: "when: False\nname: b", wantExpr: new("false")},
		{name: "yaml string shorthand", format: "yaml", input: `when: "_.env == 'prod'"`, wantExpr: new("_.env == 'prod'")},
		{name: "yaml flow expr object", format: "yaml", input: "name: c\nwhen: {expr: \"size(_.items) > 0\"}", wantExpr: new("size(_.items) > 0")},
		{name: "yaml block expression alias", format: "yaml", input: "when:\n  expression: _.count >= 3\n", wantExpr: new("_.count >= 3")},
		{name: "yaml anchor alias", format: "yaml", input: "name: &n \"_.ok\"\nwhen: *n", wantExpr: new("_.ok")},
		{name: "yaml null", format: "yaml", input: "name: d\nwhen: ~", wantExpr: nil},

		// YAML rejected shapes, with positions.
		{name: "yaml int", format: "yaml", input: "name: e\nwhen: 42", wantErr: []string{"line 2, column 7", "unsupported type !!int"}},
		{name: "yaml float", format: "yaml", input: "when: 1.5", wantErr: []string{"line 1, column 7", "unsupported type !!float"}},
		{name: "yaml sequence", format: "yaml", input: "name: f\nwhen:\n  - a\n  - b", wantErr: []string{"line 3, column 3", "unsupported node kind"}},
		{name: "yaml empty string", format: "yaml", input: `when: ""`, wantErr: []string{"line 1, column 7", "empty string"}},
		{name: "yaml both keys", format: "yaml", input: "name: g\nwhen: {expr: a, expression: b}", wantErr: []string{"line 2, column 7", "not both"}},
		{name: "yaml typo key", format: "yaml", input: "when:\n  exrp: _.x\n", wantErr: []string{"line 2, column 3", "unknown key \"exrp\""}},
		{name: "yaml empty expression", format: "yaml", input: "when:\n  expression: ''", wantErr: []string{"line 2, column 15", "empty string"}},
		{name: "yaml expr via anchor", format: "yaml", input: "name: &n \"_.x > 1\"\nwhen: {expr: *n}", wantExpr: new("_.x > 1")},
		{name: "yaml expr is a list", format: "yaml", input: "when: {expr: [a, b]}", wantErr: []string{"line 1, column 14", "must be a string or boolean"}},
		{name: "yaml expression is an int", format: "yaml", input: "name: m\nwhen: {expression: 5}", wantErr: []string{"line 2, column 20", "unsupported type !!int"}},
		{name: "yaml expr bool normalizes", format: "yaml", input: "when:\n  expr: TRUE\nname: n", wantExpr: new("true")},
		{name: "yaml expr null rejected", format: "yaml", input: "when: {expr: null}", wantErr: []string{"line 1, column 14", "\"expr\" must not be null"}},
		{name: "yaml null plus other key rejected", format: "yaml", input: "when: {expr: null, expression: \"true\"}", wantErr: []string{"must not be null"}},
		{name: "yaml unknown key rejected", format: "yaml", input: "when: {expr: \"false\", exrp: \"true\"}", wantErr: []string{"unknown key \"exrp\""}},
		{name: "json unknown key rejected", format: "json", input: `{"when":{"expr":"false","exrp":"true"}}`, wantErr: []string{"unknown key \"exrp\""}},
		{name: "json object bool value", format: "json", input: `{"when":{"expression":false}}`, wantExpr: new("false")},
		{name: "json object number rejected", format: "json", input: `{"when":{"expr":5}}`, wantErr: []string{"\"expr\" must be a string or boolean"}},
		{name: "json null plus other key rejected", format: "json", input: `{"when":{"expr":null,"expression":"true"}}`, wantErr: []string{"must not be null"}},
		{name: "yaml bad bool tag", format: "yaml", input: "when: !!bool maybe", wantErr: []string{"line 1, column 7"}},

		// JSON accepted forms.
		{name: "json bool false", format: "json", input: `{"name":"h","when":false}`, wantExpr: new("false")},
		{name: "json bool true", format: "json", input: `{"when": true}`, wantExpr: new("true")},
		{name: "json string shorthand", format: "json", input: `{"when":"_.tier in ['gold','silver']"}`, wantExpr: new("_.tier in ['gold','silver']")},
		{name: "json expr object", format: "json", input: `{"when":{"expr":"_.count > 5"},"name":"i"}`, wantExpr: new("_.count > 5")},
		{name: "json expression alias", format: "json", input: `{"when":{"expression":"has(_.owner)"}}`, wantExpr: new("has(_.owner)")},
		{name: "json null", format: "json", input: `{"name":"j","when":null}`, wantExpr: nil},

		// JSON rejected shapes.
		{name: "json number", format: "json", input: `{"when":42}`, wantErr: []string{"expected boolean, string, or object"}},
		{name: "json array", format: "json", input: `{"when":["a"]}`, wantErr: []string{"expected boolean, string, or object"}},
		{name: "json empty string", format: "json", input: `{"name":"k","when":""}`, wantErr: []string{"empty string"}},
		{name: "json both keys", format: "json", input: `{"when":{"expr":"a","expression":"b"}}`, wantErr: []string{"not both"}},
		{name: "json empty object", format: "json", input: `{"when":{}}`, wantErr: []string{"expected object with 'expr' or 'expression'"}},
		{name: "json typo key", format: "json", input: `{"when":{"exp":"_.x"}}`, wantErr: []string{"unknown key \"exp\""}},
		{name: "json empty expr", format: "json", input: `{"when":{"expr":""}}`, wantErr: []string{"empty string"}},
		{name: "json non-string expr", format: "json", input: `{"when":{"expression":7}}`, wantErr: []string{"\"expression\" must be a string or boolean"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := decodeCond(t, tt.format, tt.input)
			if len(tt.wantErr) > 0 {
				require.Error(t, err)
				for _, s := range tt.wantErr {
					assert.Contains(t, err.Error(), s)
				}
				return
			}
			require.NoError(t, err)
			if tt.wantExpr == nil {
				assert.Nil(t, d.When.Expr)
				return
			}
			require.NotNil(t, d.When.Expr)
			assert.Equal(t, *tt.wantExpr, string(*d.When.Expr))
		})
	}
}

func TestCondition_RoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		format   string
		input    string
		wantYAML string
		wantJSON string
	}{
		{name: "yaml true", format: "yaml", input: "when: true", wantYAML: "true\n", wantJSON: "true"},
		{name: "yaml TRUE normalized", format: "yaml", input: "when: TRUE", wantYAML: "true\n", wantJSON: "true"},
		{name: "yaml expression alias to shorthand", format: "yaml", input: "when: {expression: \"_.a && !_.b\"}", wantYAML: "_.a && !_.b\n", wantJSON: `"_.a \u0026\u0026 !_.b"`},
		{name: "yaml null", format: "yaml", input: "when: null", wantYAML: "null\n", wantJSON: "null"},
		{name: "json false", format: "json", input: `{"when":false}`, wantYAML: "false\n", wantJSON: "false"},
		{name: "json expr object to shorthand", format: "json", input: `{"when":{"expr":"_.env == 'prod'"}}`, wantYAML: "_.env == 'prod'\n", wantJSON: `"_.env == 'prod'"`},
		{name: "json string that looks like a number", format: "json", input: `{"when":"1 == 1"}`, wantYAML: "1 == 1\n", wantJSON: `"1 == 1"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := decodeCond(t, tt.format, tt.input)
			require.NoError(t, err)

			y, err := yaml.Marshal(d.When)
			require.NoError(t, err)
			assert.Equal(t, tt.wantYAML, string(y))

			j, err := json.Marshal(d.When)
			require.NoError(t, err)
			assert.Equal(t, tt.wantJSON, string(j))

			// The canonical output must decode back to the same condition.
			var fromYAML, fromJSON Condition
			require.NoError(t, yaml.Unmarshal(y, &fromYAML))
			require.NoError(t, json.Unmarshal(j, &fromJSON))
			assert.Equal(t, d.When, fromYAML)
			assert.Equal(t, d.When, fromJSON)
		})
	}
}
