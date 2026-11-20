package service

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	// systemConfigDir holds the system-level configuration file and the files
	// an administrator adds beside it.
	systemConfigDir = "/etc/agent-peer"
	// systemDataDir holds the system-level identity, peer table and discovery
	// records.
	systemDataDir = "/var/lib/agent-peer"
)

// ServiceBinary returns the binary the service definition of opts' level starts:
// the first field of ExecStart, or the first ProgramArguments item. ok is false
// when there is no definition.
func ServiceBinary(opts Options) (path string, ok bool, err error) {
	defPath, err := unitPath(opts)
	if err != nil {
		return "", false, err
	}
	exists, err := serviceFileExists(opts, defPath)
	if err != nil {
		return "", false, err
	}
	if !exists {
		return "", false, nil
	}
	content, err := os.ReadFile(rooted(opts, defPath))
	if err != nil {
		return "", false, fmt.Errorf("service: read %s: %w", defPath, err)
	}

	switch opts.GOOS {
	case "linux":
		path, err = systemdExecBinary(string(content), defPath)
	case "darwin":
		path, err = launchdProgramBinary(string(content), defPath)
	default:
		return "", false, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", false, fmt.Errorf("service: %s does not name the program the service starts", defPath)
	}
	return path, true, nil
}

// systemdExecBinary reads the first field of the unit's ExecStart line.
func systemdExecBinary(content, defPath string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ExecStart="))
		// systemd allows a run of prefix characters with special meanings.
		value = strings.TrimLeft(value, "-@+!:")
		field, ok := systemdFirstField(value)
		if !ok || field == "" {
			return "", fmt.Errorf("service: %s has an empty ExecStart", defPath)
		}
		// systemd escapes its specifier and variable sigils by doubling them.
		field = strings.ReplaceAll(field, "%%", "%")
		field = strings.ReplaceAll(field, "$$", "$")
		return field, nil
	}
	return "", nil
}

// systemdFirstField returns the first word of a systemd command line. Double
// quotes group words; a backslash escapes the next character.
func systemdFirstField(line string) (string, bool) {
	var b strings.Builder
	quoted := false
	started := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) {
			i++
			b.WriteByte(systemdUnescape(line[i]))
			started = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			started = true
			continue
		}
		if !quoted && (c == ' ' || c == '\t') {
			if started {
				break
			}
			continue
		}
		b.WriteByte(c)
		started = true
	}
	return b.String(), started
}

// systemdUnescape maps one escaped character to its value. The writer in
// units.go only emits backslash and double quote escapes; the rest follow
// systemd's C-style escapes so a hand-edited unit reads correctly.
func systemdUnescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case 's':
		return ' '
	default:
		return c
	}
}

// launchdProgramBinary returns the first ProgramArguments item of a plist.
func launchdProgramBinary(content, defPath string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(content))
	afterKey := false
	inArray := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("service: parse %s: %w", defPath, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return "", fmt.Errorf("service: parse %s: %w", defPath, err)
				}
				afterKey = strings.TrimSpace(text) == "ProgramArguments"
			case "array":
				if afterKey {
					inArray = true
					afterKey = false
				}
			case "string":
				if !inArray {
					continue
				}
				var text string
				if err := dec.DecodeElement(&text, &t); err != nil {
					return "", fmt.Errorf("service: parse %s: %w", defPath, err)
				}
				return text, nil
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				inArray = false
			}
		}
	}
}

// RemoveRuntimeDir removes the runtime directory of opts' level.
func RemoveRuntimeDir(opts Options) ([]string, error) {
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return nil, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	dir := filepath.Dir(localSocketPath(opts))
	removed, err := removePath(opts, dir)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, nil
	}
	return []string{dir}, nil
}

