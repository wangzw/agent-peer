package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wangzw/peers/internal/broker/service"
)

const uninstallUsage = "usage: peers uninstall [--purge]\n"

// The prompts are written to the controlling terminal, never to standard
// input or output.
const (
	uninstallDataPrompt   = "Also remove configuration, identity, audit and service logs? [y/N] "
	uninstallSystemPrompt = "Uninstall the system-level installation (requires sudo)? [y/N] "
)

// The test seams replace the terminal, sudo, the account and the installation
// steps so the command can be checked without touching the system.
var (
	uninstallOptionsFn            = service.DefaultOptions
	uninstallInstalledFn          = service.Installed
	uninstallServiceFn            = service.Uninstall
	uninstallServiceBinaryFn      = service.ServiceBinary
	uninstallRemoveRuntimeDirFn   = service.RemoveRuntimeDir
	uninstallPurgeDataFn          = service.PurgeData
	uninstallOtherAccountsFn      = service.OtherAccountBrokers
	uninstallMCPFn                = defaultRemoveMCPRegistration
	uninstallMCPLookupFn          = defaultLookupPeerMCP
	uninstallExecutableFn         = os.Executable
	uninstallEvalSymlinksFn       = filepath.EvalSymlinks
	uninstallGeteuidFn            = os.Geteuid
	uninstallGetenvFn             = os.Getenv
	uninstallLookPathFn           = exec.LookPath
	uninstallAskFn                = askOnTTY
	uninstallSudoFn               = runSudo
	uninstallFileOwnerFn          = fileOwner
	uninstallRemoveFileFn         = os.Remove
	uninstallSystemInstalledCheck = func(opts service.Options) bool {
		installed, err := service.Installed(opts)
		return err == nil && installed
	}
)

func init() {
	subcommands["uninstall"] = (*app).uninstallCmd
}

// mcpPeerCommand reports whether the peer MCP registration runs the binary at
// exe. Symlinks are resolved first, because the registration stores the path
// the install used.
func mcpPeerCommand(account, exe string) bool {
	reg := uninstallMCPLookupFn(account)
	if reg.command == "" {
		return false
	}
	command := reg.command
	target := exe
	if resolved, err := uninstallEvalSymlinksFn(exe); err == nil {
		target = resolved
	}
	return command == target || command == exe
}

// installationBinary returns the running executable when it sits at the default
// installation prefix and the MCP registration points to it. A copy downloaded
// elsewhere and run through sudo is not an installation, so uninstall leaves it
// in place.
func installationBinary(opts service.Options, account, exe string) (string, bool) {
	defaultPath := defaultInstallPath(opts)
	if !samePath(exe, defaultPath) && !samePath(exe, rootedPath(opts, defaultPath)) {
		return "", false
	}
	if mcpPeerCommand(account, exe) {
		return resolvePath(exe), true
	}
	if mcpPeerCommand(account, defaultPath) {
		return resolvePath(defaultPath), true
	}
	return "", false
}

// samePath reports whether two paths name the same location after cleaning.
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// defaultInstallPath is where an installation places the binary when no prefix
// is given.
func defaultInstallPath(opts service.Options) string {
	if opts.System {
		return "/usr/local/bin/agent-peer"
	}
	return filepath.Join(opts.Home, ".local", "bin", "agent-peer")
}

func defaultLookupPeerMCP(account string) mcpRegistration {
	return lookupPeerMCP(runMCPCommand, account)
}

func defaultRemoveMCPRegistration(account, binary string) (string, error) {
	return removeMCPRegistration(runMCPCommand, account, binary)
}

// runMCPCommand runs a command for the MCP steps.
func runMCPCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// uninstallCmd implements "peers uninstall [--purge]". It removes the
// installation of the account that runs it, or the system-level installation
// when run as root, and optionally the configuration, identity and logs.
func (a *app) uninstallCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	purge := fs.Bool("purge", false, "also remove configuration, identity and logs")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.stdout, uninstallUsage)
			return exitOK
		}
		return a.uninstallUsageError(err.Error())
	}
	if fs.NArg() > 0 {
		return a.uninstallUsageError(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}

	userOpts, err := uninstallOptionsFn(false)
	if err != nil {
		return a.uninstallFail("determine the options", err)
	}
	if !filepath.IsAbs(userOpts.Home) {
		return a.uninstallFail("determine the options",
			fmt.Errorf("the home directory %q is not an absolute path", userOpts.Home))
	}
	systemOpts, err := uninstallOptionsFn(true)
	if err != nil {
		return a.uninstallFail("determine the options", err)
	}

	if uninstallGeteuidFn() == 0 {
		return a.uninstallAsRoot(ctx, systemOpts, *purge)
	}
	return a.uninstallAsAccount(ctx, userOpts, systemOpts, *purge)
}

