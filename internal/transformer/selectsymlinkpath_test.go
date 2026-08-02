package transformer

import (
	"path/filepath"
	"testing"
)

func TestSelectSymlinkPath(t *testing.T) {
	tests := []struct {
		name            string
		symlinkPaths    []string
		nodeModulesPath string
		want            string
	}{
		{
			name:            "project node_modules wins",
			symlinkPaths:    []string{"/cache/pkg", "/project/node_modules/pkg"},
			nodeModulesPath: "/project/node_modules",
			want:            "/project/node_modules/pkg",
		},
		{
			name:            "first cache entry is fallback",
			symlinkPaths:    []string{"/cache/first", "/cache/second"},
			nodeModulesPath: "/project/node_modules",
			want:            "/cache/first",
		},
		{
			name:            "node_modules prefix without directory boundary is cache entry",
			symlinkPaths:    []string{"/project/node_modules-cache/pkg", "/cache/first"},
			nodeModulesPath: "/project/node_modules",
			want:            "/project/node_modules-cache/pkg",
		},
		{
			name:            "empty cache",
			symlinkPaths:    []string{},
			nodeModulesPath: "/project/node_modules",
			want:            "",
		},
		{
			name:            "nil cache",
			symlinkPaths:    nil,
			nodeModulesPath: "/project/node_modules",
			want:            "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := selectSymlinkPath(test.symlinkPaths, test.nodeModulesPath)
			if got != test.want {
				t.Fatalf("selectSymlinkPath() = %q, want %q", got, test.want)
			}
		})
	}

	projectPath := filepath.Join("project", "node_modules")
	if got := selectSymlinkPath([]string{"cache/pkg", filepath.Join(projectPath, "pkg")}, projectPath); got != filepath.Join(projectPath, "pkg") {
		t.Fatalf("relative project symlink = %q, want project node_modules path", got)
	}
}
