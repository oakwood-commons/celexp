// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

// Package pathnorm provides cross-platform file-path normalization used by the
// filepath CEL extension. It is a dependency-free copy of the single helper the
// CEL engine needs, so the library does not pull in the larger scafctl filepath
// package (which depends on additional filesystem helpers).
package pathnorm

import "strings"

// NormalizeFilePath normalizes a file path by removing any prefix before a colon (':'),
// and replacing all backslashes ('\') with forward slashes ('/'). This is useful for
// ensuring consistent file path formatting across different operating systems.
func NormalizeFilePath(path string) string {
	if strings.Contains(path, ":") {
		path = strings.Split(path, ":")[1]
	}
	return strings.ReplaceAll(path, "\\", "/")
}
