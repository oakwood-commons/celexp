// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package pathnorm provides cross-platform file-path normalization used by the
// filepath CEL extension. It is a dependency-free copy of the single helper the
// CEL engine needs, so the library does not pull in the larger scafctl filepath
// package (which depends on additional filesystem helpers).
package pathnorm

import "strings"

// NormalizeFilePath normalizes a file path by removing any prefix up to and
// including the first colon (':'), and replacing all backslashes ('\') with
// forward slashes ('/'). This is useful for ensuring consistent file path
// formatting across different operating systems. Only the first colon is
// treated as the prefix separator, so any remaining colons in the path are
// preserved.
func NormalizeFilePath(path string) string {
	if strings.Contains(path, ":") {
		path = strings.SplitN(path, ":", 2)[1]
	}
	return strings.ReplaceAll(path, "\\", "/")
}
