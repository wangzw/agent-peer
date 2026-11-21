package main

import (
	"strings"
	"testing"
)

func project(rel string) string {
	return modulePath + "/" + rel
}

func TestNodeOf(t *testing.T) {
	tests := []struct {
		name       string
		importPath string
		want       string
		wantOK     bool
	}{
		{"localpb is its own node", project("internal/broker/localpb"), "localpb", true},
		{"deliverpb is its own node", project("internal/broker/deliverpb"), "deliverpb", true},
		{"broker itself is broker", project("internal/broker"), "broker", true},
		{"another package under broker is broker", project("internal/broker/internalstate"), "broker", true},
		{"mcpserver", project("internal/mcpserver"), "mcpserver", true},
		{"a proto subdirectory belongs to its module", project("internal/topic/topicpb"), "topic", true},
		{"link proto belongs to link", project("internal/link/linkpb"), "link", true},
		{"testkit", project("internal/testkit"), "testkit", true},
		{"cmd", project("cmd/peers"), "cmd", true},
		{"third_party is outside the graph", project("third_party/foo"), "", false},
		{"scripts are outside the graph", project("scripts/check-deps"), "", false},
		{"standard library is outside the graph", "fmt", "", false},
		{"another module is outside the graph", "example.com/other/internal/broker", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := nodeOf(tt.importPath)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("nodeOf(%q) = (%q, %v), want (%q, %v)",
					tt.importPath, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestCheckEdge covers the cases reviewed in G1 of the contract review.
func TestCheckEdge(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		isTest  bool
		wantErr bool
	}{
		{
			name: "mcpserver may import localpb",
			from: project("internal/mcpserver"), to: project("internal/broker/localpb"),
		},
		{
			name:    "mcpserver may not import broker itself",
			from:    project("internal/mcpserver"),
			to:      project("internal/broker"),
			wantErr: true,
		},
		{
			name:    "mcpserver may not import deliverpb",
			from:    project("internal/mcpserver"),
			to:      project("internal/broker/deliverpb"),
			wantErr: true,
		},
		{
			name: "broker may import localpb",
			from: project("internal/broker"), to: project("internal/broker/localpb"),
		},
		{
			name: "broker may import deliverpb",
			from: project("internal/broker"), to: project("internal/broker/deliverpb"),
		},
		{
			name: "deliverpb may import localpb",
			from: project("internal/broker/deliverpb"), to: project("internal/broker/localpb"),
		},
		{
			name:    "config may not import inbox",
			from:    project("internal/config"),
			to:      project("internal/inbox"),
			wantErr: true,
		},
		{
			name:    "a normal import of testkit is forbidden",
			from:    project("internal/mcpserver"),
			to:      project("internal/testkit"),
			wantErr: true,
		},
		{
			name: "a test import of testkit is allowed",
			from: project("internal/mcpserver"), to: project("internal/testkit"), isTest: true,
		},
		{
			name: "a package may import its own package",
			from: project("internal/broker"), to: project("internal/broker"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, ok := nodeOf(tt.from)
			if !ok {
				t.Fatalf("nodeOf(%q) did not recognize a project package", tt.from)
			}
			to, ok := nodeOf(tt.to)
			if !ok {
				t.Fatalf("nodeOf(%q) did not recognize a project package", tt.to)
			}
			err := checkEdge(from, to, tt.isTest)
			if tt.wantErr && err == nil {
				t.Fatalf("checkEdge(%q, %q, isTest=%v) = nil, want an error",
					from, to, tt.isTest)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("checkEdge(%q, %q, isTest=%v) = %v, want nil",
					from, to, tt.isTest, err)
			}
		})
	}
}

func TestCheckPackage(t *testing.T) {
	t.Run("test imports may use testkit and project edges", func(t *testing.T) {
		pkg := packageInfo{
			ImportPath:   project("internal/mcpserver"),
			Imports:      []string{project("internal/broker/localpb")},
			TestImports:  []string{project("internal/testkit"), project("internal/broker/localpb")},
			XTestImports: []string{project("internal/testkit")},
		}
		if violations := checkPackage(pkg); len(violations) != 0 {
			t.Fatalf("checkPackage() reported violations: %v", violations)
		}
	})

	t.Run("a normal import of testkit is reported", func(t *testing.T) {
		pkg := packageInfo{
			ImportPath: project("internal/mcpserver"),
			Imports:    []string{project("internal/testkit")},
		}
		violations := checkPackage(pkg)
		if len(violations) != 1 {
			t.Fatalf("checkPackage() = %v, want one violation", violations)
		}
		if !strings.Contains(violations[0], "testkit") {
			t.Fatalf("violation %q does not mention testkit", violations[0])
		}
	})

	t.Run("the same edge rules apply to test imports", func(t *testing.T) {
		pkg := packageInfo{
			ImportPath:  project("internal/config"),
			TestImports: []string{project("internal/inbox")},
		}
		if violations := checkPackage(pkg); len(violations) != 1 {
			t.Fatalf("checkPackage() = %v, want one violation", violations)
		}
	})
}

func TestEnvWith(t *testing.T) {
	t.Setenv("GOOS", "linux")

	env := envWith("GOOS", "darwin")

	var values []string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "GOOS="); ok {
			values = append(values, value)
		}
	}
	if len(values) != 1 || values[0] != "darwin" {
		t.Fatalf("envWith(GOOS, darwin) produced GOOS entries %v, want exactly [darwin]", values)
	}
}

func TestParsePackages(t *testing.T) {
	t.Run("an unmatched pattern yields no packages", func(t *testing.T) {
		out := []byte("./internal/...|||\n./cmd/...|||\n")
		packages, err := parsePackages(out)
		if err != nil {
			t.Fatalf("parsePackages() error = %v", err)
		}
		if len(packages) != 0 {
			t.Fatalf("parsePackages() = %v, want none", packages)
		}
	})

	t.Run("empty output yields no packages", func(t *testing.T) {
		packages, err := parsePackages(nil)
		if err != nil {
			t.Fatalf("parsePackages() error = %v", err)
		}
		if len(packages) != 0 {
			t.Fatalf("parsePackages() = %v, want none", packages)
		}
	})

	t.Run("imports are split per list", func(t *testing.T) {
		line := strings.Join([]string{
			project("internal/broker"),
			project("internal/broker/localpb") + " fmt",
			project("internal/testkit"),
			project("internal/broker/deliverpb"),
		}, "|")
		packages, err := parsePackages([]byte(line + "\n"))
		if err != nil {
			t.Fatalf("parsePackages() error = %v", err)
		}
		if len(packages) != 1 {
			t.Fatalf("parsePackages() returned %d packages, want 1", len(packages))
		}
		got := packages[0]
		if got.ImportPath != project("internal/broker") {
			t.Fatalf("ImportPath = %q", got.ImportPath)
		}
		if len(got.Imports) != 2 || got.Imports[0] != project("internal/broker/localpb") || got.Imports[1] != "fmt" {
			t.Fatalf("Imports = %v", got.Imports)
		}
		if len(got.TestImports) != 1 || got.TestImports[0] != project("internal/testkit") {
			t.Fatalf("TestImports = %v", got.TestImports)
		}
		if len(got.XTestImports) != 1 || got.XTestImports[0] != project("internal/broker/deliverpb") {
			t.Fatalf("XTestImports = %v", got.XTestImports)
		}
	})
}
