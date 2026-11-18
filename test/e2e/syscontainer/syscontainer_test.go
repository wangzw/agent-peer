package syscontainer

import (
	"regexp"
	"strings"
	"testing"
)

func TestRunCommandLine(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"TestX/sub", `^TestX$/^sub$`},
		{"TestX", `^TestX$`},
		{"TestX/sub.plus+", `^TestX$/^sub\.plus\+$`},
	}
	for _, tc := range cases {
		if got := runTestFlag(tc.name); got != tc.want {
			t.Errorf("runTestFlag(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}

	// -test.run matches each slash-separated segment as its own expression, so
	// the pattern must match the named test and nothing that merely contains
	// it, while regular-expression characters in the name stay literal.
	pattern := runTestFlag("TestX/sub.plus+")
	if !matchesTestRun(pattern, "TestX/sub.plus+") {
		t.Errorf("pattern %q does not match its own test name", pattern)
	}
	for _, other := range []string{"TestX/subXplus+", "TestXOther/sub.plus+", "TestX/sub.plus"} {
		if matchesTestRun(pattern, other) {
			t.Errorf("pattern %q matches %q", pattern, other)
		}
	}
}

// matchesTestRun applies a -test.run pattern the way the testing package does:
// one regular expression per slash-separated segment.
func matchesTestRun(pattern, name string) bool {
	patterns := strings.Split(pattern, "/")
	names := strings.Split(name, "/")
	if len(patterns) != len(names) {
		return false
	}
	for i := range patterns {
		if !regexp.MustCompile(patterns[i]).MatchString(names[i]) {
			return false
		}
	}
	return true
}
