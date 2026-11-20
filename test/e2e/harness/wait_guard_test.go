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

// eventuallyCallExempt exempts one direct or indirect tool call inside a wait
// callback. The comment must stand on the same line as the call or on the line
// directly above it, and the marker must be followed by a reason. A line
// comment and a block comment are both accepted. The reason is not checked
// beyond being present: it is there for the next reader, who needs to know why
// this wait cannot retry with PollCall.
const eventuallyCallExempt = "eventually-call-allow:"

// TestEventuallyCallbacksUsePollBoundedCalls rejects a tool call made inside an
// Eventually, EventuallyPoll or WaitFor callback when the call bypasses the
// wait's bounds. Session.Call bounds one tool call at sessionCallBound, sixty
// seconds, and fails the test on a transport error, so a callback that uses it
// turns one stalled MCP call into a failed wait instead of retrying it. A
// callback makes poll-bounded calls with Session.PollCall or
// Session.PollCallWithin, which the deadline EventuallyPoll passes in bounds
// and clamps to the time the wait has left.
//
// The callback's direct calls are checked, and so is one level of indirection:
// a call to a function in the same directory whose own body makes a direct
// .Call is rejected too, because the helper hides the call from a guard that
// only reads the callback.
//
// A wait that genuinely needs the sixty-second bound exempts the call with
// // eventually-call-allow: <reason> on the call's line or the line above it.
// A wait whose callback is a named function also needs an exemption, because
// the guard cannot see that function's body from the wait call.
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

	type sourceFile struct {
		path string
		file *ast.File
	}
	var sources []sourceFile
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
		sources = append(sources, sourceFile{path: path, file: file})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", e2eDir, err)
	}

	// callFuncs maps a directory to the names of its functions whose body makes
	// a direct .Call. A wait callback that calls such a function is reported,
	// one level deep; the function's own callees are not followed.
	callFuncs := make(map[string]map[string]bool)
	for _, src := range sources {
		dir := filepath.Dir(src.path)
		if callFuncs[dir] == nil {
			callFuncs[dir] = make(map[string]bool)
		}
		for _, name := range directCallFuncs(src.file) {
			callFuncs[dir][name] = true
		}
	}

	var violations []string
	for _, src := range sources {
		dir := filepath.Dir(src.path)
		violations = append(violations, waitCallbackViolations(fset, src.file, callFuncs[dir])...)
	}

	sort.Strings(violations)
	for _, violation := range violations {
		t.Error(violation)
	}
	if len(violations) > 0 {
		t.Errorf("%d tool calls inside wait callbacks bypass the wait's bounds; call Session.PollCall or PollCallWithin with the callback's deadline, or exempt the call with // %s <reason>", len(violations), eventuallyCallExempt)
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

// directCallFuncs returns the names of the functions declared in file whose
// body makes a call of a method named Call.
func directCallFuncs(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if bodyCallsCall(fn.Body) {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

// bodyCallsCall reports whether body makes a direct call of a method named
// Call, anywhere in the body including a nested function literal. Session.Call
// is the method the guard is concerned with; a method called Call on another
// type is rejected too, which the exemption covers.
func bodyCallsCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Call" {
			found = true
			return false
		}
		return true
	})
	return found
}

