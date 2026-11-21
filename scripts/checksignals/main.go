// Command checksignals rejects a direct syscall.Kill or x/sys/unix.Kill call
// outside the one helper that guards the pid: procsig.SendSignal.
//
// The guard exists because a pid can stop naming a process while code still
// holds it. Process.Release sets Pid to -1, a service manager may report
// MainPID 0 before the process starts, and os.Getppid returns 1 once the caller
// is adopted by init. Passing any of those to kill reaches init, the caller's
// process group or every process the account may signal.
//
// Every Go file is checked, test files included: a test that parses a pid from
// ps or /proc can hold the same stale value. third_party/ is vendored source
// and is left alone, as the other lint gates do.
//
// A direct call that is genuinely intended is exempt with a comment on the
// same line or the line above:
//
//	//procsig:allow sends SIGTERM to this test's own process
//	syscall.Kill(os.Getpid(), syscall.SIGTERM)
//
// The reason after the marker is required; a bare //procsig:allow is not an
// exemption. The cases that need one are a signal to the caller's own
// process, a deliberate signal to a process group with a negative pid, and a
// signal-0 probe that must tell EPERM from success instead of only asking
// whether the pid is alive (procsig.Alive answers that question).
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// helperFile is the one file allowed to call a signal syscall directly.
	helperFile = "internal/procsig/signal.go"
	// allowPrefix marks a comment that exempts one direct call.
	allowPrefix = "//procsig:allow"
)

// skipDirs holds directory names that are not project source.
var skipDirs = map[string]bool{
	".git":         true,
	".claude":      true,
	"third_party":  true,
	"vendor":       true,
	"node_modules": true,
}

// forbidden maps an import path to the directive a violation should name.
var forbidden = map[string]string{
	"syscall":               "syscall.Kill",
	"golang.org/x/sys/unix": "unix.Kill",
}

func main() {
	root := flag.String("root", ".", "directory to scan from")
	flag.Parse()
	if err := run(*root); err != nil {
		fmt.Fprintf(os.Stderr, "checksignals: %v\n", err)
		os.Exit(1)
	}
}

// run scans root and returns an error naming every forbidden call it found.
func run(root string) error {
	var violations []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == helperFile {
			return nil
		}
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		violations = append(violations, checkFile(fset, rel, file)...)
		return nil
	})
	if err != nil {
		return err
	}
	if len(violations) == 0 {
		return nil
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v)
	}
	return fmt.Errorf("%d direct signal call(s); use procsig.SendSignal, or exempt an intended call with //procsig:allow <reason> (%s)", len(violations), helperFile)
}

// checkFile returns one message for every direct signal call in file.
func checkFile(fset *token.FileSet, name string, file *ast.File) []string {
	// aliases maps the name a file uses for an import to the import path, and
	// dotPkg records a dot import of one of the forbidden packages.
	aliases := make(map[string]string)
	dotPkg := ""
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if _, ok := forbidden[importPath]; !ok {
			continue
		}
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		switch local {
		case "_":
			continue
		case ".":
			dotPkg = importPath
		default:
			aliases[local] = importPath
		}
	}
	if len(aliases) == 0 && dotPkg == "" {
		return nil
	}

	allowed := allowLines(fset, file)
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pkg, direct := calledKill(call, aliases, dotPkg)
		if !direct {
			return true
		}
		pos := fset.Position(call.Pos())
		if allowed[pos.Line] {
			return true
		}
		out = append(out, fmt.Sprintf("%s:%d: direct %s call; use procsig.SendSignal or exempt it with //procsig:allow <reason>",
			name, pos.Line, forbidden[pkg]))
		return true
	})
	return out
}

// allowLines returns the call lines a //procsig:allow comment covers. A
// comment covers the line it ends on and the line after it, so it may sit
// beside the call or directly above it. A marker without a reason covers
// nothing.
func allowLines(fset *token.FileSet, file *ast.File) map[int]bool {
	lines := make(map[int]bool)
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !hasAllowReason(c.Text) {
				continue
			}
			end := fset.Position(c.End()).Line
			lines[end] = true
			lines[end+1] = true
		}
	}
	return lines
}

// hasAllowReason reports whether text is a //procsig:allow comment with a
// non-empty reason. The reason is required, so the marker alone is not an
// exemption.
func hasAllowReason(text string) bool {
	rest, ok := strings.CutPrefix(text, allowPrefix)
	if !ok {
		return false
	}
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return false
	}
	return strings.TrimSpace(rest) != ""
}

// calledKill reports whether call invokes Kill from one of the forbidden
// packages, and which package it is.
func calledKill(call *ast.CallExpr, aliases map[string]string, dotPkg string) (string, bool) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if fun.Sel.Name != "Kill" {
			return "", false
		}
		ident, ok := fun.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		pkg, ok := aliases[ident.Name]
		return pkg, ok
	case *ast.Ident:
		if dotPkg != "" && fun.Name == "Kill" {
			return dotPkg, true
		}
	}
	return "", false
}
