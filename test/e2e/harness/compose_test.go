package harness

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestProjectName fixes the compose project name format, which carries the
// owner marker, and requires two consecutive names to differ.
func TestProjectName(t *testing.T) {
	pattern := regexp.MustCompile(`^ape2e-[0-9]+-[0-9]+-pidns[0-9]+-[a-z0-9-]{1,32}-[0-9a-f]{6}$`)

	first := projectName("TestJourneyPairBrokers/alias conflict")
	if !pattern.MatchString(first) {
		t.Fatalf("projectName = %q, want a match for %s", first, pattern)
	}
	second := projectName("TestJourneyPairBrokers/alias conflict")
	if first == second {
		t.Fatalf("projectName returned %q twice; the names must differ", first)
	}
}

// TestComposeServices checks that every service of flat.yaml is reported,
// including the profiled hosts a TopoFlat with fewer hosts does not start.
// Compose substitutes the variables of every declared service, so Up must fill
// the control directory of each one.
func TestComposeServices(t *testing.T) {
	path, err := composeSourcePath("flat.yaml")
	if err != nil {
		t.Fatalf("locating flat.yaml: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading flat.yaml: %v", err)
	}
	got, err := composeServices(data)
	if err != nil {
		t.Fatalf("composeServices: %v", err)
	}
	want := []string{"hostA", "hostB", "hostC", "hostD"}
	if !slices.Equal(got, want) {
		t.Fatalf("composeServices = %v, want %v", got, want)
	}
}

// TestProjectNameTruncatesLongNames checks that a long or punctuation-heavy
// test name still yields a valid project name.
func TestProjectNameTruncatesLongNames(t *testing.T) {
	pattern := regexp.MustCompile(`^ape2e-[0-9]+-[0-9]+-pidns[0-9]+-[a-z0-9-]{1,32}-[0-9a-f]{6}$`)
	name := projectName(strings.Repeat("TestVeryLongName/", 20))
	if !pattern.MatchString(name) {
		t.Fatalf("projectName = %q, want a match for %s", name, pattern)
	}
}
