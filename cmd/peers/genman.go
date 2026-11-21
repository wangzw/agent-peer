//go:build peers_genman

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func init() {
	addGenManCommand = addGenManCommandImpl
}

// manPageDate is the date written to every generated page's header and HISTORY
// section. It is fixed rather than taken from the clock so regenerating an
// unchanged command tree produces byte-identical pages, which is what lets
// gen-check compare the committed pages with fresh output.
var manPageDate = time.Date(2026, time.November, 21, 0, 0, 0, 0, time.UTC)

// addGenManCommandImpl hangs the hidden man-page generator off the root command.
// The command exists only in the peers_genman build, so the release binary does
// not carry cobra's doc package.
func addGenManCommandImpl(root *cobra.Command, _ *app) {
	root.AddCommand(&cobra.Command{
		Use:    "gen-man <dir>",
		Short:  "Generate the man pages for the command tree",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return generateManPages(root, args[0])
		},
	})
}

// generateManPages writes one page per visible command into dir. Pages for
// commands that no longer exist are removed first, so the directory always
// holds exactly the current command tree. Hidden commands are skipped by
// cobra/doc itself.
func generateManPages(root *cobra.Command, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".1") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("removing stale page %s: %w", entry.Name(), err)
		}
	}
	header := &doc.GenManHeader{
		Title:   "PEERS",
		Section: "1",
		Date:    &manPageDate,
	}
	if err := doc.GenManTree(root, header, dir); err != nil {
		return fmt.Errorf("generating man pages: %w", err)
	}
	return nil
}
