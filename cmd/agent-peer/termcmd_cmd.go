package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
)

func init() {
	subcommands["status"] = (*app).statusCmd
	subcommands["audit"] = (*app).auditCmd
}

const (
	statusUsage = "usage: agent-peer status\n"
	auditUsage  = "usage: agent-peer audit [--limit <n>]\n"
)

// statusCmd implements "agent-peer status". It connects to a running Broker
// without launching one and writes the status to standard output.
func (a *app) statusCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprint(a.stdout, statusUsage)
			return exitOK
		}
		_, _ = fmt.Fprint(a.stderr, statusUsage)
		_, _ = fmt.Fprintf(a.stderr, "agent-peer status: %v\n", err)
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprint(a.stderr, statusUsage)
		_, _ = fmt.Fprintf(a.stderr, "agent-peer status: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	if err := a.deps.runStatus(ctx, a.stdout); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer status: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// auditCmd implements "agent-peer audit". It reads the audit log from a
// running Broker, without launching one, and writes one line per event.
func (a *app) auditCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := uint64(0)
	// Parse in base 10: the flag package's Uint64 reads a leading zero as an
	// octal prefix, so "--limit 010" would silently mean eight.
	fs.Func("limit", "maximum number of events to read; 0 means all", func(value string) error {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		limit = parsed
		return nil
	})
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, _ = fmt.Fprint(a.stdout, auditUsage)
			return exitOK
		}
		_, _ = fmt.Fprint(a.stderr, auditUsage)
		_, _ = fmt.Fprintf(a.stderr, "agent-peer audit: %v\n", err)
		return exitUsage
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprint(a.stderr, auditUsage)
		_, _ = fmt.Fprintf(a.stderr, "agent-peer audit: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if limit > math.MaxUint32 {
		_, _ = fmt.Fprint(a.stderr, auditUsage)
		_, _ = fmt.Fprint(a.stderr, "agent-peer audit: --limit must be at most 4294967295\n")
		return exitUsage
	}

	if err := a.deps.runAudit(ctx, a.stdout, uint32(limit)); err != nil {
		_, _ = fmt.Fprintf(a.stderr, "agent-peer audit: %v\n", err)
		return exitFailure
	}
	return exitOK
}
