// Command checksignals rejects a direct syscall.Kill or x/sys/unix.Kill call
// outside the one helper that guards the pid: procsig.SendSignal.
//
// The guard exists because a pid can stop naming a process while code still
// holds it. Process.Release sets Pid to -1, a service manager may report
// MainPID 0 before the process starts, and os.Getppid returns 1 once the caller
// is adopted by init. Passing any of those to kill reaches init, the caller's
// process group or every process the account may signal.
//
// Only non-test Go files are checked. Tests send signals to children they
// started themselves, through an *os.Process handle or a pid read from /proc
// or ps, and one test deliberately signals a process group with a negative
// pid, which SendSignal must refuse. third_party/ is vendored source and is
// left alone, as the other lint gates do.
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

// helperFile is the one file allowed to call a signal syscall directly.
const helperFile = "internal/procsig/signal.go"

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
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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
		file, err := parser.ParseFile(fset, p, nil, 0)
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
	return fmt.Errorf("%d direct signal call(s); send a signal through procsig.SendSignal (%s)", len(violations), helperFile)
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
		out = append(out, fmt.Sprintf("%s:%d: direct %s call; send a signal through procsig.SendSignal",
			name, pos.Line, forbidden[pkg]))
		return true
	})
	return out
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
