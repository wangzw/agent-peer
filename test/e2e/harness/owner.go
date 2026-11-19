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

// owner identifies one incarnation of a test process: its PID, the kernel's
// start time for that PID, and the inode of its PID namespace. A reused PID
// belongs to a different process and has a different start time, so the pair
// distinguishes a live run from residue left by an earlier one. The namespace
// keeps a cleanup from reading another namespace's /proc entry, where the same
// PID may name an unrelated process or no process at all.
type owner struct {
	pid   int
	start uint64
	ns    uint64
}

// ownerNamespacePrefix marks the PID namespace field of a name's owner marker.
// The field is spelled out so it cannot be confused with the slug that follows
// the marker.
const ownerNamespacePrefix = "pidns"

// String formats the marker that names carry: "<pid>-<start>-pidns<inode>".
// A platform without PID namespaces records zero, which a cleanup treats as
// unknown and therefore leaves alone on a host that does have namespaces.
func (o owner) String() string {
	return fmt.Sprintf("%d-%d-%s%d", o.pid, o.start, ownerNamespacePrefix, o.ns)
}

var (
	currentOwnerOnce sync.Once
	currentOwnerVal  owner
)

// currentOwner returns the owner of this process. The start time and PID
// namespace are read once, so every project and runtime root of one test
// process carries the same marker.
func currentOwner() owner {
	currentOwnerOnce.Do(func() {
		pid := os.Getpid()
		start, err := processStartTime(pid)
		if err != nil {
			start = 0
		}
		ns, err := processNamespace(pid)
		if err != nil {
			ns = 0
		}
		currentOwnerVal = owner{pid: pid, start: start, ns: ns}
	})
	return currentOwnerVal
}

// parseOwner reads the marker from a name built as
// "ape2e-<pid>-<start>-pidns<inode>...". The fields after the marker do not
// matter. A name written before the namespace field existed parses with a zero
// namespace; a name without the marker returns false.
func parseOwner(name string) (owner, bool) {
	fields := strings.Split(name, "-")
	if len(fields) < 3 || fields[0] != "ape2e" {
		return owner{}, false
	}
	o, ok := makeOwner(fields[1], fields[2])
	if !ok {
		return owner{}, false
	}
	if len(fields) > 3 {
		o.ns = parseNamespaceField(fields[3])
	}
	return o, true
}

// parseOwnerValue reads a marker stored by itself, as
// "<pid>-<start>-pidns<inode>". A reservation written before the namespace
// field existed has two fields and parses with a zero namespace.
func parseOwnerValue(text string) (owner, bool) {
	fields := strings.Split(strings.TrimSpace(text), "-")
	if len(fields) != 2 && len(fields) != 3 {
		return owner{}, false
	}
	o, ok := makeOwner(fields[0], fields[1])
	if !ok {
		return owner{}, false
	}
	if len(fields) == 3 {
		o.ns = parseNamespaceField(fields[2])
	}
	return o, true
}

// parseNamespaceField returns the inode of a namespace field. A field without
// the prefix, or with a number that does not parse, yields zero: the namespace
// is then unknown and the cleanup leaves the resource alone.
func parseNamespaceField(field string) uint64 {
	if !strings.HasPrefix(field, ownerNamespacePrefix) {
		return 0
	}
	ns, err := strconv.ParseUint(field[len(ownerNamespacePrefix):], 10, 64)
	if err != nil {
		return 0
	}
	return ns
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

// alive reports whether the process that carries this owner still runs. It is
// conservative on purpose: a marker from another PID namespace, or one whose
// /proc entry cannot be read for a reason other than "no such process", is
// treated as alive so a cleanup never removes another suite's resources.
func (o owner) alive() bool {
	return o.aliveIn(currentOwner().ns, processStartTime, processExists)
}

// aliveIn reports whether the owner is alive as observed from selfNS. The
// start-time reader and the existence check are parameters so the decision can
// be tested without the system.
//
// A different PID namespace means the marker's PID says nothing about this
// namespace, and an absent namespace on either side means the comparison is
// impossible; both are kept. In the same namespace, only a confirmed ESRCH or
// ENOENT, or a different start time, proves the process is gone. Every other
// read error keeps the resource.
func (o owner) aliveIn(selfNS uint64, startTime func(int) (uint64, error), exists func(int) bool) bool {
	if o.pid <= 0 {
		return false
	}
	if o.ns != 0 && selfNS != 0 && o.ns != selfNS {
		return true
	}
	if o.ns == 0 && selfNS != 0 {
		return true
	}
	if o.ns != 0 && selfNS == 0 {
		return true
	}
	if o.start == 0 {
		return exists(o.pid)
	}
	start, err := startTime(o.pid)
	switch {
	case err == nil:
		return start == o.start
	case errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrNotExist):
		return false
	case errors.Is(err, errors.ErrUnsupported):
		// The platform cannot report a start time, as on a system without
		// /proc; the PID alone decides.
		return exists(o.pid)
	default:
		return true
	}
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
