package harness

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wangzw/peers/test/e2e/node"
)

// runtimeRootPrefix is the directory prefix NewLocalHost gives every runtime
// root. A process whose environment carries a root under it belongs to a test
// run, whether or not that run is still alive.
const runtimeRootPrefix = "/tmp/ape2e"

// supervisorChildEnv marks the copy of the test binary that runs the tests. The
// process go test starts does not run them itself: it waits for this child and
// cleans up after it. A go test timeout panics without unwinding, so the only
// way to clean up before go test returns is from the parent.
const supervisorChildEnv = "APE2E_TEST_SUPERVISOR_CHILD"

// CleanResidue removes what runs whose owner is gone leave behind: processes
// whose runtime root is under /tmp/ape2e, the compose containers, networks and
// volumes named ape2e-*, the test images named peers-e2e, and the runtime
// roots themselves. Every project, root and image carries its owner's PID,
// start time and PID namespace, so resources of a live owner are left alone and
// two suites can run on one host. A resource whose owner lies in another PID
// namespace is left alone too: this process cannot tell whether that owner is
// alive. It tolerates an empty system and reports failures through logf instead
// of stopping.
func CleanResidue(logf func(format string, args ...any)) {
	killRuntimeProcesses(logf)
	removeDockerResidue(logf)
	removeDockerImages(logf)
	cleanSubnetReservations(logf)
	cleanPortReservations(logf)
	removeRuntimeRoots(logf)
}

// IsSupervisorChild reports whether this process is the copy that runs the
// tests, rather than the supervisor that waits for it.
func IsSupervisorChild() bool {
	return os.Getenv(supervisorChildEnv) == "1"
}

// RunTestProcess runs a child copy of the test binary with args and returns its
// exit code. The child is killed by the kernel if this process dies, so a
// killed supervisor does not leave a stray test run behind.
func RunTestProcess(logf func(format string, args ...any), args []string) int {
	exe, err := os.Executable()
	if err != nil {
		logf("cleanup: locating the test binary: %v", err)
		return 1
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), supervisorChildEnv+"=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	setParentDeath(cmd.SysProcAttr)
	if err := cmd.Start(); err != nil {
		logf("cleanup: starting the test process: %v", err)
		return 1
	}
	err = cmd.Wait()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code > 0 {
			return code
		}
		return 1
	}
	logf("cleanup: waiting for the test process: %v", err)
	return 1
}

// killRuntimeProcesses kills every process whose runtime root belongs to an
// owner that is gone. A process started by a live suite is left alone, because
// that suite is still using it.
func killRuntimeProcesses(logf func(format string, args ...any)) {
	procs, err := node.ListProcs(func(p node.Proc) bool {
		root := p.Env["PEERS_TEST_RUNTIME_ROOT"]
		return strings.HasPrefix(root, runtimeRootPrefix) && ownerGone(filepath.Base(root))
	})
	if err != nil {
		if !errors.Is(err, errors.ErrUnsupported) {
			logf("cleanup: listing processes: %v", err)
		}
		return
	}
	self := os.Getpid()
	for _, proc := range procs {
		if proc.PID == self {
			continue
		}
		if err := syscall.Kill(proc.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			logf("cleanup: killing process %d: %v", proc.PID, err)
		}
	}
}

// dockerResource is one container, network or volume with the compose project
// that owns it. The name is the fallback marker when a resource carries no
// project label.
type dockerResource struct {
	id      string
	name    string
	project string
}

// deadDockerIDs returns the resources whose owner is gone. Resources without a
// marker have an unknown owner and are left alone.
func deadDockerIDs(resources []dockerResource) []string {
	var ids []string
	for _, resource := range resources {
		marker := resource.project
		if marker == "" {
			marker = resource.name
		}
		if ownerGone(marker) {
			ids = append(ids, resource.id)
		}
	}
	return ids
}

