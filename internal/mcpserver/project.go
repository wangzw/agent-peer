package mcpserver

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"
)

// projectConfigFile is the name of the project configuration file that
// declares the topics to join automatically.
const projectConfigFile = ".agent-peer.yaml"

// projectConfig is the parsed content of a project configuration file found in
// the session working directory or one of its ancestors.
type projectConfig struct {
	// Path is the absolute path of the file that was read.
	Path string
	// Topics is the declared topic list with empty strings dropped and
	// duplicates removed, keeping the first occurrence.
	Topics []string
	// AutoJoin reports whether the MCP server should join the default topics.
	// It is true when the key is absent.
	AutoJoin bool
}

// projectConfigDocument is the YAML shape of the project configuration file.
// Unknown keys are ignored.
type projectConfigDocument struct {
	Topics   []string `yaml:"topics"`
	AutoJoin *bool    `yaml:"auto_join"`
}

// searchStop bounds the upward search. It is an absolute directory name: when
// the search reaches it, the search stops after looking there instead of
// continuing to "/". Tests set it to a temporary directory so that files above
// it cannot affect the result. Production code leaves it empty.
var searchStop string

// projectOwnerUID returns the uid that owns path, following symbolic links.
// Tests replace it to simulate a file or a directory owned by another account:
// an unprivileged test cannot chown one.
var projectOwnerUID = func(path string) (uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s has no owner information", path)
	}
	return st.Uid, nil
}

// findProjectConfig searches dir and its ancestors up to "/" for a regular file
// named .agent-peer.yaml. A symbolic link is followed: it counts when its
// target is a regular file, and a dangling link or a target that is not a
// regular file is treated as absent, so the search continues upward. found is
// false when there is none. err is non-nil when the first file found cannot be
// read or parsed; found is then true.
func findProjectConfig(dir string) (cfg projectConfig, found bool, err error) {
	return findProjectConfigLogged(dir, nil)
}

// findProjectConfigLogged is findProjectConfig with a logger for the files it
// refuses to adopt. A nil log means slog.Default().
func findProjectConfigLogged(dir string, log *slog.Logger) (cfg projectConfig, found bool, err error) {
	if log == nil {
		log = slog.Default()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return projectConfig{}, false, fmt.Errorf("resolve project directory %q: %w", dir, err)
	}
	for d := abs; ; {
		path := filepath.Join(d, projectConfigFile)
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			if err := checkProjectConfigOwner(path); err != nil {
				// Another account could have planted the file, so it is not a
				// project configuration. Keep looking upward.
				log.Warn("ignoring a project configuration file that is not owned by this account or root",
					"path", path, "error", err)
			} else {
				return readProjectConfig(path)
			}
		}
		if searchStop != "" && d == searchStop {
			return projectConfig{}, false, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return projectConfig{}, false, nil
		}
		d = parent
	}
}

// ownsProjectPath reports whether uid may own a project configuration file or
// the directory that holds it. Root is accepted because a system-wide checkout
// is often root-owned.
func ownsProjectPath(uid uint32) bool {
	return uid == uint32(os.Geteuid()) || uid == 0
}

// checkProjectConfigOwner verifies that the file, after following symbolic
// links, and the directory naming it are owned by this account or root.
func checkProjectConfigOwner(path string) error {
	fileUID, err := projectOwnerUID(path)
	if err != nil {
		return fmt.Errorf("cannot determine the owner: %w", err)
	}
	if !ownsProjectPath(fileUID) {
		return fmt.Errorf("%s is owned by uid %d, not by this account or root", path, fileUID)
	}
	dir := filepath.Dir(path)
	dirUID, err := projectOwnerUID(dir)
	if err != nil {
		return fmt.Errorf("cannot determine the owner of %s: %w", dir, err)
	}
	if !ownsProjectPath(dirUID) {
		return fmt.Errorf("%s is owned by uid %d, not by this account or root", dir, dirUID)
	}
	return nil
}

// readProjectConfig reads and parses one project configuration file. A file
// that cannot be read or parsed is reported with found set: the caller must not
// fall back to an ancestor file.
func readProjectConfig(path string) (cfg projectConfig, found bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return projectConfig{Path: path}, true, fmt.Errorf("read project config %s: %w", path, err)
	}
	var doc projectConfigDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return projectConfig{Path: path}, true, fmt.Errorf("parse project config %s: %w", path, err)
	}
	cfg.Path = path
	cfg.AutoJoin = true
	if doc.AutoJoin != nil {
		cfg.AutoJoin = *doc.AutoJoin
	}
	seen := make(map[string]struct{}, len(doc.Topics))
	for _, topic := range doc.Topics {
		if topic == "" {
			continue
		}
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		cfg.Topics = append(cfg.Topics, topic)
	}
	return cfg, true, nil
}
