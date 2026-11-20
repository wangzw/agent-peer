package harness

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// eventuallyCallExempt exempts one direct .Call inside an Eventually or
// EventuallyPoll callback. The comment must stand on the same line as the call
// or on the line directly above it, and the marker must be followed by a
// reason. The reason is not checked beyond being present: it is there for the
// next reader, who needs to know why this wait cannot retry with PollCall.
const eventuallyCallExempt = "// eventually-call-allow:"

// TestEventuallyCallbacksUsePollBoundedCalls rejects a direct Session.Call
// inside an Eventually or EventuallyPoll callback. Call bounds one tool call at
// sessionCallBound, sixty seconds, and fails the test on a transport error, so
// a callback that uses it turns one stalled MCP call into a failed wait instead
// of retrying it. A callback makes poll-bounded calls with Session.PollCall or
// Session.PollCallWithin, which the deadline EventuallyPoll passes in bounds
// and clamps to the time the wait has left.
//
// A wait that genuinely needs the sixty-second bound exempts the call with
// // eventually-call-allow: <reason> on the call's line or the line above it.
//
// The test walks the Go sources under test/e2e rather than the compiled
// packages because most of them carry the e2e build tag, and it lives in the
// harness package so make test runs it.
func TestEventuallyCallbacksUsePollBoundedCalls(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	e2eDir := filepath.Join(root, "test", "e2e")

	var violations []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(e2eDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		violations = append(violations, eventuallyCallViolations(fset, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", e2eDir, err)
	}

	sort.Strings(violations)
	for _, violation := range violations {
		t.Error(violation)
	}
	if len(violations) > 0 {
		t.Errorf("%d direct .Call calls inside Eventually callbacks; call Session.PollCall or PollCallWithin with the callback's deadline, or exempt the call with %s <reason>", len(violations), eventuallyCallExempt)
	}
}

// repositoryRoot returns the module root, found by walking up from this source
// file to the directory holding go.mod.
func repositoryRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("harness: cannot locate the guard test's source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("harness: no go.mod above %s", file)
		}
		dir = parent
	}
}

// eventuallyCallViolations returns one message for every direct .Call call in
// the callback passed to an Eventually or EventuallyPoll call in file.
func eventuallyCallViolations(fset *token.FileSet, file *ast.File) []string {
	exempt := exemptLines(fset, file)
	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := waitCallName(call)
		if !ok {
			return true
		}
		cond := condFuncLit(call)
		if cond == nil {
			return true
		}
		ast.Inspect(cond.Body, func(n ast.Node) bool {
			inner, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if _, nested := waitCallName(inner); nested {
				// The walk over the file reports a nested wait's callback
				// on its own; do not count its calls twice.
				return false
			}
			sel, ok := inner.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Call" {
				return true
			}
			line := fset.Position(inner.Lparen).Line
			if exempt[line] || exempt[line-1] {
				return true
			}
			pos := fset.Position(sel.Sel.Pos())
			violations = append(violations, fmt.Sprintf(
				"%s: .Call inside the callback of %s; a wait must use Session.PollCall or PollCallWithin and its deadline, or exempt the call with %s <reason>",
				pos, name, eventuallyCallExempt))
			return true
		})
		return true
	})
	return violations
}

// waitCallName reports whether call invokes Eventually or EventuallyPoll, and
// returns the name for the message.
func waitCallName(call *ast.CallExpr) (string, bool) {
	var name string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	default:
		return "", false
	}
	switch name {
	case "Eventually", "EventuallyPoll":
		return name, true
	}
	return "", false
}

// condFuncLit returns the callback passed to a wait call, or nil when the
// callback is not a function literal.
func condFuncLit(call *ast.CallExpr) *ast.FuncLit {
	if len(call.Args) == 0 {
		return nil
	}
	lit, _ := call.Args[len(call.Args)-1].(*ast.FuncLit)
	return lit
}

// exemptLines returns the lines carrying an exemption marker with a reason.
// A block comment marks every line it covers.
func exemptLines(fset *token.FileSet, file *ast.File) map[int]bool {
	lines := make(map[int]bool)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, eventuallyCallExempt) {
				continue
			}
			if strings.TrimSpace(strings.TrimPrefix(comment.Text, eventuallyCallExempt)) == "" {
				continue
			}
			start := fset.Position(comment.Pos()).Line
			end := fset.Position(comment.End()).Line
			for line := start; line <= end; line++ {
				lines[line] = true
			}
		}
	}
	return lines
}
