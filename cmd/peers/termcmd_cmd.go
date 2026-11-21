package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/wangzw/peers/internal/mcpserver/termcmd"
)

// parseAuditLimit parses the --limit value in base 10 and bounds it to the
// uint32 the local interface accepts. pflag's Uint64 would read a leading zero
// as an octal prefix, so the value stays text until here.
func parseAuditLimit(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed > math.MaxUint32 {
		return 0, fmt.Errorf("--limit must be at most %d", uint64(math.MaxUint32))
	}
	return parsed, nil
}

// serviceStatusCommand builds "service status". It connects to a running
// Broker without launching one and writes the status to standard output.
func (a *app) serviceStatusCommand() *cobra.Command {
	var level *levelFlags
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the status of the selected Broker",
		Args:  noPositionalArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			system, err := level.system(a.deps.euid())
			if err != nil {
				return err
			}
			if err := a.deps.runStatus(cmd.Context(), system, a.stdout); err != nil {
				// The status already names the cause and the repair; a degraded
				// Broker that serves diagnostics only makes the command fail
				// without repeating them on standard error.
				if errors.Is(err, termcmd.ErrDiagnosticsOnly) {
					return commandResult(exitFailure)
				}
				return err
			}
			return nil
		},
	}
	level = addLevelFlags(cmd)
	return cmd
}

// serviceAuditCommand builds "service audit". It reads the audit log from a
// running Broker, without launching one, and writes one line per event.
func (a *app) serviceAuditCommand() *cobra.Command {
	var (
		limitText string
		level     *levelFlags
	)
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Read the audit log from the selected Broker",
		Args:  noPositionalArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			system, err := level.system(a.deps.euid())
			if err != nil {
				return err
			}
			// Parse in base 10: a leading zero must not select an octal
			// prefix, so "--limit 010" means ten.
			limit, err := parseAuditLimit(limitText)
			if err != nil {
				return usageError(err)
			}
			return a.deps.runAudit(cmd.Context(), system, a.stdout, uint32(limit))
		},
	}
	level = addLevelFlags(cmd)
	cmd.Flags().StringVar(&limitText, "limit", "0", "maximum number of events to read; 0 means all")
	return cmd
}
