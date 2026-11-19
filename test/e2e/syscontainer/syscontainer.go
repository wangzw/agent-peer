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
	"strconv"
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

const (
	// systemdReadyBound is the total budget for one systemd container to finish
	// booting. It covers every probe, the one rebuild and the stop before it.
	// Evidence collection after this budget is given up has its own bound and
	// does not count here.
	systemdReadyBound = 90 * time.Second

	// systemdProbeBound bounds one is-system-running --wait probe. A healthy
	// boot returns in under a second. A boot still in the initial transaction
	// blocks until this bound kills the probe, and that timeout is the first
	// moment the stall is observable, so it equals systemdBootRebuildDelay.
	systemdProbeBound = systemdBootRebuildDelay

	// systemdPollInterval separates two probes that returned without a ready
	// state and without using the probe bound.
	systemdPollInterval = 100 * time.Millisecond
)

// InContainer reports whether the process was started by an e2e container.
func InContainer() bool {
	return os.Getenv(inContainerEnv) == "1"
}

// testBinary is the host path of the test binary built by Main. Run mounts it
// into the container.
var testBinary string

// testBinaryPath is the path the container sees.
const testBinaryPath = "/opt/agent-peer-test"

// ArtifactPath returns the path inside the container of a file staged with
// StageArtifact.
func ArtifactPath(rel string) string {
	return filepath.Join(testBinaryPath, rel)
}

