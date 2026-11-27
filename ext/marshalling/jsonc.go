// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package marshalling

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/oakwood-commons/celexp"
	"github.com/oakwood-commons/celexp/conversion"
)

const utf8BOM = "\xef\xbb\xbf"

// ErrUnterminatedBlockComment is returned by StripJSONC when a /* block comment
// reaches the end of input without a matching closing */ delimiter.
var ErrUnterminatedBlockComment = errors.New("unterminated block comment")

// StripJSONC strips a leading UTF-8 BOM, // line comments, /* ... */ block
// comments, and trailing commas before } or ] in a single O(n) pass while
// leaving quoted JSON string literals untouched.
//
// Non-newline bytes inside /* ... */ block comments and stripped trailing
// commas are replaced with ASCII spaces, and all \r and \n line endings are
// preserved. Line numbers are preserved across all comment forms; byte offsets
// are also preserved across /* ... */ block comments and stripped trailing
// commas when no leading UTF-8 BOM or // line comments precede the position.
// Empty or consecutive commas such as [,] or [1,,] are left intact so standard
// JSON parsing rejects malformed inputs.
func StripJSONC(input string) (string, error) {
	input = strings.TrimPrefix(input, utf8BOM)
	n := len(input)
	if n == 0 {
		return "", nil
	}

	buf := make([]byte, 0, n)
	inString := false
	escaped := false
	canTrailComma := false
	pendingCommaPos := -1

	for i := 0; i < n; i++ {
		c := input[i]

		if inString {
			buf = append(buf, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
				canTrailComma = true
			}
			continue
		}

		// Line comment: //... up to \r, \n, or EOF.
		if c == '/' && i+1 < n && input[i+1] == '/' {
			i += 2
			for i < n && input[i] != '\n' && input[i] != '\r' {
				i++
			}
			if i < n {
				// Re-process the line ending byte in the outer loop.
				i--
			}
			continue
		}

		// Block comment: /*...*/ replacing non-line-ending bytes with spaces.
		if c == '/' && i+1 < n && input[i+1] == '*' {
			buf = append(buf, ' ', ' ')
			i += 2
			closed := false
			for i < n {
				if input[i] == '*' && i+1 < n && input[i+1] == '/' {
					buf = append(buf, ' ', ' ')
					i++
					closed = true
					break
				}
				if input[i] == '\n' || input[i] == '\r' {
					buf = append(buf, input[i])
				} else {
					buf = append(buf, ' ')
				}
				i++
			}
			if !closed {
				return "", ErrUnterminatedBlockComment
			}
			continue
		}

		// JSON whitespace does not affect trailing-comma state.
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			buf = append(buf, c)
			continue
		}

		switch c {
		case '"':
			pendingCommaPos = -1
			inString = true
			buf = append(buf, c)

		case ',', ':':
			if c == ':' {
				pendingCommaPos = -1
				canTrailComma = false
				buf = append(buf, c)
				continue
			}
			pos := len(buf)
			buf = append(buf, c)
			if canTrailComma && pendingCommaPos == -1 {
				pendingCommaPos = pos
				canTrailComma = false
			} else {
				pendingCommaPos = -1
				canTrailComma = false
			}

		case '{', '[':
			pendingCommaPos = -1
			canTrailComma = false
			buf = append(buf, c)

		case '}', ']':
			if pendingCommaPos != -1 {
				buf[pendingCommaPos] = ' '
				pendingCommaPos = -1
			}
			canTrailComma = true
			buf = append(buf, c)

		default:
			pendingCommaPos = -1
			canTrailComma = true
			buf = append(buf, c)
		}
	}

	return string(buf), nil
}