// uninstallAsRoot removes the system-level installation. Root has no
// user-level installation of its own.
func (a *app) uninstallAsRoot(ctx context.Context, opts service.Options, purge bool) int {
	noServiceBinary := ""
	installed, err := uninstallInstalledFn(opts)
	if err != nil {
		return a.uninstallFail("inspect the installation", err)
	}
	if !installed {
		// An installation that used --no-service left only the MCP
		// registration. Root's own registration and sudo's caller are both
		// checked, since either may be the account the install belonged to.
		exe, err := uninstallExecutableFn()
		if err != nil {
			return a.uninstallFail("locate the executable", err)
		}
		accounts := []string{""}
		if user := uninstallGetenvFn("SUDO_USER"); user != "" && user != "root" {
			accounts = append(accounts, user)
		}
		for _, account := range accounts {
			if binary, ok := installationBinary(opts, account, exe); ok {
				noServiceBinary = binary
				installed = true
				break
			}
		}
	}
	if !installed {
		_, _ = fmt.Fprintln(a.stdout, "peers is not installed")
		return exitOK
	}
	return a.uninstallLevel(ctx, opts, true, purge, noServiceBinary)
}

// uninstallAsAccount removes this account's user-level installation. When only
// a system-level installation exists, it asks whether to run the uninstall
// through sudo.
func (a *app) uninstallAsAccount(ctx context.Context, userOpts, systemOpts service.Options, purge bool) int {
	noServiceBinary := ""
	installed, err := uninstallInstalledFn(userOpts)
	if err != nil {
		return a.uninstallFail("inspect the installation", err)
	}
	if !installed {
		exe, err := uninstallExecutableFn()
		if err != nil {
			return a.uninstallFail("locate the executable", err)
		}
		if binary, ok := installationBinary(userOpts, "", exe); ok {
			noServiceBinary = binary
			installed = true
		}
	}
	if installed {
		code := a.uninstallLevel(ctx, userOpts, false, purge, noServiceBinary)
		if code != exitOK {
			return code
		}
		if uninstallSystemInstalledCheck(systemOpts) {
			_, _ = fmt.Fprintf(a.stdout, "A system-level installation is still present; run: sudo peers uninstall%s\n", flagSuffix(purge))
		}
		return exitOK
	}

	systemInstalled, err := uninstallInstalledFn(systemOpts)
	if err != nil {
		return a.uninstallFail("inspect the installation", err)
	}
	if !systemInstalled {
		_, _ = fmt.Fprintln(a.stdout, "peers is not installed")
		return exitOK
	}

	yes, asked := uninstallAskFn(uninstallSystemPrompt)
	if !asked || !yes {
		_, _ = fmt.Fprintf(a.stdout, "Run: sudo peers uninstall%s\n", flagSuffix(purge))
		return exitOK
	}
	if _, err := uninstallLookPathFn("sudo"); err != nil {
		return a.uninstallFail("run sudo", fmt.Errorf("sudo was not found: %w", err))
	}
	exe, err := uninstallExecutableFn()
	if err != nil {
		return a.uninstallFail("locate the executable", err)
	}
	sudoArgs := []string{"uninstall"}
	if purge {
		sudoArgs = append(sudoArgs, "--purge")
	}
	code, err := uninstallSudoFn(ctx, exe, sudoArgs, a.stdin, a.stdout, a.stderr)
	if err != nil {
		return a.uninstallFail("run sudo", err)
	}
	return code
}

