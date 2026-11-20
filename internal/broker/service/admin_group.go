package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DefaultAdminGroup is the group that administers a system-level Broker unless
// the installation names another one. Its members can read and write the
// system configuration file without root.
const DefaultAdminGroup = "agent-peer"

// adminGroupNameRE matches a group name both Linux and macOS accept: a letter
// or underscore first, then letters, digits, underscores and hyphens. A name
// that starts with a hyphen is rejected so it cannot be mistaken for an option.
var adminGroupNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

// ValidAdminGroupName reports whether name is a usable administration group
// name.
func ValidAdminGroupName(name string) bool {
	return adminGroupNameRE.MatchString(name)
}

// adminGroupName returns the group the installation delegates administration
// to.
func adminGroupName(opts Options) string {
	if opts.AdminGroup != "" {
		return opts.AdminGroup
	}
	return DefaultAdminGroup
}

// ensureAdminGroup returns the group id of the administration group, creating
// the group when it does not exist. An existing group is used as it is; its
// members are never changed. A system-level installation runs this before it
// writes any file, so a failure to create the group leaves no configuration
// behind.
func ensureAdminGroup(opts Options, res *Result) (int, error) {
	name := adminGroupName(opts)
	if !ValidAdminGroupName(name) {
		return 0, fmt.Errorf("service: invalid administration group name %q", name)
	}
	gid, exists, err := lookupGroup(opts, name)
	if err != nil {
		return 0, err
	}
	if exists {
		return gid, nil
	}
	if err := createAdminGroup(opts, res, name); err != nil {
		return 0, err
	}
	gid, exists, err = lookupGroup(opts, name)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, fmt.Errorf("service: group %q is still missing after creating it", name)
	}
	return gid, nil
}

// lookupGroup reports the group id of name and whether the group exists.
// Options.GroupGID replaces the lookup in a test; otherwise the platform's
// group database is queried through the command runner.
func lookupGroup(opts Options, name string) (int, bool, error) {
	if opts.GroupGID != nil {
		return opts.GroupGID(name)
	}
	switch opts.GOOS {
	case "linux":
		return lookupGroupLinux(opts, name)
	case "darwin":
		return lookupGroupDarwin(opts, name)
	default:
		return 0, false, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
}

// lookupGroupLinux reads the group id from getent. getent exits 2 when the
// group is absent.
func lookupGroupLinux(opts Options, name string) (int, bool, error) {
	out, err := opts.Run("getent", "group", name)
	if err != nil {
		if exitStatus(err) == 2 {
			return 0, false, nil
		}
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return 0, false, fmt.Errorf("service: look up group %q: %w: %s", name, err, msg)
		}
		return 0, false, fmt.Errorf("service: look up group %q: %w", name, err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(fields) < 3 {
		return 0, false, fmt.Errorf("service: getent group %s: unexpected output %q", name, strings.TrimSpace(string(out)))
	}
	gid, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, false, fmt.Errorf("service: getent group %s: group id %q is not a number", name, fields[2])
	}
	return gid, true, nil
}

// lookupGroupDarwin reads the group id from the directory service record.
// dscl exits nonzero when the record does not exist.
func lookupGroupDarwin(opts Options, name string) (int, bool, error) {
	out, err := opts.Run("dscl", ".", "-read", "/Groups/"+name)
	if err != nil {
		return 0, false, nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "PrimaryGroupID" {
			continue
		}
		gid, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false, fmt.Errorf("service: dscl group %s: group id %q is not a number", name, strings.TrimSpace(value))
		}
		return gid, true, nil
	}
	return 0, false, fmt.Errorf("service: dscl group %s: no PrimaryGroupID in output %q", name, strings.TrimSpace(string(out)))
}

// createAdminGroup creates a system group for administration.
func createAdminGroup(opts Options, res *Result, name string) error {
	var argv []string
	switch opts.GOOS {
	case "linux":
		argv = []string{"groupadd", "--system", name}
	case "darwin":
		argv = []string{"dseditgroup", "-o", "create", "-r", "agent-peer administrators", name}
	default:
		return fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	cmdline := strings.Join(argv, " ")
	res.Commands = append(res.Commands, cmdline)
	out, err := opts.Run(argv[0], argv[1:]...)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
}