// PurgeData removes the configuration, data, audit log and service log of opts'
// level, the systemd drop-in directory, and at the system level the admin group
// when the installation recorded that it created the group. removed lists the
// paths removed, "group <name>" for a deleted group, and "kept group <name>:
// <reason>" or "kept group with gid <id>: <reason>" for a group that was left
// in place.
func PurgeData(opts Options) (removed []string, err error) {
	switch opts.GOOS {
	case "linux", "darwin":
	default:
		return nil, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	if !opts.System && !filepath.IsAbs(opts.Home) {
		return nil, fmt.Errorf("service: the home directory %q is not an absolute path", opts.Home)
	}

	// The group record and the configuration file's owner are read before
	// anything is removed; the data directory holding the record goes away in
	// the loop below.
	record := ""
	current := ""
	configGID := -1
	configExists := false
	if opts.System {
		record, err = readAdminGroupRecord(opts)
		if err != nil {
			return nil, err
		}
		current, configGID, configExists, err = currentAdminGroupName(opts)
		if err != nil {
			return nil, err
		}
	}

	for _, path := range purgePaths(opts) {
		ok, err := removePath(opts, path)
		if err != nil {
			return removed, err
		}
		if ok {
			removed = append(removed, path)
		}
	}
	if opts.GOOS == "linux" && opts.System {
		// The audit log is the only file this level keeps there, so the
		// directory is empty now. Removing it fails harmlessly when someone
		// left another file in it.
		if pruned, err := pruneEmptyDir(opts, systemLogDir); err != nil {
			return removed, err
		} else if pruned {
			removed = append(removed, systemLogDir)
		}
	}

	if opts.System {
		deletedName, kept, err := purgeAdminGroup(opts, record, current, configGID, configExists)
		if err != nil {
			return removed, err
		}
		if deletedName != "" {
			removed = append(removed, "group "+deletedName)
		}
		if kept != "" {
			removed = append(removed, kept)
		}
	}
	return removed, nil
}

// purgePaths returns the paths PurgeData removes for opts' level.
func purgePaths(opts Options) []string {
	if opts.System {
		paths := []string{systemConfigDir, systemDataDir}
		switch opts.GOOS {
		case "linux":
			paths = append(paths,
				filepath.Join(systemLogDir, "audit.log"),
				filepath.Join(systemdUnitDir, UnitName+".d"),
			)
		case "darwin":
			paths = append(paths, systemLogDir)
		}
		return paths
	}
	paths := []string{filepath.Join(opts.Home, ".agent-peer")}
	if opts.GOOS == "linux" {
		paths = append(paths, filepath.Join(configHome(opts), "systemd", "user", UnitName+".d"))
	} else {
		paths = append(paths, filepath.Join(opts.Home, "Library", "Logs", "agent-peer"))
	}
	return paths
}

// removePath removes path and its contents. A path that does not exist is not
// an error. ok reports whether something was removed.
func removePath(opts Options, path string) (ok bool, err error) {
	rootedPath := rooted(opts, path)
	if _, err := os.Lstat(rootedPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("service: inspect %s: %w", path, err)
	}
	if err := os.RemoveAll(rootedPath); err != nil {
		return false, fmt.Errorf("service: remove %s: %w", path, err)
	}
	return true, nil
}

// pruneEmptyDir removes path when it is an empty directory. A missing or
// non-empty directory is not an error and reports false.
func pruneEmptyDir(opts Options, path string) (bool, error) {
	rootedPath := rooted(opts, path)
	entries, err := os.ReadDir(rootedPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("service: inspect %s: %w", path, err)
	}
	if len(entries) != 0 {
		return false, nil
	}
	if err := os.Remove(rootedPath); err != nil {
		return false, fmt.Errorf("service: remove %s: %w", path, err)
	}
	return true, nil
}

// readAdminGroupRecord returns the group name an installation recorded, or an
// empty string when there is no record.
func readAdminGroupRecord(opts Options) (string, error) {
	path := adminGroupRecordPath
	data, err := os.ReadFile(rooted(opts, path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("service: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// currentAdminGroupName returns the group that owns the system configuration
// file. exists reports whether the file is there, and gid is the file's owner
// group id, or -1 when the file is not there. name is empty when the group
// database does not resolve the id to a name.
func currentAdminGroupName(opts Options) (name string, gid int, exists bool, err error) {
	path := configFilePath(opts)
	info, err := os.Lstat(rooted(opts, path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", -1, false, nil
		}
		return "", -1, false, fmt.Errorf("service: inspect %s: %w", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", -1, true, fmt.Errorf("service: no owner information for %s", path)
	}
	gid = int(st.Gid)
	name, err = groupNameByGID(opts, gid)
	if err != nil {
		return "", gid, true, err
	}
	return name, gid, true, nil
}

// purgeAdminGroup removes the administration group only when the group is safe
// to remove: the installation recorded creating it, or it is the default
// agent-peer group of a v0.2.0 installation whose configuration file belongs to
// it; the record matches the group that currently owns the configuration file;
// and the group is neither the root group nor any enumerated account's primary
// group. Otherwise it returns a kept message with a manual command.
func purgeAdminGroup(opts Options, record, current string, configGID int, configExists bool) (deletedName, kept string, err error) {
	if record == "" {
		// v0.2.0 installations have no record. Only the default group named by
		// the configuration file is provably this project's; any other name may
		// be a system or user group.
		if !configExists || current != DefaultAdminGroup {
			if configExists && current == "" {
				// The file's group id is known but has no name in the group
				// database, so falling back to the default name would point the
				// command at a group that may not be the file's owner. Report the
				// id and look the name up by id when the command runs.
				return "", keptGroupMessageByGID(opts, configGID,
					"the installation did not record creating it, and the group database does not name the group that owns the configuration file"), nil
			}
			name := current
			if name == "" {
				name = DefaultAdminGroup
			}
			return "", keptGroupMessage(opts, name, "the installation did not record creating it"), nil
		}
		record = current
	}
	if !ValidAdminGroupName(record) {
		return "", keptGroupMessage(opts, record, "the recorded name is not a valid group name"), nil
	}
	if configExists {
		if current == "" {
			return "", keptGroupMessage(opts, record, "the configuration file's group could not be read"), nil
		}
		if current != record {
			return "", keptGroupMessage(opts, record, "the configuration file belongs to group "+current), nil
		}
	}

	gid, exists, err := lookupGroup(opts, record)
	if err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", nil
	}
	if gid == 0 {
		return "", keptGroupMessage(opts, record, "it has group id 0"), nil
	}
	primary, err := primaryGroupOfAnyAccount(opts, gid)
	if err != nil {
		return "", keptGroupMessage(opts, record,
			"whether an account uses it as its primary group could not be checked: "+err.Error()+
				"; groupdel refuses to delete a local account's primary group anyway"), nil
	}
	if primary {
		return "", keptGroupMessage(opts, record,
			"it is an account's primary group; only accounts the directory lookup enumerates are checked, and groupdel refuses a local account's primary group anyway"), nil
	}
	if err := deleteAdminGroup(opts, record); err != nil {
		return "", "", err
	}
	return record, "", nil
}

// keptGroupMessage describes a group that was left in place and how to remove
// it by hand.
func keptGroupMessage(opts Options, name, reason string) string {
	return fmt.Sprintf("kept group %s: %s; remove it manually with: %s",
		name, reason, adminGroupDeleteCommand(opts, name))
}

// adminGroupDeleteCommand returns the platform's group deletion command.
func adminGroupDeleteCommand(opts Options, name string) string {
	if opts.GOOS == "darwin" {
		return "dseditgroup -o delete " + name
	}
	return "groupdel " + name
}

// keptGroupMessageByGID describes a group that was left in place when only its
// group id is known, and how to remove it by hand. The command looks the name up
// by id, so it never falls back to a name the group database did not confirm.
func keptGroupMessageByGID(opts Options, gid int, reason string) string {
	return fmt.Sprintf("kept group with gid %d: %s; remove it manually with: %s",
		gid, reason, adminGroupDeleteCommandByGID(opts, gid))
}

// adminGroupDeleteCommandByGID returns the platform's group deletion command
// with the group name resolved from gid at run time.
func adminGroupDeleteCommandByGID(opts Options, gid int) string {
	id := strconv.Itoa(gid)
	if opts.GOOS == "darwin" {
		return `dseditgroup -o delete "$(dscl . -list /Groups PrimaryGroupID | awk '$2==` + id + ` {print $1}')"`
	}
	return `groupdel "$(getent group ` + id + ` | cut -d: -f1)"`
}

// primaryGroupOfAnyAccount reports whether gid is the primary group of an
// account. The list is whatever the platform's directory lookup returns; an
// account it does not enumerate is not seen. groupdel itself refuses to remove
// a local account's primary group, so a missed account still leaves the group
// in place.
func primaryGroupOfAnyAccount(opts Options, gid int) (bool, error) {
	id := strconv.Itoa(gid)
	switch opts.GOOS {
	case "linux":
		out, err := opts.Run("getent", "passwd")
		if err != nil {
			return false, fmt.Errorf("service: list accounts: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) >= 4 && fields[3] == id {
				return true, nil
			}
		}
		return false, nil
	case "darwin":
		out, err := opts.Run("dscl", ".", "-list", "/Users", "PrimaryGroupID")
		if err != nil {
			return false, fmt.Errorf("service: list accounts: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == id {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
}

// groupNameByGID returns the name of the group with gid, or an empty string
// when the group database has no such group.
func groupNameByGID(opts Options, gid int) (string, error) {
	id := strconv.Itoa(gid)
	switch opts.GOOS {
	case "linux":
		out, err := opts.Run("getent", "group", id)
		if err != nil {
			if exitStatus(err) == 2 {
				return "", nil
			}
			return "", fmt.Errorf("service: look up group %s: %w", id, err)
		}
		fields := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(fields) == 0 || fields[0] == "" {
			return "", fmt.Errorf("service: getent group %s: unexpected output %q", id, strings.TrimSpace(string(out)))
		}
		return fields[0], nil
	case "darwin":
		out, err := opts.Run("dscl", ".", "-list", "/Groups", "PrimaryGroupID")
		if err != nil {
			return "", fmt.Errorf("service: list groups: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == id {
				return fields[0], nil
			}
		}
		return "", nil
	default:
		return "", fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
}

// deleteAdminGroup removes the administration group. The caller has already
// checked that the group exists and may be removed.
func deleteAdminGroup(opts Options, name string) error {
	var argv []string
	switch opts.GOOS {
	case "linux":
		argv = []string{"groupdel", name}
	case "darwin":
		argv = []string{"dseditgroup", "-o", "delete", name}
	default:
		return fmt.Errorf("service: unsupported platform %q", opts.GOOS)
	}
	cmdline := strings.Join(argv, " ")
	out, err := opts.Run(argv[0], argv[1:]...)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("service: %s: %w: %s", cmdline, err, msg)
		}
		return fmt.Errorf("service: %s: %w", cmdline, err)
	}
	return nil
}

// OtherAccountBrokers lists the accounts other than opts.UID whose user-level
// Broker answers on /tmp/agent-peer-<uid>/broker.sock.
func OtherAccountBrokers(opts Options) (accounts []string, err error) {
	tmp := rooted(opts, "/tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("service: read %s: %w", tmp, err)
	}
	if opts.Probe == nil {
		return nil, nil
	}

	type broker struct {
		uid  int
		name string
	}
	var found []broker
	for _, entry := range entries {
		name := entry.Name()
		uidText, ok := strings.CutPrefix(name, "agent-peer-")
		if !ok {
			continue
		}
		uid, err := strconv.Atoi(uidText)
		if err != nil || uid == opts.UID {
			continue
		}
		socket := filepath.Join("/tmp", name, "broker.sock")
		if _, answering := opts.Probe(socket); !answering {
			continue
		}
		found = append(found, broker{uid: uid, name: accountName(opts, uid)})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].uid < found[j].uid })

	accounts = make([]string, len(found))
	for i, b := range found {
		accounts[i] = b.name
	}
	return accounts, nil
}

// accountName returns the account name of uid, or the uid itself when the
// account database does not know it.
func accountName(opts Options, uid int) string {
	id := strconv.Itoa(uid)
	switch opts.GOOS {
	case "linux":
		if out, err := opts.Run("getent", "passwd", id); err == nil {
			if fields := strings.Split(strings.TrimSpace(string(out)), ":"); len(fields) > 0 && fields[0] != "" {
				return fields[0]
			}
		}
	case "darwin":
		if out, err := opts.Run("dscl", ".", "-list", "/Users", "UniqueID"); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[1] == id {
					return fields[0]
				}
			}
		}
	}
	if u, err := user.LookupId(id); err == nil && u.Username != "" {
		return u.Username
	}
	return id
}
