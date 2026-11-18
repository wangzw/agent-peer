package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// owner identifies one incarnation of a test process: its PID together with
// the kernel's start time for that PID. A reused PID belongs to a different
// process and has a different start time, so the pair distinguishes a live run
// from residue left by an earlier one.
type owner struct {
	pid   int
	start uint64
}

// String formats the marker that names carry: "<pid>-<start>".
func (o owner) String() string {
	return fmt.Sprintf("%d-%d", o.pid, o.start)
}

var (
	currentOwnerOnce sync.Once
	currentOwnerVal  owner
)

// currentOwner returns the owner of this process. The start time is read once,
// so every project and runtime root of one test process carries the same
// marker.
func currentOwner() owner {
	currentOwnerOnce.Do(func() {
		pid := os.Getpid()
		start, err := processStartTime(pid)
		if err != nil {
			start = 0
		}
		currentOwnerVal = owner{pid: pid, start: start}
	})
	return currentOwnerVal
}

// parseOwner reads the marker from a name built as
// "ape2e-<pid>-<start>...". The fields after the marker do not matter. A name
// without the marker, including one written before the marker existed, returns
// false.
func parseOwner(name string) (owner, bool) {
	fields := strings.Split(name, "-")
	if len(fields) < 3 || fields[0] != "ape2e" {
		return owner{}, false
	}
	return makeOwner(fields[1], fields[2])
}

// parseOwnerValue reads a marker stored by itself, as "<pid>-<start>".
func parseOwnerValue(text string) (owner, bool) {
	fields := strings.Split(strings.TrimSpace(text), "-")
	if len(fields) != 2 {
		return owner{}, false
	}
	return makeOwner(fields[0], fields[1])
}

func makeOwner(pidText, startText string) (owner, bool) {
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		return owner{}, false
	}
	start, err := strconv.ParseUint(startText, 10, 64)
	if err != nil {
		return owner{}, false
	}
	return owner{pid: pid, start: start}, true
}

// parseOwnerFromPath reads the marker from a runtime root such as
// /tmp/ape2e-1234-5678-9012.
func parseOwnerFromPath(path string) (owner, bool) {
	return parseOwner(filepath.Base(path))
}

// alive reports whether the process that carries this owner still runs. A start
// time of zero means the platform cannot report one; the PID alone decides
// then.
func (o owner) alive() bool {
	if o.pid <= 0 {
		return false
	}
	if o.start == 0 {
		return processExists(o.pid)
	}
	start, err := processStartTime(o.pid)
	return err == nil && start == o.start
}

// ownerGone reports whether name carries an owner marker and that owner is
// gone. A name without the marker has an unknown owner and is left alone: it
// may belong to a live suite that predates the marker.
func ownerGone(name string) bool {
	o, ok := parseOwner(name)
	return ok && !o.alive()
}

// processExists reports whether pid names a process.
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
