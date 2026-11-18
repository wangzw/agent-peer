package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		changelog      string
		version        string
		want           string
		wantErr        error
		wantNotContain []string
	}{
		{
			name: "only the requested section is returned",
			changelog: `## [0.2.0] - 2026-12-01

Second release.

## [0.1.0] - 2026-11-18

First release.

### Added

- Initial release.
`,
			version:        "0.1.0",
			want:           "First release.\n\n### Added\n\n- Initial release.\n",
			wantNotContain: []string{"## [0.1.0]", "Second release."},
		},
		{
			name: "the last section runs to the end of the file",
			changelog: `## [0.2.0] - 2026-12-01

Second release.

## [0.1.0] - 2026-11-18

First release.
`,
			version: "0.1.0",
			want:    "First release.\n",
		},
		{
			name: "a longer version is not a prefix match",
			changelog: `## [0.1.0-rc.1] - 2026-11-01

Release candidate.

## [0.1.0] - 2026-11-18

Final release.
`,
			version:        "0.1.0",
			want:           "Final release.\n",
			wantNotContain: []string{"Release candidate."},
		},
		{
			name: "interior blank lines and level-three headings are kept",
			changelog: `## [0.1.0] - 2026-11-18


### Added

- One.
- Two.

### Fixed

- Three.


`,
			version: "0.1.0",
			want:    "### Added\n\n- One.\n- Two.\n\n### Fixed\n\n- Three.\n",
		},
		{
			name: "a missing version is reported",
			changelog: `## [0.2.0] - 2026-12-01

Second release.
`,
			version: "0.1.0",
			wantErr: errNotFound,
		},
		{
			name:      "an Unreleased date is refused",
			changelog: "## [0.1.0] - Unreleased\n\nWork in progress.\n",
			version:   "0.1.0",
			wantErr:   errUnreleased,
		},
		{
			name:      "an Unreleased date is refused case-insensitively",
			changelog: "## [0.1.0] - unreleased\n\nWork in progress.\n",
			version:   "0.1.0",
			wantErr:   errUnreleased,
		},
		{
			name:      "a missing date is refused",
			changelog: "## [0.1.0]\n\nWork in progress.\n",
			version:   "0.1.0",
			wantErr:   errUnreleased,
		},
		{
			name: "an empty section is refused",
			changelog: `## [0.1.0] - 2026-11-18


## [0.2.0] - 2026-12-01

Second release.
`,
			version: "0.1.0",
			wantErr: errEmpty,
		},
		{
			name:      "CRLF input yields the same output",
			changelog: "## [0.1.0] - 2026-11-18\r\n\r\n### Added\r\n\r\n- One.\r\n- Two.\r\n",
			version:   "0.1.0",
			want:      "### Added\n\n- One.\n- Two.\n",
		},
		{
			name:      "a level-three heading does not start a version",
			changelog: "### [0.1.0] - 2026-11-18\n\nNot a version section.\n",
			version:   "0.1.0",
			wantErr:   errNotFound,
		},
		{
			name: "the first of two identical versions wins",
			changelog: `## [0.1.0] - 2026-11-18

First copy.

## [0.1.0] - 2026-11-19

Second copy.
`,
			version:        "0.1.0",
			want:           "First copy.\n",
			wantNotContain: []string{"Second copy."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := extract([]byte(tt.changelog), tt.version)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("extract() error = %v, want an error matching %v", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.version) {
					t.Errorf("extract() error = %q, does not mention version %q", err, tt.version)
				}
				if got != "" {
					t.Errorf("extract() = %q alongside an error, want an empty string", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extract() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("extract() = %q, want %q", got, tt.want)
			}
			for _, fragment := range tt.wantNotContain {
				if strings.Contains(got, fragment) {
					t.Errorf("extract() = %q, must not contain %q", got, fragment)
				}
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	const notes = "### Added\n\n- First release.\n\n### Fixed\n\n- Nothing yet.\n"

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr bool
	}{
		{
			name:       "writes the requested section",
			args:       []string{"-version", "0.1.0", "-changelog", "testdata/changelog.md"},
			wantExit:   0,
			wantStdout: notes,
		},
		{
			name:       "accepts a leading v and returns the same output",
			args:       []string{"-version", "v0.1.0", "-changelog", "testdata/changelog.md"},
			wantExit:   0,
			wantStdout: notes,
		},
		{
			name:       "reports a missing version section",
			args:       []string{"-version", "9.9.9", "-changelog", "testdata/changelog.md"},
			wantExit:   1,
			wantStderr: true,
		},
		{
			name:       "requires the version flag",
			args:       []string{"-changelog", "testdata/changelog.md"},
			wantExit:   2,
			wantStderr: true,
		},
		{
			name:       "rejects positional arguments",
			args:       []string{"-changelog", "testdata/changelog.md", "0.1.0"},
			wantExit:   2,
			wantStderr: true,
		},
		{
			name:       "reports a missing changelog file",
			args:       []string{"-version", "0.1.0", "-changelog", "testdata/missing.md"},
			wantExit:   1,
			wantStderr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			exit := run(tt.args, &stdout, &stderr)
			if exit != tt.wantExit {
				t.Fatalf("run(%q) = %d, want %d (stderr: %s)",
					tt.args, exit, tt.wantExit, stderr.String())
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("run(%q) stdout = %q, want %q", tt.args, stdout.String(), tt.wantStdout)
			}
			if tt.wantStderr && stderr.Len() == 0 {
				t.Errorf("run(%q) wrote no diagnostic to stderr, want one", tt.args)
			}
		})
	}
}