// JSONStripCommentsFunc returns a CEL function that strips JSONC comments,
// trailing commas, and a leading UTF-8 BOM from a JSONC string, returning
// standard JSON text.
//
// Example usage:
//
//	json.stripComments('{\n  // port config\n  "port": 8080,\n}')
func JSONStripCommentsFunc() celexp.ExtFunction {
	const (
		funcName   = "json.stripComments"
		overloadID = "json_stripComments"
	)
	estOpt, progOpt := unaryStringScanCostOptions(overloadID, marshallingTraversalFactor)

	lib := &funcLib{
		name: "cel.lib.ext.celexp.json.strip_comments",
		compileOpts: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(overloadID,
					[]*cel.Type{cel.StringType},
					cel.StringType,
					cel.UnaryBinding(func(value ref.Val) ref.Val {
						raw, ok := value.Value().(string)
						if !ok {
							return types.NewErr("json.stripComments: expected string, got %T", value.Value())
						}
						cleaned, err := StripJSONC(raw)
						if err != nil {
							return types.NewErr("json.stripComments: %s", err.Error())
						}
						return types.String(cleaned)
					}),
				),
			),
			estOpt,
		},
		programOpts: []cel.ProgramOption{progOpt},
	}

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "json.stripComments(string) -> string",
		Description:   "Strips UTF-8 BOM, // line comments, /* */ block comments, and trailing commas from a JSONC string while preserving string literals",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Strip comments and trailing commas from JSONC",
				Expression:  `json.stripComments('{"name": "app", /* inline */ "port": 8080,}')`,
			},
			{
				Description: "Preserve URL double slashes inside strings",
				Expression:  `json.stripComments('{"url": "https://example.com//path"} // endpoint')`,
			},
		},
		EnvOptions: []cel.EnvOption{cel.Lib(lib)},
	}
}

// JSONUnmarshalJSONCFunc returns a CEL function that parses a JSONC
// (JSON with Comments and trailing commas) string into a CEL value.
//
// Example usage:
//
//	json.unmarshalJSONC('{\n  // server port\n  "port": 8080,\n}')
func JSONUnmarshalJSONCFunc() celexp.ExtFunction {
	const (
		funcName   = "json.unmarshalJSONC"
		overloadID = "json_unmarshalJSONC"
	)
	estOpt, progOpt := unaryStringScanCostOptions(overloadID, 2*marshallingTraversalFactor)

	lib := &funcLib{
		name: "cel.lib.ext.celexp.json.unmarshal_jsonc",
		compileOpts: []cel.EnvOption{
			cel.Function(funcName,
				cel.Overload(overloadID,
					[]*cel.Type{cel.StringType},
					cel.DynType,
					cel.UnaryBinding(func(value ref.Val) ref.Val {
						raw, ok := value.Value().(string)
						if !ok {
							return types.NewErr("json.unmarshalJSONC: expected string, got %T", value.Value())
						}
						cleaned, err := StripJSONC(raw)
						if err != nil {
							return types.NewErr("json.unmarshalJSONC: %s", err.Error())
						}
						var result any
						if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
							return types.NewErr("json.unmarshalJSONC: %s", err.Error())
						}
						return conversion.GoToCelValue(result)
					}),
				),
			),
			estOpt,
		},
		programOpts: []cel.ProgramOption{progOpt},
	}

	return celexp.ExtFunction{
		Name:          funcName,
		Signature:     "json.unmarshalJSONC(string) -> dyn",
		Description:   "Unmarshals a JSONC string (supporting UTF-8 BOM, // and /* */ comments, and trailing commas) to a CEL value",
		FunctionNames: []string{funcName},
		Custom:        true,
		Examples: []celexp.Example{
			{
				Description: "Unmarshal JSONC with comments and trailing commas",
				Expression:  `json.unmarshalJSONC('{\n  // service settings\n  "host": "localhost",\n  "port": 8080,\n}')`,
			},
			{
				Description: "Preserve URLs containing // and /* inside strings",
				Expression:  `json.unmarshalJSONC('{"url": "https://example.com//path", /* trailing */ }').url`,
			},
		},
		EnvOptions: []cel.EnvOption{cel.Lib(lib)},
	}
}
