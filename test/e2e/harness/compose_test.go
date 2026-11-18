package harness

import (
	"regexp"
	"strings"
	"testing"
)

// TestProjectName fixes the compose project name format and requires two
// consecutive names to differ.
func TestProjectName(t *testing.T) {
	pattern := regexp.MustCompile(`^ape2e-[a-z0-9-]{1,32}-[0-9a-f]{6}$`)

	first := projectName("TestJourneyPairBrokers/alias conflict")
	if !pattern.MatchString(first) {
		t.Fatalf("projectName = %q, want a match for %s", first, pattern)
	}
	second := projectName("TestJourneyPairBrokers/alias conflict")
	if first == second {
		t.Fatalf("projectName returned %q twice; the names must differ", first)
	}
}

// TestProjectNameTruncatesLongNames checks that a long or punctuation-heavy
// test name still yields a valid project name.
func TestProjectNameTruncatesLongNames(t *testing.T) {
	pattern := regexp.MustCompile(`^ape2e-[a-z0-9-]{1,32}-[0-9a-f]{6}$`)
	name := projectName(strings.Repeat("TestVeryLongName/", 20))
	if !pattern.MatchString(name) {
		t.Fatalf("projectName = %q, want a match for %s", name, pattern)
	}
}
