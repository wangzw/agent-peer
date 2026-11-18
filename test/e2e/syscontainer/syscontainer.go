//go:build e2e

// Package syscontainer runs the system-level end-to-end tests inside
// containers. The host test process forwards each test to a fresh container of
// the requested service, so tests that need root, several accounts, or systemd
// see the environment the system Broker is designed for.
package syscontainer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// Service names the compose service a test runs in.
type Service string

const (
	// Plain is a container that runs an ordinary init and starts nothing.
	Plain Service = "plain"
	// Systemd is a privileged container whose process 1 is systemd.
	Systemd Service = "systemd"
)

// inContainerEnv marks a test process that runs inside the container.
const inContainerEnv = "AGENT_PEER_E2E_IN_CONTAINER"

// expectFailEnv forwards an intentional failure request into the container. It
// is set only by the smoke test that checks failure propagation.
const expectFailEnv = "AGENT_PEER_E2E_EXPECT_FAIL"

// systemdReadyBound bounds the wait for systemd to finish booting.
const systemdReadyBound = 90 * time.Second

// InContainer reports whether the process was started by an e2e container.
func InContainer() bool {
	return os.Getenv(inContainerEnv) == "1"
}

// testBinary is the host path of the test binary built by Main. Run mounts it
// into the container.
var testBinary string

// testBinaryPath is the path the container sees.
const testBinaryPath = "/opt/agent-peer-test"

// Main is called from a package's TestMain. On the host it builds the calling
// package's test binary once for linux, runs the tests, and removes the
// binary's directory. Inside the container it just runs the tests.
func Main(m *testing.M) int {
	if InContainer() {
		return m.Run()
	}

	path, cleanup, err := buildTestBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "syscontainer: %v\n", err)
		return 1
	}
	defer cleanup()
	testBinary = path
	return m.Run()
}

// buildTestBinary compiles the calling package's tests for linux. The binary is
// built without cgo so it runs in the debian-based image.
func buildTestBinary() (string, func(), error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("determining the package directory: %w", err)
	}
	tmp, err := os.MkdirTemp("", "ap-e2e-systest-")
	if err != nil {
		return "", nil, fmt.Errorf("creating the test binary directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	out := filepath.Join(tmp, filepath.Base(dir)+".test")
	cmd := exec.Command("go", "test", "-c", "-tags", "e2e", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH="+runtime.GOARCH,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("building the test binary: %w\n%s", err, strings.TrimSpace(string(combined)))
	}
	return out, cleanup, nil
}

var (
	systemImageOnce sync.Once
	systemImageTag  string
)

// systemImage builds the systemd-derived image once per test process and
// returns its tag. The tag is derived from the base image tag, and an existing
// image with that tag is reused.
func systemImage(t *testing.T) string {
	t.Helper()

	base := harness.Image(t)
	systemImageOnce.Do(func() {
		tag := "agent-peer-e2e-system:" + shortHash(base)
		if imageExists(tag) {
			systemImageTag = tag
			return
		}

		dir, err := composeDir()
		if err != nil {
			t.Fatalf("syscontainer: %v", err)
		}
		cmd := exec.Command("docker", "build",
			"--build-arg", "BASE="+base,
			"-t", tag,
			"-f", filepath.Join(dir, "Dockerfile"),
			dir,
		)
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("syscontainer: docker build of the system image: %v\n%s", err, strings.TrimSpace(string(out)))
		}
		systemImageTag = tag
	})
	if systemImageTag == "" {
		t.Fatalf("syscontainer: the system image was not built")
	}
	return systemImageTag
}

// shortHash returns the first twelve hexadecimal characters of the SHA-256 of
// s.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// imageExists reports whether the tag is in the local image store.
func imageExists(tag string) bool {
	cmd := exec.Command("docker", "image", "inspect", tag)
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

// composeDir returns the absolute path of test/e2e/compose/system.
func composeDir() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "test", "e2e", "compose", "system"), nil
}

var (
	moduleRootOnce sync.Once
	moduleRootPath string
	moduleRootErr  error
)

// moduleRoot returns the root directory of the Go module.
func moduleRoot() (string, error) {
	moduleRootOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOMOD").Output()
		if err != nil {
			moduleRootErr = fmt.Errorf("go env GOMOD: %w", err)
			return
		}
		gomod := strings.TrimSpace(string(out))
		if gomod == "" || gomod == os.DevNull {
			moduleRootErr = fmt.Errorf("not inside a Go module")
			return
		}
		moduleRootPath = filepath.Dir(gomod)
	})
	return moduleRootPath, moduleRootErr
}

