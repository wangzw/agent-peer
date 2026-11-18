package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// gitOriginTimeout bounds the git command that reads the origin URL.
const gitOriginTimeout = 5 * time.Second

// originReader reads the git origin URL of the repository that contains dir.
// An error means there is no usable origin, for example because dir is not in a
// git repository or the repository has none.
type originReader func(ctx context.Context, dir string) (string, error)

// gitOrigin runs `git -C dir config --local --get remote.origin.url` with a 5s
// deadline. It reports an error when the directory is not a git repository,
// when the repository has no origin, when git is not installed, or when the
// command takes too long.
//
// The --local flag is deliberate: without it git would also read
// remote.origin.url from the user's global configuration, which is not a
// property of the repository.
func gitOrigin(ctx context.Context, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitOriginTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--local", "--get", "remote.origin.url")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("read git origin in %s: %w", dir, ctxErr)
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("read git origin in %s: %w: %s", dir, err, detail)
		}
		return "", fmt.Errorf("read git origin in %s: %w", dir, err)
	}
	url := strings.TrimSpace(stdout.String())
	if url == "" {
		return "", fmt.Errorf("read git origin in %s: empty remote.origin.url", dir)
	}
	return url, nil
}

// defaultTopics returns the topics to join after the first registration, in
// the order they should be joined. It applies the sequence in the design: the
// Broker may switch automatic joining off, the project configuration may switch
// it off or name the topics, and otherwise the normalized git origin names one
// topic. An error along the way yields no topic, never a partial list.
//
// readOrigin is called only when the project configuration does not name the
// topics, so a test or a caller can tell whether the git command was needed.
func defaultTopics(ctx context.Context, dir string, brokerAutoJoin bool, readOrigin originReader, log *slog.Logger) []string {
	if log == nil {
		log = slog.Default()
	}
	if !brokerAutoJoin {
		return nil
	}

	cfg, found, err := findProjectConfigLogged(dir, log)
	if err != nil {
		log.Error("cannot read the project configuration; not joining any default topic",
			"path", cfg.Path, "error", err)
		return nil
	}
	if found {
		if !cfg.AutoJoin {
			return nil
		}
		if len(cfg.Topics) > 0 {
			return cfg.Topics
		}
	}

	raw, err := readOrigin(ctx, dir)
	if err != nil {
		// Being outside a git repository or having no origin is ordinary, so
		// this is a debug message rather than an error.
		log.Debug("no usable git origin; not joining a default topic", "dir", dir, "error", err)
		return nil
	}
	topic, ok := normalizeRemote(raw)
	if !ok {
		log.Debug("the git origin does not name a remote repository; not joining a default topic",
			"dir", dir, "origin", raw)
		return nil
	}
	return []string{topic}
}