// uninstallLevel performs the steps for one level. binary is set only when the
// level has no service definition and the executable itself is the
// installation.
func (a *app) uninstallLevel(ctx context.Context, opts service.Options, system, purge bool, binary string) int {
	account := ""
	if system {
		account = uninstallGetenvFn("SUDO_USER")
		if account == "root" {
			account = ""
		}
	}

	if binary == "" {
		path, ok, err := uninstallServiceBinaryFn(opts)
		if err != nil {
			return a.uninstallFail("read the service definition", err)
		}
		if ok {
			binary = path
		} else {
			// The service manager still knows the unit although its file is
			// gone. Only the running executable at the default prefix whose
			// MCP registration points to it counts as the installation; any
			// other copy is left alone.
			exe, err := uninstallExecutableFn()
			if err != nil {
				return a.uninstallFail("locate the executable", err)
			}
			if candidate, ok := installationBinary(opts, account, exe); ok {
				binary = candidate
			} else {
				_, _ = fmt.Fprintf(a.stdout, "kept the running executable %s: it is not the installation binary at %s\n",
					exe, defaultInstallPath(opts))
			}
		}
	}

	purgeData := purge
	if !purgeData {
		if yes, asked := uninstallAskFn(uninstallDataPrompt); asked && yes {
			purgeData = true
		}
	}

	result, err := uninstallServiceFn(opts)
	if err != nil {
		return a.uninstallFail("stop and remove the service", err)
	}
	if result.Removed {
		_, _ = fmt.Fprintf(a.stdout, "stopped and removed the service %s\n", result.UnitPath)
	} else {
		_, _ = fmt.Fprintln(a.stdout, "the service was not installed")
	}

	if binary == "" {
		_, _ = fmt.Fprintln(a.stdout, "skipped the peer MCP registration: the installation's binary is unknown")
	} else if system && account == "" {
		_, _ = fmt.Fprintln(a.stdout, "skipped the peer MCP registration; run claude mcp remove -s user peer in each account that registered it")
	} else {
		outcome, err := uninstallMCPFn(account, binary)
		if err != nil {
			return a.uninstallFail("remove the MCP registration", err)
		}
		_, _ = fmt.Fprintln(a.stdout, outcome)
	}

	removedDirs, err := uninstallRemoveRuntimeDirFn(opts)
	if err != nil {
		return a.uninstallFail("remove the runtime directory", err)
	}
	if len(removedDirs) == 0 {
		_, _ = fmt.Fprintln(a.stdout, "the runtime directory was already gone")
	}
	for _, path := range removedDirs {
		_, _ = fmt.Fprintf(a.stdout, "removed %s\n", path)
	}

	if purgeData {
		removed, purgeErr := uninstallPurgeDataFn(opts)
		for _, entry := range removed {
			if strings.HasPrefix(entry, "kept ") {
				_, _ = fmt.Fprintln(a.stdout, entry)
				continue
			}
			_, _ = fmt.Fprintf(a.stdout, "removed %s\n", entry)
		}
		if purgeErr != nil {
			return a.uninstallFail("remove the data", purgeErr)
		}
		if len(removed) == 0 {
			_, _ = fmt.Fprintln(a.stdout, "no configuration, data or logs were present")
		}
	} else {
		_, _ = fmt.Fprintf(a.stdout, "kept configuration, data and logs: %s\n", strings.Join(keptPaths(opts), ", "))
	}

	if system {
		accounts, err := uninstallOtherAccountsFn(opts)
		if err != nil {
			return a.uninstallFail("list other accounts' Brokers", err)
		}
		if len(accounts) > 0 {
			_, _ = fmt.Fprintf(a.stdout, "other accounts run a user-level Broker: %s\n", strings.Join(accounts, ", "))
			if binary != "" {
				_, _ = fmt.Fprintf(a.stdout, "their Broker will fail to start after %s is removed\n", binary)
			} else {
				_, _ = fmt.Fprintln(a.stdout, "their Broker may fail to start if the shared binary is removed")
			}
		}
	}

	if binary != "" {
		if err := a.removeServiceBinary(opts, system, binary); err != nil {
			return a.uninstallFail("remove the binary", err)
		}
	}

	if system {
		_, _ = fmt.Fprintln(a.stdout, "each account that registered the peer MCP server must remove it with: claude mcp remove -s user peer")
	}
	return exitOK
}

// removeServiceBinary removes the binary the service started. A user-level
// uninstall leaves a binary that belongs to another account alone, such as a
// shared system-level binary. The path must also prove it is this
// installation's agent-peer binary.
func (a *app) removeServiceBinary(opts service.Options, system bool, binary string) error {
	path := rootedPath(opts, binary)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(a.stdout, "the binary %s was already gone\n", binary)
			return nil
		}
		return fmt.Errorf("inspect %s: %w", binary, err)
	}
	if reason := serviceBinaryIdentity(opts, binary); reason != "" {
		_, _ = fmt.Fprintf(a.stdout, "kept the binary %s: %s\n", binary, reason)
		return nil
	}
	if !system {
		uid, err := uninstallFileOwnerFn(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				_, _ = fmt.Fprintf(a.stdout, "the binary %s was already gone\n", binary)
				return nil
			}
			return fmt.Errorf("inspect %s: %w", binary, err)
		}
		if uid != uint32(opts.EUID) {
			_, _ = fmt.Fprintf(a.stdout, "kept the binary %s: it belongs to another account\n", binary)
			return nil
		}
	}
	if err := uninstallRemoveFileFn(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, _ = fmt.Fprintf(a.stdout, "the binary %s was already gone\n", binary)
			return nil
		}
		return fmt.Errorf("remove %s: %w", binary, err)
	}
	_, _ = fmt.Fprintf(a.stdout, "removed the binary %s\n", binary)
	return nil
}

