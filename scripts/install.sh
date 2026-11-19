#!/bin/sh
# Install agent-peer from an extracted release archive.
#
# The archive keeps this script next to the agent-peer binary. The script
# installs that binary, starts the Broker as a user-level or system-level
# service, and registers the MCP server for the account that runs it. It never
# downloads anything, so it works on a host without network access.
#
# Usage:
#   sh ./install.sh [--system] [--prefix <dir>] [--no-service] [--no-mcp]
#
#   --system        install for the whole host. Must run as root, usually
#                   through sudo. The binary goes to /usr/local/bin unless
#                   --prefix says otherwise, and the service runs at boot.
#   --prefix <dir>  install the binary in <dir> instead of the default:
#                   $HOME/.local/bin for a user-level install and
#                   /usr/local/bin for a system-level install.
#   --no-service    do not install the Broker service.
#   --no-mcp        do not register the MCP server with Claude Code.
#   -h, --help      print this usage and exit.
#
# Uninstall with "agent-peer service uninstall [--system]", then
# "claude mcp remove -s user peer", then remove the installed binary. This
# script does not uninstall anything.

set -eu

system=0
prefix=
prefix_set=0
install_service=1
install_mcp=1

# fail reports the step that failed and exits. Every step that can fail calls
# it explicitly, so the message names the part of the installation that stopped
# rather than leaving the caller with a bare shell exit status.
fail() {
	printf 'install.sh: %s\n' "$1" >&2
	exit 1
}

usage() {
	cat <<'EOF'
usage: sh ./install.sh [--system] [--prefix <dir>] [--no-service] [--no-mcp]

Install agent-peer from the extracted release archive.

  --system        install for the whole host; must run as root, usually
                  through sudo
  --prefix <dir>  install the binary in <dir> instead of $HOME/.local/bin
                  for a user-level install or /usr/local/bin for a
                  system-level install
  --no-service    do not install the Broker service
  --no-mcp        do not register the MCP server with Claude Code
  -h, --help      print this usage and exit
EOF
}

# The archive root is the directory that holds this script. When $0 carries no
# slash, the caller reached the script through the current directory, as in
# "sh install.sh", so resolve it there rather than searching PATH for another
# file of the same name.
case $0 in
	*/*) _script_path=$0 ;;
	*) _script_path=./$0 ;;
esac
case $_script_path in
	*/*)
		if ! script_dir=$(CDPATH='' cd -P "${_script_path%/*}" && pwd); then
			fail "cannot resolve the directory that contains this script"
		fi
		;;
	*)
		if ! script_dir=$(CDPATH='' cd -P . && pwd); then
			fail "cannot resolve the current directory"
		fi
		;;
esac
archive_binary=$script_dir/agent-peer

while [ "$#" -gt 0 ]; do
	case $1 in
		--system)
			system=1
			;;
		--prefix)
			if [ "$#" -lt 2 ]; then
				printf 'install.sh: --prefix needs a directory\n' >&2
				usage >&2
				exit 2
			fi
			prefix=$2
			prefix_set=1
			shift
			;;
		--prefix=*)
			prefix=${1#--prefix=}
			prefix_set=1
			;;
		--no-service)
			install_service=0
			;;
		--no-mcp)
			install_mcp=0
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf 'install.sh: unknown argument: %s\n' "$1" >&2
			usage >&2
			exit 2
			;;
	esac
	shift
done

if [ "$prefix_set" -eq 1 ] && [ -z "$prefix" ]; then
	printf 'install.sh: --prefix needs a non-empty directory\n' >&2
	usage >&2
	exit 2
fi

if [ ! -f "$archive_binary" ]; then
	fail "no agent-peer binary next to $0; extract the whole release archive and run sh ./install.sh from it"
fi

