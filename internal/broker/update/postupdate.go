package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// postUpdateTimeout bounds the new binary's post-update step. Rewriting a
	// unit and asking the service manager to reload it is quick; a binary that
	// hangs must not keep the previous version from coming back.
	postUpdateTimeout = 30 * time.Second

	// postUpdateExitNotNeeded is the exit code of a binary that has no
	// post-update command. The contract reserves 2 for exactly that: a later
	// release may drop the command, and the update then has no post-install
	// step to run.
	postUpdateExitNotNeeded = 2
)

// PostUpdateOptions names the installed binary whose post-update step runs and
// the service level whose unit it rewrites.
type PostUpdateOptions struct {
	Binary string
	Level  string
}

// Install installs o's release and then runs the new binary's hidden
// post-update command at level, before anything restarts it. The previous
// binary is kept beside the target until the step succeeds: a failed or
// timed-out step restores it, so the caller can report the failure and leave
// the old version in place.
func Install(ctx context.Context, o ApplyOptions, level string) error {
	if level != "system" && level != "user" {
		return fmt.Errorf("service level %q is not system or user", level)
	}
	backup, err := backupTarget(o.Target)
	if err != nil {
		return err
	}
	if err := Apply(ctx, o); err != nil {
		// Apply promises to leave the target untouched on failure, but it can
		// fail after the rename while syncing the directory. Restoring the
		// hard link covers both cases and is a no-op when the target never
		// changed, because the backup names the same inode.
		if restoreErr := restoreTarget(backup, o.Target); restoreErr != nil {
			return fmt.Errorf("%w; restoring the previous binary failed: %v", err, restoreErr)
		}
		return err
	}
	if err := RunPostUpdate(ctx, PostUpdateOptions{Binary: o.Target, Level: level}); err != nil {
		if restoreErr := restoreTarget(backup, o.Target); restoreErr != nil {
			return fmt.Errorf("%w; restoring the previous binary failed: %v", err, restoreErr)
		}
		return fmt.Errorf("%w; the previous binary was restored", err)
	}
	// The new binary and its rewritten unit are in place; a leftover rollback
	// name is untidy but harmless, and failing the update over it would report
	// a failure that did not happen.
	_ = os.Remove(backup)
	return nil
}

// RunPostUpdate runs binary's "internal post-update --level level", bounded by
// postUpdateTimeout and ctx. Exit code 2 means the binary has no post-update
// command, which is not an error.
func RunPostUpdate(ctx context.Context, o PostUpdateOptions) error {
	if o.Binary == "" {
		return errors.New("no binary to run the post-update step")
	}
	if o.Level != "system" && o.Level != "user" {
		return fmt.Errorf("service level %q is not system or user", o.Level)
	}

	runCtx, cancel := context.WithTimeout(ctx, postUpdateTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, o.Binary, "internal", "post-update", "--level", o.Level)
	// The step runs the service manager commands itself, so it inherits this
	// process's environment: a user-level Broker carries the session bus and
	// runtime directory those commands need.
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == postUpdateExitNotNeeded {
		return nil
	}
	switch runCtx.Err() {
	case context.DeadlineExceeded:
		return fmt.Errorf("the post-update step timed out: %w", runCtx.Err())
	case context.Canceled:
		return fmt.Errorf("the post-update step was canceled: %w", runCtx.Err())
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" {
		return fmt.Errorf("the post-update step failed: %w (stderr: %s)", err, detail)
	}
	return fmt.Errorf("the post-update step failed: %w", err)
}

// backupTarget hard-links target to a fresh name beside it and returns that
// name. A hard link keeps the old inode intact without copying the binary, and
// it leaves target itself in place while the replacement is prepared.
func backupTarget(target string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), ".peers-rollback-*")
	if err != nil {
		return "", fmt.Errorf("reserving a rollback name next to %s: %w", target, err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Remove(name); err != nil {
		return "", fmt.Errorf("removing the reserved rollback name %s: %w", name, err)
	}
	if err := os.Link(target, name); err != nil {
		return "", fmt.Errorf("saving a rollback copy of %s: %w", target, err)
	}
	return name, nil
}

// restoreTarget renames backup over target and flushes the directory entry, so
// the previous binary survives a crash right after a failed update.
func restoreTarget(backup, target string) error {
	if err := os.Rename(backup, target); err != nil {
		return fmt.Errorf("restoring %s from %s: %w", target, backup, err)
	}
	return syncDir(filepath.Dir(target))
}