// serviceBinaryIdentity returns why binary is not this installation's
// agent-peer binary, or an empty string when it is. A file only qualifies by
// name and by being the running executable; nothing else is deleted with root's
// permission, and an unrelated file is never executed to find out.
func serviceBinaryIdentity(opts service.Options, binary string) string {
	manual := "remove it manually with: rm " + binary
	if !filepath.IsAbs(binary) {
		return "it is not an absolute path; " + manual
	}
	if filepath.Base(binary) != "agent-peer" {
		return "its name is not agent-peer; " + manual
	}
	path := rootedPath(opts, binary)
	info, err := os.Stat(path)
	if err != nil {
		return "it could not be inspected: " + err.Error() + "; " + manual
	}
	if exe, err := uninstallExecutableFn(); err == nil {
		if exeInfo, err := os.Stat(exe); err == nil && os.SameFile(info, exeInfo) {
			return ""
		}
	}
	return "it is not this agent-peer binary; " + manual
}

// keptPaths lists the paths a keep-data uninstall left in place.
func keptPaths(opts service.Options) []string {
	var paths []string
	if opts.System {
		paths = append(paths, "/etc/agent-peer", "/var/lib/agent-peer")
		if opts.GOOS == "darwin" {
			paths = append(paths, "/var/log/agent-peer")
		} else {
			paths = append(paths, "/var/log/agent-peer/audit.log")
		}
		return paths
	}
	paths = append(paths, filepath.Join(opts.Home, ".agent-peer"))
	if opts.GOOS == "darwin" {
		paths = append(paths, filepath.Join(opts.Home, "Library", "Logs", "agent-peer"))
	}
	return paths
}

// rootedPath maps a production path under the test root prefix.
func rootedPath(opts service.Options, path string) string {
	if opts.Root == "" {
		return path
	}
	return filepath.Join(opts.Root, path)
}

// resolvePath returns path with symlinks resolved, or path itself when it
// cannot be resolved.
func resolvePath(path string) string {
	if resolved, err := uninstallEvalSymlinksFn(path); err == nil {
		return resolved
	}
	return path
}

// askOnTTY writes prompt to the controlling terminal and reads one answer.
// ok is false when the terminal cannot be opened or read.
func askOnTTY(prompt string) (yes, ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, false
	}
	defer func() { _ = tty.Close() }()

	if _, err := io.WriteString(tty, prompt); err != nil {
		return false, false
	}
	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && line == "" {
		return false, false
	}
	return yesAnswer(line), true
}

// yesAnswer reports whether answer is y or yes, ignoring case and surrounding
// space. Every other answer is no.
func yesAnswer(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// runSudo re-runs this command as root and returns the child's exit code.
func runSudo(ctx context.Context, exe string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "sudo", append([]string{exe}, args...)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err == nil {
		return exitOK, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return exitFailure, err
}

// fileOwner returns the owner of path without following a symbolic link.
func fileOwner(path string) (uint32, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("no owner information for %s", path)
	}
	return st.Uid, nil
}

// uninstallUsageError reports a usage error found before the operation starts.
func (a *app) uninstallUsageError(reason string) int {
	_, _ = fmt.Fprint(a.stderr, uninstallUsage)
	_, _ = fmt.Fprintf(a.stderr, "peers uninstall: %s\n", reason)
	return exitUsage
}

// uninstallFail reports the step that failed. The step functions name the path
// and the cause, so the command adds only the step name.
func (a *app) uninstallFail(step string, err error) int {
	_, _ = fmt.Fprintf(a.stderr, "peers uninstall: %s: %v\n", step, err)
	return exitFailure
}

// flagSuffix returns the flags for a hint that repeats this invocation.
func flagSuffix(purge bool) string {
	if purge {
		return " --purge"
	}
	return ""
}
