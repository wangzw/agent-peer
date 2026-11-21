// Command check-deps enforces the module dependency direction recorded in the
// module dependency graph of docs/design/architecture.md.
//
// It asks the Go toolchain for the imports of every package under ./internal
// and ./cmd and fails when a package imports another project package that the
// graph does not allow. Standard-library, third-party and third_party/ imports
// are ignored, and so are imports of tooling outside the module graph (scripts,
// test).
//
// A package under internal/<module>/<pb> counts as part of <module>, because
// the generated protobuf packages live in such subdirectories. The two
// protobuf packages under internal/broker are the exception: localpb and
// deliverpb are nodes of their own, because mcpserver may only reach the local
// interface and deliverpb itself depends on localpb.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const modulePath = "github.com/wangzw/peers"

// testkitNode is the graph node of internal/testkit, the shared test support
// package. It is not part of the dependency graph and may only be imported
// from test files.
const testkitNode = "testkit"

// allowed maps each package in the module graph to the project packages it may
// import. A package absent from this map may import no project package at all.
var allowed = map[string][]string{
	"cmd":       {"broker", "mcpserver"},
	"mcpserver": {"localpb"},
	"broker":    {"topic", "directory", "link", "inbox", "pairing", "config", "accountdb", "localpb", "deliverpb"},
	"deliverpb": {"localpb"},
	"topic":     {"link"},
	"directory": {"link", "pairing", "config"},
	"link":      {"pairing", "config"},
	"pairing":   {"config"},
	"config":    {"accountdb"},
	"inbox":     nil,
	"accountdb": nil,
	"localpb":   nil,
}

// packageInfo holds the imports of one package as reported by go list.
type packageInfo struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "check-deps: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// The flags configure the go list subprocess, not this binary: the checker
	// itself always runs on the host, and cross-configuration is applied to the
	// package loading it asks the Go toolchain for.
	goos := flag.String("goos", "", "GOOS for the go list subprocess; empty keeps the host GOOS")
	tags := flag.String("tags", "", "build tags for the go list subprocess; empty means none")
	flag.Parse()

	args := []string{"list", "-e"}
	if *tags != "" {
		args = append(args, "-tags", *tags)
	}
	// -e keeps go list quiet when ./internal/... or ./cmd/... matches no
	// packages yet; an empty result is a pass, not a failure.
	args = append(args,
		"-f", `{{.ImportPath}}|{{join .Imports " "}}|{{join .TestImports " "}}|{{join .XTestImports " "}}`,
		"./internal/...", "./cmd/...")

	cmd := exec.Command("go", args...)
	cmd.Stderr = os.Stderr
	if *goos != "" {
		cmd.Env = envWith("GOOS", *goos)
	}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("go list failed: %w", err)
	}

	packages, err := parsePackages(out)
	if err != nil {
		return fmt.Errorf("reading go list output: %w", err)
	}

	checked := 0
	var violations []string
	for _, pkg := range packages {
		if _, ok := nodeOf(pkg.ImportPath); !ok {
			// A package outside the module graph (tooling or tests).
			continue
		}
		checked++
		violations = append(violations, checkPackage(pkg)...)
	}

	if checked == 0 {
		fmt.Println("check-deps: no packages under internal/ or cmd/ yet; nothing to check")
		return nil
	}
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintf(os.Stderr, "check-deps: %s\n", v)
		}
		return fmt.Errorf("%d dependency-direction violation(s)", len(violations))
	}
	fmt.Printf("check-deps: dependency direction is clean across %d packages\n", checked)
	return nil
}

// envWith returns the current environment with key set to value. An existing
// entry for key is replaced rather than duplicated, so the requested value also
// takes effect when the host environment already sets the variable.
func envWith(key, value string) []string {
	env := os.Environ()
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

// parsePackages parses the pipe-separated lines printed by go list. Lines that
// do not name a package inside the module (the placeholder such as
// "./internal/..." printed for an unmatched pattern) are skipped.
func parsePackages(out []byte) ([]packageInfo, error) {
	var packages []packageInfo
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	// Import lists can grow past bufio's default token size.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		importPath := strings.TrimSpace(parts[0])
		if !strings.HasPrefix(importPath, modulePath+"/") {
			continue
		}
		packages = append(packages, packageInfo{
			ImportPath:   importPath,
			Imports:      strings.Fields(parts[1]),
			TestImports:  strings.Fields(parts[2]),
			XTestImports: strings.Fields(parts[3]),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return packages, nil
}

// checkPackage returns the violations among one package's normal and test
// imports. The same edge rules apply to both; test imports only relax the ban
// on importing testkit.
func checkPackage(pkg packageInfo) []string {
	from, ok := nodeOf(pkg.ImportPath)
	if !ok {
		return nil
	}
	var violations []string
	for _, imported := range pkg.Imports {
		violations = append(violations, checkImport(pkg.ImportPath, from, imported, false)...)
	}
	for _, imported := range pkg.TestImports {
		violations = append(violations, checkImport(pkg.ImportPath, from, imported, true)...)
	}
	for _, imported := range pkg.XTestImports {
		violations = append(violations, checkImport(pkg.ImportPath, from, imported, true)...)
	}
	return violations
}

// checkImport applies checkEdge to one import and renders a violation message.
func checkImport(importer, from, imported string, isTest bool) []string {
	to, ok := nodeOf(imported)
	if !ok {
		return nil
	}
	if err := checkEdge(from, to, isTest); err != nil {
		return []string{fmt.Sprintf(
			"%s imports %s (%s -> %s): %v", importer, imported, from, to, err)}
	}
	return nil
}

// checkEdge reports whether from may import to. A package may always import
// its own package. testkit may only be imported by test code, and test support
// code itself is outside the graph.
func checkEdge(from, to string, isTest bool) error {
	if from == to {
		return nil
	}
	if to == testkitNode {
		if isTest {
			return nil
		}
		return fmt.Errorf("internal/testkit may only be imported from test files")
	}
	if from == testkitNode {
		// Test support code is not part of the module dependency graph.
		return nil
	}
	if isAllowed(from, to) {
		return nil
	}
	return fmt.Errorf("not an edge in the module dependency graph of docs/design/architecture.md")
}

// nodeOf returns the module-graph node that owns a project package. Packages
// outside the graph (tooling, docs, third_party) report ok == false.
func nodeOf(importPath string) (string, bool) {
	rel, ok := strings.CutPrefix(importPath, modulePath+"/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rel, "/")
	switch parts[0] {
	case "internal":
		if len(parts) < 2 {
			return "", false
		}
		if parts[1] == "broker" && len(parts) >= 3 {
			switch parts[2] {
			case "localpb":
				return "localpb", true
			case "deliverpb":
				return "deliverpb", true
			}
		}
		return parts[1], true
	case "cmd":
		return "cmd", true
	default:
		return "", false
	}
}

func isAllowed(from, to string) bool {
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