// waitCallbackViolations returns one message for every tool call in the
// callback passed to an Eventually, EventuallyPoll or WaitFor call in file that
// bypasses the wait's bounds. callFuncs names the functions in the file's
// directory whose body makes a direct .Call.
func waitCallbackViolations(fset *token.FileSet, file *ast.File, callFuncs map[string]bool) []string {
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
		last := lastArg(call)
		if last == nil {
			return true
		}
		cond, isLiteral := last.(*ast.FuncLit)
		if !isLiteral {
			line := fset.Position(call.Lparen).Line
			if exempt[line] || exempt[line-1] {
				return true
			}
			violations = append(violations, fmt.Sprintf(
				"%s: %s takes a named callback, so this guard cannot see whether it makes a tool call; pass a function literal or exempt the wait with // %s <reason>",
				fset.Position(last.Pos()), name, eventuallyCallExempt))
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
			line := fset.Position(inner.Lparen).Line
			if exempt[line] || exempt[line-1] {
				return true
			}
			switch fun := inner.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel.Name != "Call" {
					return true
				}
				violations = append(violations, fmt.Sprintf(
					"%s: .Call inside the callback of %s; a wait must use Session.PollCall or PollCallWithin and its deadline, or exempt the call with // %s <reason>",
					fset.Position(fun.Sel.Pos()), name, eventuallyCallExempt))
			case *ast.Ident:
				if !callFuncs[fun.Name] {
					return true
				}
				violations = append(violations, fmt.Sprintf(
					"%s: %s is called inside the callback of %s and makes a direct .Call in its body; call a deadline-taking variant and Session.PollCall, or exempt the call with // %s <reason>",
					fset.Position(fun.Pos()), fun.Name, name, eventuallyCallExempt))
			}
			return true
		})
		return true
	})
	return violations
}

// lastArg returns the last argument of call, or nil when it takes none.
func lastArg(call *ast.CallExpr) ast.Expr {
	if len(call.Args) == 0 {
		return nil
	}
	return call.Args[len(call.Args)-1]
}

// waitCallName reports whether call invokes Eventually, EventuallyPoll or
// WaitFor, and returns the name for the message.
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
	case "Eventually", "EventuallyPoll", "WaitFor":
		return name, true
	}
	return "", false
}

// exemptLines returns the lines carrying an exemption marker with a reason. A
// line comment and a block comment are both accepted; a block comment marks
// every line it covers.
func exemptLines(fset *token.FileSet, file *ast.File) map[int]bool {
	lines := make(map[int]bool)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !hasExemptionReason(comment.Text) {
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

// hasExemptionReason reports whether text is an exemption marker followed by a
// reason, in a line comment or a block comment.
func hasExemptionReason(text string) bool {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "//")
	if strings.HasPrefix(text, "/*") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, eventuallyCallExempt) {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, eventuallyCallExempt)) != ""
}

// TestWaitGuardSeesNamedAndIndirectCallbacks covers the two cases the walk must
// not pass silently: a named callback, whose body the guard cannot see from the
// wait call, and a call to a same-package function whose body makes a direct
// .Call. The last case checks that a block comment exempts the call, as the
// marker's documentation says.
func TestWaitGuardSeesNamedAndIndirectCallbacks(t *testing.T) {
	const src = `package p

type S struct{}

func (s S) Call(t any, tool string, args any) {}

func helper(s S) { s.Call(nil, "list_agents", nil) }

func f(cond func() (bool, string)) {
	Eventually(nil, 0, "named", cond)
	Eventually(nil, 0, "indirect", func() (bool, string) { helper(S{}); return true, "" })
	Eventually(nil, 0, "exempt", func() (bool, string) {
		helper(S{}) /* eventually-call-allow: helper is a read-only poll */
		return true, ""
	})
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}
	violations := waitCallbackViolations(fset, file, map[string]bool{"helper": true})
	joined := strings.Join(violations, "\n")
	if len(violations) != 2 {
		t.Fatalf("found %d violations, want 2 (named callback and indirect call):\n%s", len(violations), joined)
	}
	if !strings.Contains(joined, "takes a named callback") {
		t.Errorf("the named callback was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "helper is called inside") {
		t.Errorf("the indirect call was not reported:\n%s", joined)
	}
}

// TestExemptionCommentForms checks that the marker accepts the line and block
// comment forms its documentation names, and requires a reason.
func TestExemptionCommentForms(t *testing.T) {
	for _, tc := range []struct {
		comment string
		want    bool
	}{
		{"// eventually-call-allow: read-only poll", true},
		{"/* eventually-call-allow: read-only poll */", true},
		{"// eventually-call-allow:", false},
		{"/* eventually-call-allow: */", false},
		{"// unrelated comment", false},
	} {
		if got := hasExemptionReason(tc.comment); got != tc.want {
			t.Errorf("hasExemptionReason(%q) = %v, want %v", tc.comment, got, tc.want)
		}
	}
}