// Run runs the current test inside a fresh container of svc and fails t when
// the inner run fails. The test must not touch the container itself.
func Run(t *testing.T, svc Service) {
	t.Helper()
	t.Parallel()

	if InContainer() {
		t.Fatalf("syscontainer: Run was called inside the container")
	}
	if testBinary == "" {
		t.Fatalf("syscontainer: Main was not called, so there is no test binary to run")
	}
	if err := checkCompose(); err != nil {
		t.Fatalf("syscontainer: %v", err)
	}

	image := systemImage(t)
	dir, err := composeDir()
	if err != nil {
		t.Fatalf("syscontainer: %v", err)
	}
	project := projectName(t.Name())
	c := &container{
		t:           t,
		project:     project,
		composeFile: filepath.Join(dir, "compose.yaml"),
		// The compose file mounts this directory at /opt/agent-peer-test.
		env: append(os.Environ(),
			"AGENT_PEER_E2E_SYSTEM_IMAGE="+image,
			"AGENT_PEER_E2E_BIN="+filepath.Dir(testBinary),
		),
	}
	t.Cleanup(c.down)

	argv := []string{
		testBinaryPath + "/" + filepath.Base(testBinary),
		"-test.run", runTestFlag(t.Name()),
		"-test.v",
		"-test.count=1",
	}

	var out []byte
	switch svc {
	case Plain:
		args := []string{"compose", "-p", project, "-f", c.composeFile, "run", "--rm", "-T"}
		args = append(args, forwardEnvArgs()...)
		args = append(args, string(Plain))
		args = append(args, argv...)
		out, err = c.command(args...).CombinedOutput()
	case Systemd:
		if out, err = c.command("compose", "-p", project, "-f", c.composeFile, "up", "-d", "systemd").CombinedOutput(); err != nil {
			t.Fatalf("syscontainer: starting systemd in %s: %v\n%s", project, err, strings.TrimSpace(string(out)))
		}
		if err := c.waitSystemd(); err != nil {
			t.Fatalf("syscontainer: %v", err)
		}
		args := []string{"compose", "-p", project, "-f", c.composeFile, "exec", "-T"}
		args = append(args, forwardEnvArgs()...)
		args = append(args, "systemd")
		args = append(args, argv...)
		out, err = c.command(args...).CombinedOutput()
	default:
		t.Fatalf("syscontainer: unknown service %q", svc)
	}

	if len(out) > 0 {
		t.Logf("syscontainer: output of %s in %s:\n%s", t.Name(), project, strings.TrimSpace(string(out)))
	}
	if err != nil {
		t.Fatalf("syscontainer: %s failed inside the %s container: %v", t.Name(), svc, err)
	}
}

// forwardEnvArgs returns compose flags that forward the intentional-failure
// variable into the container when the host process has it.
func forwardEnvArgs() []string {
	if value, ok := os.LookupEnv(expectFailEnv); ok {
		return []string{"-e", expectFailEnv + "=" + value}
	}
	return nil
}

// container is one compose project started for one test.
type container struct {
	t           *testing.T
	project     string
	composeFile string
	env         []string
}

// command builds one docker compose invocation for the project.
func (c *container) command(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", args...)
	cmd.Env = c.env
	return cmd
}

// waitSystemd waits until the container's systemd reports running or degraded.
func (c *container) waitSystemd() error {
	deadline := time.Now().Add(systemdReadyBound)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "docker",
			"compose", "-p", c.project, "-f", c.composeFile, "exec", "-T", "systemd",
			"systemctl", "is-system-running", "--wait")
		cmd.Env = c.env
		out, err := cmd.CombinedOutput()
		cancel()

		state := strings.TrimSpace(string(out))
		if state == "running" || state == "degraded" {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("waiting for systemd in %s: %v: %s", c.project, err, state)
			}
			return fmt.Errorf("waiting for systemd in %s: state %q", c.project, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// down tears the project down and removes the network. It is idempotent.
func (c *container) down() {
	cmd := c.command("compose", "-p", c.project, "-f", c.composeFile, "down", "-v", "--remove-orphans", "--timeout", "1")
	if out, err := cmd.CombinedOutput(); err != nil {
		c.t.Logf("syscontainer: tearing down %s: %v\n%s", c.project, err, strings.TrimSpace(string(out)))
	}
}

// checkCompose reports whether docker and the compose plugin are usable. A
// host without them fails the test with a reason; it does not skip.
func checkCompose() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker is required for container scenarios: %w", err)
	}
	cmd := exec.Command("docker", "compose", "version")
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker compose is required for container scenarios: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var (
	projectNameMu   sync.Mutex
	projectNameLast string
	projectNameSeq  uint64
)

// projectName derives a compose project name from a test name. Every name
// starts with ape2e- so the residue checks find it.
func projectName(testName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(testName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		slug = "e2e"
	}

	projectNameMu.Lock()
	defer projectNameMu.Unlock()
	for {
		name := "ape2e-" + slug + "-" + randomHex6()
		if name != projectNameLast {
			projectNameLast = name
			return name
		}
	}
}

// randomHex6 returns six random hexadecimal digits.
func randomHex6() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	projectNameSeq++
	return fmt.Sprintf("%06x", projectNameSeq&0xffffff)
}

// WaitFor polls cond every 20ms until it is true or three seconds pass. Longer
// waits use harness.Eventually with the sum of the relevant timers plus three
// seconds.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("syscontainer: %s did not happen within 3s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
