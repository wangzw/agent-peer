package node

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// projectConfigName is the project configuration file read from a workspace.
const projectConfigName = ".peers.yaml"

// GitWorkspace creates dir when it does not exist, initialises a git
// repository there and, when origin is not empty, adds origin as the remote of
// the same name. No commit is made. It returns the absolute path of the
// workspace.
//
// env is the complete environment of git. It should contain HOME and
// GIT_CONFIG_NOSYSTEM=1 so that the host's git configuration is not read;
// GIT_CONFIG_NOSYSTEM=1 is added when it is missing.
func GitWorkspace(dir, origin string, env []string) (string, error) {
	root, err := prepareWorkspaceDir(dir)
	if err != nil {
		return "", err
	}
	if err := gitRun(root, env, "init", "-q"); err != nil {
		return "", err
	}
	if origin != "" {
		if err := gitRun(root, env, "remote", "add", "origin", origin); err != nil {
			return "", err
		}
	}
	return root, nil
}

// ProjectWorkspace creates dir when it does not exist and writes projectYAML
// as .peers.yaml in it. When origin is not empty it also initialises a
// git repository with origin, like GitWorkspace; when origin is empty the
// directory stays a plain one. It returns the absolute path of the workspace.
func ProjectWorkspace(dir, projectYAML, origin string, env []string) (string, error) {
	root, err := prepareWorkspaceDir(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, projectConfigName)
	if err := os.WriteFile(path, []byte(projectYAML), 0o644); err != nil {
		return "", fmt.Errorf("node: writing %s: %w", path, err)
	}
	if origin != "" {
		if err := gitRun(root, env, "init", "-q"); err != nil {
			return "", err
		}
		if err := gitRun(root, env, "remote", "add", "origin", origin); err != nil {
			return "", err
		}
	}
	return root, nil
}

// PlainWorkspace creates dir without a repository or a project configuration
// file and returns its absolute path.
func PlainWorkspace(dir string) (string, error) {
	return prepareWorkspaceDir(dir)
}

// prepareWorkspaceDir creates dir and returns it as an absolute path.
func prepareWorkspaceDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("node: creating workspace %s: %w", dir, err)
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("node: resolving workspace %s: %w", dir, err)
	}
	return root, nil
}

// gitRun runs git in dir with env and reports the command's standard error on
// failure.
func gitRun(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = withGitNoSystem(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		command := "git " + strings.Join(args, " ")
		if text := strings.TrimSpace(stderr.String()); text != "" {
			return fmt.Errorf("node: %s: %w: %s", command, err, text)
		}
		return fmt.Errorf("node: %s: %w", command, err)
	}
	return nil
}

// withGitNoSystem returns env with GIT_CONFIG_NOSYSTEM=1 added unless the
// caller already set it. The slice passed in is never modified.
func withGitNoSystem(env []string) []string {
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_CONFIG_NOSYSTEM=") {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	out = append(out, env...)
	return append(out, "GIT_CONFIG_NOSYSTEM=1")
}
