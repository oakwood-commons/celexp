// Copyright 2025-2026 Oakwood Commons
// SPDX-License-Identifier: Apache-2.0

package pathnorm

import "testing"

func TestNormalizeFilePath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "No colon, no backslash",
			path: "folder/file.txt",
			want: "folder/file.txt",
		},
		{
			name: "Windows backslashes",
			path: "folder\\file.txt",
			want: "folder/file.txt",
		},
		{
			name: "Colon prefix, no backslash",
			path: "prefix:folder/file.txt",
			want: "folder/file.txt",
		},
		{
			name: "Colon prefix, with backslashes",
			path: "prefix:folder\\file.txt",
			want: "folder/file.txt",
		},
		{
			name: "Multiple colons",
			path: "prefix:subprefix:folder\\file.txt",
			want: "subprefix",
		},
		{
			name: "Empty string",
			path: "",
			want: "",
		},
		{
			name: "Colon only",
			path: ":file.txt",
			want: "file.txt",
		},
		{
			name: "Backslash only",
			path: "\\file.txt",
			want: "/file.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeFilePath(tt.path)
			if got != tt.want {
				t.Errorf("NormalizeFilePath() = %v, want %v", got, tt.want)
			}
		})
	}
}
