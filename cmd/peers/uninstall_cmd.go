package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/wangzw/peers/internal/broker/service"
)

// uninstallDataPrompt asks whether to delete the configuration, identity and
// logs. --purge skips it and deletes them; --yes skips it and keeps them.
const uninstallDataPrompt = "Also remove configuration, identity, audit and service logs? [y/N] "

// The test seams replace the installation steps so the command can be checked
// without touching the system.
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
	uninstallGetenvFn             = os.Getenv
	uninstallFileOwnerFn          = fileOwner
	uninstallRemoveFileFn         = os.Remove
	uninstallSystemInstalledCheck = func(opts service.Options) bool {
		installed, err := service.Installed(opts)
		return err == nil && installed
	}
)

// newUninstallCommand builds "peers uninstall [--purge] [--yes]".
func newUninstallCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "uninstall",
		Short:   "Remove the peers installation",
		GroupID: groupInstall,
		Args:    noArgs,
	}
	purge := cmd.Flags().Bool("purge", false, "also remove configuration, identity, audit and service logs")
	yes := addYesFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return a.uninstallCmd(cmd.Context(), *purge, *yes)
	}
	return cmd
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
		return "/usr/local/bin/peers"
	}
	return filepath.Join(opts.Home, ".local", "bin", "peers")
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

// uninstallCmd implements "peers uninstall [--purge] [--yes]". It removes the
// installation of the account that runs it, or the system-level installation
// when run as root, and optionally the configuration, identity and logs.
func (a *app) uninstallCmd(ctx context.Context, purge, yes bool) error {
	userOpts, err := uninstallOptionsFn(false)
	if err != nil {
		return uninstallError("determine the options", err)
	}
	if !filepath.IsAbs(userOpts.Home) {
		return uninstallError("determine the options",
			fmt.Errorf("the home directory %q is not an absolute path", userOpts.Home))
	}
	systemOpts, err := uninstallOptionsFn(true)
	if err != nil {
		return uninstallError("determine the options", err)
	}

	if a.deps.euid() == 0 {
		return a.uninstallAsRoot(ctx, systemOpts, purge, yes)
	}
	return a.uninstallAsAccount(ctx, userOpts, systemOpts, purge, yes)
}

// uninstallAsRoot removes the system-level installation. Root has no
// user-level installation of its own.
func (a *app) uninstallAsRoot(ctx context.Context, opts service.Options, purge, yes bool) error {
	noServiceBinary := ""
	installed, err := uninstallInstalledFn(opts)
	if err != nil {
		return uninstallError("inspect the installation", err)
	}
	if !installed {
		// An installation that used --no-service left only the MCP
		// registration. Root's own registration and sudo's caller are both
		// checked, since either may be the account the install belonged to.
		exe, err := uninstallExecutableFn()
		if err != nil {
			return uninstallError("locate the executable", err)
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
		return nil
	}
	return a.uninstallLevel(ctx, opts, true, purge, yes, noServiceBinary)
}

// uninstallAsAccount removes this account's user-level installation. When only
// a system-level installation exists, it reports the command that removes it
// instead of running sudo, so the caller can read it and rerun it verbatim.
func (a *app) uninstallAsAccount(ctx context.Context, userOpts, systemOpts service.Options, purge, yes bool) error {
	noServiceBinary := ""
	installed, err := uninstallInstalledFn(userOpts)
	if err != nil {
		return uninstallError("inspect the installation", err)
	}
	if !installed {
		exe, err := uninstallExecutableFn()
		if err != nil {
			return uninstallError("locate the executable", err)
		}
		if binary, ok := installationBinary(userOpts, "", exe); ok {
			noServiceBinary = binary
			installed = true
		}
	}
	if installed {
		if err := a.uninstallLevel(ctx, userOpts, false, purge, yes, noServiceBinary); err != nil {
			return err
		}
		if uninstallSystemInstalledCheck(systemOpts) {
			_, _ = fmt.Fprintf(a.stdout, "A system-level installation is still present; run: %s\n", sudoUninstallCommand(purge))
		}
		return nil
	}

	systemInstalled, err := uninstallInstalledFn(systemOpts)
	if err != nil {
		return uninstallError("inspect the installation", err)
	}
	if !systemInstalled {
		_, _ = fmt.Fprintln(a.stdout, "peers is not installed")
		return nil
	}

	return fmt.Errorf("only a system-level installation is present; run: %s", sudoUninstallCommand(purge))
}

// sudoUninstallCommand is the complete command that removes the system-level
// installation without asking. It is quoted into the error so the caller can
// rerun it as it is.
func sudoUninstallCommand(purge bool) string {
	command := "sudo peers uninstall"
	if purge {
		command += " --purge"
	}
	return command + " --yes"
}

// uninstallLevel performs the steps for one level. binary is set only when the
// level has no service definition and the executable itself is the
// installation.
func (a *app) uninstallLevel(ctx context.Context, opts service.Options, system, purge, yes bool, binary string) error {
	account := ""
	if system {
		account = uninstallGetenvFn("SUDO_USER")
		if account == "root" {
			account = ""
		}
	}

	// Confirmation comes after the level is known but before anything is
	// read or removed. --purge does not ask, and --yes skips the question
	// without deleting the data. Without a terminal, confirm reports
	// errNeedsYes and the command exits with failure before touching
	// anything.
	purgeData := purge
	if !purgeData && !yes {
		confirmed, err := a.confirm(uninstallDataPrompt, false)
		if err != nil {
			if errors.Is(err, errNeedsYes) {
				return err
			}
			return uninstallError("confirm the data removal", err)
		}
		purgeData = confirmed
	}

	if binary == "" {
		path, ok, err := uninstallServiceBinaryFn(opts)
		if err != nil {
			return uninstallError("read the service definition", err)
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
				return uninstallError("locate the executable", err)
			}
			if candidate, ok := installationBinary(opts, account, exe); ok {
				binary = candidate
			} else {
				_, _ = fmt.Fprintf(a.stdout, "kept the running executable %s: it is not the installation binary at %s\n",
					exe, defaultInstallPath(opts))
			}
		}
	}

	result, err := uninstallServiceFn(opts)
	if err != nil {
		return uninstallError("stop and remove the service", err)
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
			return uninstallError("remove the MCP registration", err)
		}
		_, _ = fmt.Fprintln(a.stdout, outcome)
	}

	if err := a.removeInstallationFiles(opts, system); err != nil {
		return uninstallError("remove the shell completions and man pages", err)
	}

	removedDirs, err := uninstallRemoveRuntimeDirFn(opts)
	if err != nil {
		return uninstallError("remove the runtime directory", err)
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
			return uninstallError("remove the data", purgeErr)
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
			return uninstallError("list other accounts' Brokers", err)
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
			return uninstallError("remove the binary", err)
		}
	}

	if system {
		_, _ = fmt.Fprintln(a.stdout, "each account that registered the peer MCP server must remove it with: claude mcp remove -s user peer")
	}
	return nil
}