if [ "$system" -eq 1 ]; then
	if ! _current_uid=$(id -u); then
		fail "determining the current user id"
	fi
	if [ "$_current_uid" -ne 0 ]; then
		fail "a system-level install must run as root; run: sudo sh ./install.sh --system"
	fi
	if [ -z "$prefix" ]; then
		prefix=/usr/local/bin
	fi
	case $prefix in
		/*) ;;
		*) fail "a system-level --prefix must be an absolute path" ;;
	esac
else
	if ! _current_uid=$(id -u); then
		fail "determining the current user id"
	fi
	if [ "$_current_uid" -eq 0 ]; then
		fail "a user-level install must not run as root; run sh ./install.sh as your own account, or sudo sh ./install.sh --system for a system-level install"
	fi
	if [ -z "$prefix" ]; then
		if [ -z "${HOME:-}" ]; then
			fail "HOME is not set; pass --prefix with the directory to install into"
		fi
		prefix=$HOME/.local/bin
	fi
	if ! mkdir -p "$prefix"; then
		fail "creating the install directory $prefix"
	fi
fi

# A system-level Broker runs as root, so the binary and every directory above
# it must be owned by root and not writable by group or others. Otherwise an
# ordinary account could replace the binary and take over the service.
if [ "$system" -eq 1 ]; then
	if [ ! -d "$prefix" ]; then
		fail "the system-level install directory $prefix does not exist; create it first (for example with 'sudo mkdir -p $prefix') or pass --prefix"
	fi
	if ! prefix=$(CDPATH='' cd -P "$prefix" && pwd); then
		fail "resolving the install directory $prefix"
	fi

	# check_system_dir fails unless one directory is root-owned and cannot be
	# written by group or others. stat takes different flags on Linux and
	# macOS, so try the GNU form first and fall back to the BSD form.
	check_system_dir() {
		_dir=$1
		if ! _uid=$(stat -L -c '%u' "$_dir" 2>/dev/null); then
			if ! _uid=$(stat -L -f '%u' "$_dir" 2>/dev/null); then
				fail "cannot read the owner of $_dir; pass --prefix with a directory whose owner can be checked"
			fi
		fi
		if ! _mode=$(stat -L -c '%a' "$_dir" 2>/dev/null); then
			if ! _mode=$(stat -L -f '%Lp' "$_dir" 2>/dev/null); then
				fail "cannot read the mode of $_dir; pass --prefix with a directory whose mode can be checked"
			fi
		fi
		if [ "$_uid" != "0" ]; then
			fail "system-level install: $_dir is not owned by root; pass --prefix with a root-owned directory such as /usr/local/bin"
		fi
		case $_mode in
			[0-7][0-7][0-7]|[0-7][0-7][0-7][0-7]) ;;
			*) fail "system-level install: unexpected mode '$_mode' for $_dir" ;;
		esac
		# The last three octal digits are the permission bits. A leading zero
		# makes the shell read them as octal, so 022 selects group write and
		# other write.
		_last=${_mode#"${_mode%???}"}
		if [ $((0$_last & 022)) -ne 0 ]; then
			fail "system-level install: $_dir is writable by group or others; pass --prefix with a directory such as /usr/local/bin"
		fi
		# Every directory above the binary must also be searchable by others.
		# Otherwise the service, which may run with a reduced set of
		# supplementary groups, cannot traverse the path to the binary.
		if [ $((0$_last & 001)) -eq 0 ]; then
			fail "system-level install: $_dir is not searchable by others; pass --prefix with a directory that others can enter, such as /usr/local/bin"
		fi
	}

	_check=$prefix
	while :; do
		check_system_dir "$_check"
		[ "$_check" = "/" ] && break
		_check=$(dirname "$_check")
	done
fi

# The macOS release binaries are not signed or notarized. A browser download
# carries the quarantine attribute, and macOS blocks the binary until it is
# removed. The attribute is absent after a command-line download, and xattr
# then fails; that is not an error here.
if ! _os=$(uname -s); then
	fail "determining the operating system"
fi
if [ "$_os" = "Darwin" ]; then
	xattr -d com.apple.quarantine "$archive_binary" 2>/dev/null || true
fi

# 1. Install the binary.
if [ "$system" -eq 1 ]; then
	if ! install -o root -g 0 -m 0755 "$archive_binary" "$prefix/agent-peer"; then
		fail "installing the binary to $prefix"
	fi
else
	if ! install -m 0755 "$archive_binary" "$prefix/agent-peer"; then
		fail "installing the binary to $prefix"
	fi
fi

# 2. Check that the installed binary actually runs and identifies itself. A
# failure here usually means the archive was built for another platform.
if ! _version_output=$("$prefix/agent-peer" --version 2>&1); then
	fail "running $prefix/agent-peer --version; the binary may be built for another platform"
fi
case $_version_output in
	"agent-peer "*) ;;
	*) fail "unexpected output from $prefix/agent-peer --version: $_version_output" ;;
esac

# 3. Install the service. service install is repeatable: an existing service is
# restarted with the new binary, so the script does not check first.
if [ "$install_service" -eq 0 ]; then
	_service_state="skipped (--no-service)"
elif [ "$system" -eq 1 ]; then
	if ! "$prefix/agent-peer" service install --system; then
		fail "installing the system-level service"
	fi
	_service_state="installed (system-level, starts at boot)"
else
	if ! "$prefix/agent-peer" service install; then
		fail "installing the user-level service"
	fi
	_service_state="installed (user-level, starts at login)"
fi

# 4. Register the MCP server with Claude Code. A system-level install registers
# it for the account that ran sudo; running the script directly as root leaves
# no account to register for, so it prints the command instead.
_mcp_state="skipped (--no-mcp)"
if [ "$install_mcp" -eq 1 ]; then
	_target_user=
	_find_claude=0
	if [ "$system" -eq 1 ]; then
		_target_user=${SUDO_USER:-}
		if [ -z "$_target_user" ] || [ "$_target_user" = "root" ]; then
			_mcp_state="skipped (no account to register for)"
			printf 'install.sh: register the MCP server for your account manually:\n'
			printf '  claude mcp add --scope user peer -- %s mcp\n' "$prefix/agent-peer"
		else
			_find_claude=1
		fi
	else
		if ! _target_user=$(id -un); then
			fail "determining the current user"
		fi
		_find_claude=1
	fi

	if [ "$_find_claude" -eq 1 ]; then
		if [ "$system" -eq 1 ]; then
			# Resolve claude through the target account's own login shell. The
			# account may configure PATH in a zsh or bash profile that a plain
			# sh does not read. getent answers on Linux and dscl on macOS; when
			# neither knows the account, fall back to sh.
			_target_shell=
			if command -v getent >/dev/null 2>&1; then
				_target_shell=$(getent passwd "$_target_user" 2>/dev/null | cut -d: -f7)
			elif command -v dscl >/dev/null 2>&1; then
				_target_shell=$(dscl . -read "/Users/$_target_user" UserShell 2>/dev/null | sed -n 's/^UserShell: //p')
			fi
			[ -n "$_target_shell" ] || _target_shell=/bin/sh
			_claude_path=$(sudo -u "$_target_user" -H "$_target_shell" -lc 'command -v claude' 2>/dev/null) || _claude_path=
		else
			_claude_path=$(command -v claude 2>/dev/null) || _claude_path=
		fi

		if [ -z "$_claude_path" ]; then
			_mcp_state="skipped (claude not found)"
			printf 'install.sh: claude was not found; register the MCP server manually:\n'
			printf '  claude mcp add --scope user peer -- %s mcp\n' "$prefix/agent-peer"
		else
			if [ "$system" -eq 1 ]; then
				# The login shell resolved claude to an absolute path, so run
				# that path directly as the target account.
				_get_output=$(sudo -u "$_target_user" -H "$_claude_path" mcp get peer 2>&1) && _get_status=0 || _get_status=$?
			else
				_get_output=$("$_claude_path" mcp get peer 2>&1) && _get_status=0 || _get_status=$?
			fi

			if [ "$_get_status" -eq 0 ]; then
				_existing=$(printf '%s\n' "$_get_output" | sed -n 's/^[[:space:]]*Command:[[:space:]]*//p' | head -n 1)
				if [ -n "$_existing" ] && [ "$_existing" != "$prefix/agent-peer" ]; then
					_mcp_state="already registered with a different command; left unchanged"
					printf 'install.sh: the peer MCP server is already registered with %s; leaving it unchanged.\n' "$_existing"
					printf 'install.sh: to point it at this install, run: claude mcp remove -s user peer, then rerun this script.\n'
				else
					_mcp_state="already registered"
					printf 'install.sh: the peer MCP server is already registered; leaving it unchanged.\n'
				fi
			else
				if [ "$system" -eq 1 ]; then
					if ! sudo -u "$_target_user" -H "$_claude_path" mcp add --scope user peer -- "$prefix/agent-peer" mcp; then
						fail "registering the MCP server for $_target_user"
					fi
				else
					if ! "$_claude_path" mcp add --scope user peer -- "$prefix/agent-peer" mcp; then
						fail "registering the MCP server"
					fi
				fi
				_mcp_state="registered"
			fi
		fi
	fi
fi

# 5. A user-level prefix is often outside PATH. Say so instead of leaving the
# caller with "agent-peer: command not found" in the next terminal.
if [ "$system" -eq 0 ]; then
	case ":${PATH:-}:" in
		*":$prefix:"*) ;;
		*) printf 'install.sh: %s is not on PATH; add it to PATH to run agent-peer by name.\n' "$prefix" ;;
	esac
fi

# 6. Summarize. The status call contacts the Broker and is allowed to fail; a
# Broker that is still starting or a host without one must not fail the
# installation after the binary and service are in place.
printf '\nagent-peer installed\n'
printf '  binary:  %s\n' "$prefix/agent-peer"
printf '  version: %s\n' "$_version_output"
printf '  service: %s\n' "$_service_state"
printf '  MCP:     %s\n' "$_mcp_state"
if [ "$install_service" -eq 1 ]; then
	if _status_output=$("$prefix/agent-peer" status 2>&1); then
		printf '  status:  %s\n' "$_status_output"
	else
		printf '  status:  not available yet\n'
	fi
fi
printf '\nReopen any Claude Code session that is already open to load the peer MCP server.\n'