// removeDockerResidue removes the containers, networks and volumes of every
// ape2e-* compose project whose owner is gone.
func removeDockerResidue(logf func(format string, args ...any)) {
	if ids := deadDockerIDs(dockerContainers()); len(ids) > 0 {
		if out, err := dockerOutput(append([]string{"rm", "-f"}, ids...)...); err != nil {
			logf("cleanup: removing containers: %v: %s", err, strings.TrimSpace(out))
		}
	}
	// A network can linger for a moment after its last container stops, so try
	// twice before reporting it.
	for attempt := 0; attempt < 2; attempt++ {
		ids := deadDockerIDs(dockerNetworks())
		if len(ids) == 0 {
			break
		}
		if out, err := dockerOutput(append([]string{"network", "rm"}, ids...)...); err != nil {
			logf("cleanup: removing networks: %v: %s", err, strings.TrimSpace(out))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if names := deadDockerIDs(dockerVolumes()); len(names) > 0 {
		if out, err := dockerOutput(append([]string{"volume", "rm", "-f"}, names...)...); err != nil {
			logf("cleanup: removing volumes: %v: %s", err, strings.TrimSpace(out))
		}
	}
}

// removeDockerImages removes the peers-e2e images whose owner is gone.
// The image tag names its owner, so the image of a live suite is left alone
// even when both suites run as the same account. A tag without the marker was
// built before the marker existed; its owner cannot be told apart from a live
// one, so it is left alone too.
func removeDockerImages(logf func(format string, args ...any)) {
	refs := dockerLines("images", "peers-e2e", "--format", "{{.Repository}}:{{.Tag}}")
	for _, ref := range refs {
		if !imageTagOwnerGone(ref) {
			continue
		}
		if out, err := dockerOutput("image", "rm", "-f", ref); err != nil {
			logf("cleanup: removing image %s: %v: %s", ref, err, strings.TrimSpace(out))
		}
	}
}

// dockerContainers lists the ape2e-* containers with their compose project.
func dockerContainers() []dockerResource {
	return dockerResources("ps", "-a", "--filter", "name=ape2e-", "--format",
		`{{.ID}} {{.Names}} {{.Label "com.docker.compose.project"}}`)
}

// dockerNetworks lists the ape2e-* networks with their compose project.
func dockerNetworks() []dockerResource {
	return dockerResources("network", "ls", "--filter", "name=ape2e-", "--format",
		`{{.ID}} {{.Name}} {{.Label "com.docker.compose.project"}}`)
}

// dockerVolumes lists the ape2e-* volumes with their compose project. The name
// is the identifier volume rm takes.
func dockerVolumes() []dockerResource {
	return dockerResources("volume", "ls", "--filter", "name=ape2e-", "--format",
		`{{.Name}} {{.Label "com.docker.compose.project"}}`)
}

func dockerResources(args ...string) []dockerResource {
	var resources []dockerResource
	for _, line := range dockerLines(args...) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		resource := dockerResource{id: fields[0], name: fields[0]}
		if len(fields) > 1 {
			resource.name = fields[1]
		}
		if len(fields) > 2 {
			resource.project = fields[2]
		}
		resources = append(resources, resource)
	}
	return resources
}

// removeRuntimeRoots removes every runtime root whose owner is gone and leaves
// the roots of live suites alone.
func removeRuntimeRoots(logf func(format string, args ...any)) {
	paths, err := filepath.Glob(runtimeRootPrefix + "*")
	if err != nil {
		logf("cleanup: listing runtime roots: %v", err)
		return
	}
	for _, path := range paths {
		if !ownerGone(filepath.Base(path)) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			logf("cleanup: removing runtime root %s: %v", path, err)
		}
	}
}

// dockerLines runs one docker query and returns its non-empty output lines.
func dockerLines(args ...string) []string {
	out, err := dockerOutput(args...)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// dockerOutput runs one docker command and returns its combined output.
func dockerOutput(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return string(out), err
}
