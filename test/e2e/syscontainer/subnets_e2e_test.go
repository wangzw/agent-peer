//go:build e2e

package syscontainer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// subnetChildEnv marks the child copy of this test binary that allocates one
// system subnet and holds it while the parent reads the other child's answer.
const subnetChildEnv = "AGENT_PEER_E2E_SUBNET_CHILD"

// TestAllocSystemSubnetCrossProcess starts two copies of this test binary, has
// each reserve a system subnet, and requires the two answers to differ. The
// in-process allocator alone lets both copies take the first free /24, and
// docker then refuses the second network with "Pool overlaps with other one on
// this address space". The reservation must therefore be cross-process, like
// the harness routed allocator's.
func TestAllocSystemSubnetCrossProcess(t *testing.T) {
	if os.Getenv(subnetChildEnv) == "1" {
		subnet := allocSystemSubnet(t)
		fmt.Printf("SUBNET %s\n", subnet)
		// Stay alive while the parent reads the other child's answer, so the
		// two reservations overlap in time.
		time.Sleep(2 * time.Second)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}

	type child struct {
		cmd     *exec.Cmd
		scanner *bufio.Scanner
		stderr  *strings.Builder
	}
	start := func() *child {
		cmd := exec.Command(exe, "-test.run", "^TestAllocSystemSubnetCrossProcess$", "-test.v")
		cmd.Env = append(os.Environ(), subnetChildEnv+"=1")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("child stdout: %v", err)
		}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting child: %v", err)
		}
		return &child{cmd: cmd, scanner: bufio.NewScanner(stdout), stderr: &stderr}
	}

	first := start()
	second := start()

	subnetOf := func(c *child, label string) string {
		result := make(chan string, 1)
		go func() {
			for c.scanner.Scan() {
				line := c.scanner.Text()
				if strings.HasPrefix(line, "SUBNET ") {
					result <- strings.TrimSpace(strings.TrimPrefix(line, "SUBNET "))
					return
				}
			}
			result <- ""
		}()
		select {
		case subnet := <-result:
			if subnet == "" {
				t.Errorf("%s did not report a subnet; stderr:\n%s", label, c.stderr.String())
			}
			return subnet
		case <-time.After(20 * time.Second):
			t.Fatalf("%s did not report a subnet within 20s; stderr:\n%s", label, c.stderr.String())
			return ""
		}
	}

	one := subnetOf(first, "first child")
	two := subnetOf(second, "second child")

	for _, c := range []*child{first, second} {
		if err := c.cmd.Wait(); err != nil {
			t.Errorf("child exit: %v; stderr:\n%s", err, c.stderr.String())
		}
	}

	if one == "" || two == "" {
		return
	}
	if one == two {
		t.Fatalf("two processes both allocated %s; the system subnet reservation is not cross-process", one)
	}
}