// StageArtifact writes data to rel under the directory mounted into the
// container at testBinaryPath and returns the path the container sees. It lets
// a host-side test give the inner test a file from the repository, such as the
// install script the release archive carries. The mount is read-only inside
// the container, so the file must be staged before Run starts it.
func StageArtifact(t *testing.T, rel string, data []byte, mode os.FileMode) string {
	t.Helper()

	if InContainer() {
		t.Fatalf("syscontainer: StageArtifact runs on the host")
	}
	if testBinary == "" {
		t.Fatalf("syscontainer: Main was not called, so there is no mount to stage %s in", rel)
	}
	if !filepath.IsLocal(rel) {
		t.Fatalf("syscontainer: staged path %q leaves the mount", rel)
	}
	dst := filepath.Join(filepath.Dir(testBinary), rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("syscontainer: creating the staged directory for %s: %v", rel, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		t.Fatalf("syscontainer: staging %s: %v", rel, err)
	}
	if err := os.Chmod(dst, mode); err != nil {
		t.Fatalf("syscontainer: setting the mode of staged %s: %v", rel, err)
	}
	return ArtifactPath(rel)
}

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
		dir, err := composeDir()
		if err != nil {
			t.Fatalf("syscontainer: %v", err)
		}
		// The tag covers the system Dockerfile as well as the base image: a
		// change confined to the system layer must not reuse an older image.
		dockerfile, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		if err != nil {
			t.Fatalf("syscontainer: reading the system Dockerfile: %v", err)
		}
		tag := "agent-peer-e2e-system:" + shortHash(base+"\x00"+string(dockerfile))
		if imageExists(tag) {
			systemImageTag = tag
			return
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
	// Allocate the subnet before registering down: t.Cleanup runs in reverse,
	// so the network is removed before its slot is handed to another project.
	subnet := allocSystemSubnet(t)
	project := projectName(t.Name())
	env := []string{
		"AGENT_PEER_E2E_SYSTEM_IMAGE=" + image,
		"AGENT_PEER_E2E_BIN=" + filepath.Dir(testBinary),
		"AGENT_PEER_E2E_SYSTEM_SUBNET=" + subnet,
	}
	c := &container{
		t:           t,
		project:     project,
		composeFile: filepath.Join(dir, "compose.yaml"),
		// The compose file mounts this directory at /opt/agent-peer-test.
		env: append(os.Environ(), env...),
		// The inner process needs the values; compose exec reads them from the
		// docker CLI environment, which is this build of env.
		forward: env,
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
		args = append(args, c.forwardEnvArgs()...)
		args = append(args, string(Plain))
		args = append(args, argv...)
		out, err = c.command(args...).CombinedOutput()
	case Systemd:
		if out, err = c.command("compose", "-p", project, "-f", c.composeFile, "up", "-d", "systemd").CombinedOutput(); err != nil {
			t.Fatalf("syscontainer: starting systemd in %s: %v\n%s", project, err, strings.TrimSpace(string(out)))
		}
		c.waitSystemd()
		args := []string{"compose", "-p", project, "-f", c.composeFile, "exec", "-T"}
		args = append(args, c.forwardEnvArgs()...)
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
// variable and the host gateway into the container.
func (c *container) forwardEnvArgs() []string {
	var args []string
	if value, ok := os.LookupEnv(expectFailEnv); ok {
		args = append(args, "-e", expectFailEnv+"="+value)
	}
	for _, entry := range c.forward {
		args = append(args, "-e", entry)
	}
	return args
}

// container is one compose project started for one test.
type container struct {
	t           *testing.T
	project     string
	composeFile string
	env         []string
	// forward is passed into the container with -e, one NAME=value per entry.
	forward []string
}

// command builds one docker compose invocation for the project.
func (c *container) command(args ...string) *exec.Cmd {
	cmd := exec.Command("docker", args...)
	cmd.Env = c.env
	return cmd
}

// systemdRebuildLedger records every systemd container rebuild. make e2e prints
// it after the test run, so a gate that does not pass -v still shows how often
// and why a container was rebuilt. The path is repeated in the Makefile.
const systemdRebuildLedger = "/tmp/ape2e-systemd-rebuilds.log"

// waitSystemd waits until the container's systemd reports running or degraded.
// systemctl --wait blocks until the state settles, so a probe returns either a
// settled state or, when its own bound expires, the stall.
//
// A container is recreated at most once, and only for the two failure modes
// observed under the full gate; bootretry.go names them. The whole wait,
// including the probe, the rebuild and the stop before it, shares one
// systemdReadyBound context. Evidence collection after that budget is given up
// has its own bound and does not extend it.
func (c *container) waitSystemd() {
	ctx, cancel := context.WithTimeout(context.Background(), systemdReadyBound)
	defer cancel()

	start := time.Now()
	rebuilds := 0
	lastState := "no probe completed"

	for {
		if ctx.Err() != nil {
			c.failSystemdBoot(lastState)
		}

		probe := c.probeSystemd(ctx)
		if probe.ready {
			return
		}
		if probe.state != "" {
			lastState = probe.state
		}
		if ctx.Err() != nil {
			c.failSystemdBoot(lastState)
		}

		observation := systemdBootObservation{
			rebuilds: rebuilds,
			elapsed:  time.Since(start),
		}
		switch {
		case probe.exited:
			observation.exited = true
			if code, logs, err := c.containerExit(ctx); err == nil {
				observation.exitCode = code
				observation.logsEmpty = strings.TrimSpace(logs) == ""
			}
		case probe.timedOut:
			observation.probeTimedOut = true
			observation.state = c.systemdCurrentState(ctx)
			lastState = fmt.Sprintf("probe timed out after %s, state %q", systemdProbeBound, observation.state)
		}

		if shouldRebuildSystemd(observation) {
			rebuilds++
			reason := rebuildReason(observation)
			c.recordRebuild(reason)
			c.t.Logf("syscontainer: %s: %s; recreating the container (%d of %d)",
				c.project, reason, rebuilds, systemdBootRebuilds)
			// Keep the evidence of the failed boot before the container is
			// replaced.
			c.logBootEvidence()
			c.recreateSystemd(ctx)
			continue
		}
		if observation.exited {
			// A container whose process 1 is gone does not come back on its
			// own. Any exit that is not the observed 255-with-empty-logs is a
			// real failure, so report it now instead of waiting out the bound.
			c.failSystemdBoot(lastState)
		}

		select {
		case <-ctx.Done():
		case <-time.After(systemdPollInterval):
		}
	}
}

// rebuildReason describes the observation for the rebuild ledger and the test
// log.
func rebuildReason(o systemdBootObservation) string {
	if o.exited {
		return fmt.Sprintf("process 1 exited with code %d and docker logs is empty", o.exitCode)
	}
	return fmt.Sprintf("is-system-running --wait timed out after %s and the state is %q", systemdProbeBound, o.state)
}

// failSystemdBoot writes the evidence and ends the test at the readiness bound.
func (c *container) failSystemdBoot(lastState string) {
	c.logBootEvidence()
	c.t.Fatalf("systemd to finish booting: not within %s (last state: %s)", systemdReadyBound, lastState)
}

// probeSystemd runs one readiness probe under the shared context. The probe's
// own bound is shorter than the readiness bound, and a probe that expires
// while the outer context is still alive is the timeout that can precede a
// rebuild.
func (c *container) probeSystemd(ctx context.Context) systemdProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, systemdProbeBound)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "docker",
		"compose", "-p", c.project, "-f", c.composeFile, "exec", "-T", "systemd",
		"systemctl", "is-system-running", "--wait")
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	timedOut := probeCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil
	return interpretSystemdState(string(out), err, timedOut)
}