// removeInstallationFiles deletes the shell completion files the install
// script created for the level, and the system-level man pages. Files that are
// already gone are not an error; every removed path is reported.
func (a *app) removeInstallationFiles(opts service.Options, system bool) error {
	paths := completionFiles(opts, system)
	if system {
		pattern := filepath.Join(rootedPath(opts, "/usr/local/share/man/man1"), "peers*.1")
		pages, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("list %s: %w", pattern, err)
		}
		paths = append(paths, pages...)
	}
	for _, path := range paths {
		err := os.Remove(path)
		switch {
		case err == nil:
			_, _ = fmt.Fprintf(a.stdout, "removed %s\n", path)
		case errors.Is(err, os.ErrNotExist):
		default:
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

// completionFiles lists the shell completion files the install script writes
// for one level. The user level never writes the zsh completion, so neither
// uninstall case touches it.
func completionFiles(opts service.Options, system bool) []string {
	if system {
		return []string{
			rootedPath(opts, "/usr/local/share/bash-completion/completions/peers"),
			rootedPath(opts, "/usr/local/share/zsh/site-functions/_peers"),
			rootedPath(opts, "/usr/local/share/fish/vendor_completions.d/peers.fish"),
		}
	}
	return []string{
		rootedPath(opts, filepath.Join(opts.Home, ".local", "share", "bash-completion", "completions", "peers")),
		rootedPath(opts, filepath.Join(opts.Home, ".config", "fish", "completions", "peers.fish")),
	}
}

// removeServiceBinary removes the binary the service started. A user-level
// uninstall leaves a binary that belongs to another account alone, such as a
// shared system-level binary. The path must also prove it is this
// installation's peers binary.
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
// peers binary, or an empty string when it is. A file only qualifies by
// name and by being the running executable; nothing else is deleted with root's
// permission, and an unrelated file is never executed to find out.
func serviceBinaryIdentity(opts service.Options, binary string) string {
	manual := "remove it manually with: rm " + binary
	if !filepath.IsAbs(binary) {
		return "it is not an absolute path; " + manual
	}
	if filepath.Base(binary) != "peers" {
		return "its name is not peers; " + manual
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
	return "it is not this peers binary; " + manual
}

// keptPaths lists the paths a keep-data uninstall left in place.
func keptPaths(opts service.Options) []string {
	var paths []string
	if opts.System {
		paths = append(paths, "/etc/peers", "/var/lib/peers")
		if opts.GOOS == "darwin" {
			paths = append(paths, "/var/log/peers")
		} else {
			paths = append(paths, "/var/log/peers/audit.log")
		}
		return paths
	}
	paths = append(paths, filepath.Join(opts.Home, ".peers"))
	if opts.GOOS == "darwin" {
		paths = append(paths, filepath.Join(opts.Home, "Library", "Logs", "peers"))
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

// uninstallError names the step that failed. The step functions name the path
// and the cause, so the command adds only the step name; the framework prints
// the command path and exit code.
func uninstallError(step string, err error) error {
	return fmt.Errorf("%s: %w", step, err)
}
