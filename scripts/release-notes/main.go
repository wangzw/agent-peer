// Command release-notes extracts one version's section from the changelog and
// writes it to standard output, for use as the body of a GitHub release.
//
// The changelog follows Keep a Changelog: each version starts with a level-two
// heading "## [<version>] - <date>" and its section runs until the next
// level-two heading or the end of the file. The command refuses a section whose
// date is missing or still says "Unreleased", and a section without content, so
// a release cannot be published from an unfinished changelog.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// The sentinel errors separate the three ways extraction can fail. They are
// unexported because this is a command, not a library; tests match them with
// errors.Is.
var (
	errNotFound   = errors.New("version section not found")
	errUnreleased = errors.New("version section is not released")
	errEmpty      = errors.New("version section is empty")
)

// headingRE matches a level-two version heading. The version is the text in
// brackets and the date is everything after " - ". A heading without a date
// matches with an empty date.
var headingRE = regexp.MustCompile(`^## \[([^\]]+)\](?: - (.+))?$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it parses args, extracts the requested
// section and writes it to stdout. It returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release-notes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "version whose section to extract (0.1.0 or v0.1.0)")
	changelogPath := fs.String("changelog", "CHANGELOG.md", "path to the changelog file")
	if err := fs.Parse(args); err != nil {
		// flag has already written the error and usage to stderr.
		return 2
	}
	if fs.NArg() > 0 {
		writef(stderr, "release-notes: unexpected positional arguments: %s\n",
			strings.Join(fs.Args(), " "))
		return 2
	}
	if *version == "" {
		writef(stderr, "release-notes: -version is required\n")
		return 2
	}

	changelog, err := os.ReadFile(*changelogPath)
	if err != nil {
		writef(stderr, "release-notes: %v\n", err)
		return 1
	}
	notes, err := extract(changelog, strings.TrimPrefix(*version, "v"))
	if err != nil {
		writef(stderr, "release-notes: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, notes); err != nil {
		writef(stderr, "release-notes: %v\n", err)
		return 1
	}
	return 0
}

// writef writes a diagnostic. A failed write leaves nothing better to report
// it to, so the error is deliberately dropped; the exit code still reports the
// failure that produced the diagnostic.
func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// extract returns the body of the section for version: the lines after the
// matching "## [version] - <date>" heading, up to the next level-two heading or
// the end of the file. Leading and trailing blank lines are dropped, interior
// lines are kept verbatim, and the result ends with exactly one newline.
//
// The first matching heading wins, because the changelog lists the newest
// version first. A missing date and an "Unreleased" date are both refusals: the
// section is not finished and must not become a release body.
func extract(changelog []byte, version string) (string, error) {
	// Normalize Windows line endings so the output is always LF.
	text := strings.ReplaceAll(string(changelog), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	start := -1
	date := ""
	for i, line := range lines {
		match := headingRE.FindStringSubmatch(line)
		if match == nil || match[1] != version {
			continue
		}
		start = i
		date = match[2]
		break
	}
	if start < 0 {
		return "", fmt.Errorf("%w: %s", errNotFound, version)
	}
	if strings.TrimSpace(date) == "" || strings.EqualFold(date, "unreleased") {
		return "", fmt.Errorf("%w: %s", errUnreleased, version)
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}

	body := lines[start+1 : end]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return "", fmt.Errorf("%w: %s", errEmpty, version)
	}
	return strings.Join(body, "\n") + "\n", nil
}
