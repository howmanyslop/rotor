package conformance

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestDiagnosticsCorpus(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "testdata", "conformance", "excluded", "diagnostics")

	cases, err := loadDiagnosticFixtures(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no diagnostic fixtures found")
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if reason := skipReasonForDiagnosticFixture(tc); reason != "" {
				t.Skip(reason)
			}
			got, err := compileDiagnosticFixture(root, tc.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.Exact {
				if !slices.Equal(got, tc.ExpectedIDs) {
					t.Fatalf("got diagnostics %v, want %v", got, tc.ExpectedIDs)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("expected diagnostic %q, got none", tc.ExpectedIDs[0])
			}
			for _, id := range got {
				if id != tc.ExpectedIDs[0] {
					t.Fatalf("expected only %q, got %v", tc.ExpectedIDs[0], got)
				}
			}
		})
	}
}

func TestNoRobloxSymbolInstanceofFixture(t *testing.T) {
	root := repoRoot(t)
	fixture := filepath.Join(root, "testdata", "conformance", "excluded", "diagnostics", "noRobloxSymbolInstanceof.1.ts")
	got, err := compileDiagnosticFixture(root, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"noRobloxSymbolInstanceof"}) {
		t.Fatalf("got diagnostics %v, want [noRobloxSymbolInstanceof]", got)
	}
}

func TestRojoTopologyDiagnosticFixtures(t *testing.T) {
	root := repoRoot(t)
	tests := []struct {
		name string
		want string
	}{
		{name: "noIsolatedImport.ts", want: "noIsolatedImport"},
		{name: "noRojoData.ts", want: "noRojoData"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := filepath.Join(root, "testdata", "conformance", "excluded", "diagnostics", tt.name)
			got, err := compileDiagnosticFixture(root, fixture)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, []string{tt.want}) {
				t.Fatalf("got diagnostics %v, want [%s]", got, tt.want)
			}
		})
	}
}