// systemdCurrentState reads the state without --wait, which reports the current
// text instead of blocking. It confirms that a timed-out probe was looking at a
// system that was still initializing.
func (c *container) systemdCurrentState(ctx context.Context) string {
	probeCtx, cancel := context.WithTimeout(ctx, systemdProbeBound)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "docker",
		"compose", "-p", c.project, "-f", c.composeFile, "exec", "-T", "systemd",
		"systemctl", "is-system-running")
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return ""
	}
	return text
}

// containerID returns the project's systemd container id, including an exited
// one.
func (c *container) containerID(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker",
		"compose", "-p", c.project, "-f", c.composeFile, "ps", "-aq", "systemd")
	cmd.Env = c.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// containerExit returns process 1's exit code and docker logs for a container
// that compose reported as not running.
func (c *container) containerExit(ctx context.Context) (int, string, error) {
	id, err := c.containerID(ctx)
	if err != nil {
		return 0, "", err
	}
	if id == "" {
		return 0, "", fmt.Errorf("no container id")
	}
	inspect := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.ExitCode}}", id)
	inspect.Env = c.env
	out, err := inspect.CombinedOutput()
	if err != nil {
		return 0, "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, "", fmt.Errorf("parsing the exit code %q: %w", strings.TrimSpace(string(out)), err)
	}
	logsCmd := exec.CommandContext(ctx, "docker", "logs", id)
	logsCmd.Env = c.env
	logs, err := logsCmd.CombinedOutput()
	if err != nil {
		return code, "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(logs)))
	}
	return code, string(logs), nil
}

// recreateSystemd stops the container with a one-second timeout and rebuilds it
// in place, keeping the project's network. Both commands share the readiness
// context. A failure is reported rather than fatal, so the wait continues and
// the final state still explains the outcome.
func (c *container) recreateSystemd(ctx context.Context) {
	if id, err := c.containerID(ctx); err == nil && id != "" {
		stop := exec.CommandContext(ctx, "docker", "stop", "--timeout", "1", id)
		stop.Env = c.env
		if out, err := stop.CombinedOutput(); err != nil {
			c.t.Logf("syscontainer: stopping the systemd container in %s: %v\n%s",
				c.project, err, strings.TrimSpace(string(out)))
		}
	}
	up := exec.CommandContext(ctx, "docker",
		"compose", "-p", c.project, "-f", c.composeFile, "up", "-d", "--force-recreate", "systemd")
	up.Env = c.env
	if out, err := up.CombinedOutput(); err != nil {
		c.t.Logf("syscontainer: recreating the systemd container in %s: %v\n%s",
			c.project, err, strings.TrimSpace(string(out)))
	}
}

// recordRebuild appends one line to systemdRebuildLedger. The ledger is
// appended to with O_APPEND, so concurrent suites do not lose each other's
// lines.
func (c *container) recordRebuild(reason string) {
	line := fmt.Sprintf("%s %s %s\n", time.Now().UTC().Format(time.RFC3339), c.project, reason)
	f, err := os.OpenFile(systemdRebuildLedger, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		c.t.Logf("syscontainer: recording the rebuild: %v", err)
		return
	}
	_, writeErr := f.WriteString(line)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		c.t.Logf("syscontainer: recording the rebuild: %v; closing: %v", writeErr, closeErr)
	}
}

// down tears the project down and removes the network. It is idempotent. The
// one-second stop timeout force-kills a container whose process 1 does not exit
// on the image's stop signal, so a container torn down here can end with exit
// code 137; the residue cleanup only force-removes containers whose owner
// process is already gone.
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

// allocSystemSubnet reserves one /24 for one compose project and returns it as
// a CIDR. Docker refuses two live networks with overlapping subnets, and every
// system test starts its own project, so a subnet fixed in compose.yaml would
// make parallel tests fail. The reservation goes through the harness allocator,
// which locks the shared state directory and records the owner: a second suite
// running at the same time takes another /24 instead of the same one. The range
// is disjoint from the harness routed allocator's.
func allocSystemSubnet(t *testing.T) string {
	t.Helper()

	index, err := harness.ReserveSubnetIndex(t, systemSubnetFirst, systemSubnetLast)
	if err != nil {
		t.Fatalf("syscontainer: reserving a system network subnet: %v", err)
	}
	return systemSubnetCIDR(index)
}

// WaitFor waits up to three seconds for cond to return true, delegating the
// polling to harness.Eventually. Longer waits use harness.Eventually with the
// sum of the relevant timers plus three seconds.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, what, func() (bool, string) {
		return cond(), ""
	})
}
