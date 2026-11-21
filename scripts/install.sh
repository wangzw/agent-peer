#!/bin/sh
# Install peers from an extracted release archive.
#
# The archive keeps this script next to the peers binary. The script
# installs that binary, starts the Broker as a user-level or system-level
# service, and registers the MCP server for the account that runs it. It never
# downloads anything, so it works on a host without network access.
#
# Usage:
#   sh ./install.sh [--system | --user] [--prefix <dir>] [--no-service]
#                   [--no-mcp] [--admin-group <name>] [--join-admin-group]
#                   [--no-admin-group]
#
#   --system        install for the whole host. The script runs itself through
#                   sudo when it is not root. The binary goes to /usr/local/bin
#                   unless --prefix says otherwise, and the service runs at
#                   boot.
#   --user          install for the account that runs the script. The account
#                   must not be root.
#   --prefix <dir>  install the binary in <dir> instead of the default:
#                   $HOME/.local/bin for a user-level install and
#                   /usr/local/bin for a system-level install.
#   --no-service    do not install the Broker service.
#   --no-mcp        do not register the MCP server with Claude Code.
#   --admin-group <name>
#                   manage the system-level Broker through <name> instead of
#                   the default peers group; passed to
#                   "service install --system".
#   --join-admin-group
#                   add the account that ran sudo to the admin group without
#                   asking.
#   --no-admin-group
#                   do not ask to add the account that ran sudo to the admin
#                   group.
#   -h, --help      print this usage and exit.
#
# Without --system or --user the script chooses the level itself. Root installs
# at the system level without asking. An account without a controlling terminal
# gets a user-level install without waiting for input. An account with a
# controlling terminal is asked whether to install at the system level, and is
# asked whether to upgrade the existing system-level installation when the host
# already has one; answering no to the upgrade leaves the host unchanged.
#
# A system-level install asks, on the controlling terminal, whether to add the
# account that ran sudo to the admin group, so that account can manage the
# Broker without sudo after logging in again. The answer defaults to no. The
# prompt is skipped when the answer cannot be read, and when the account is
# already in the group.
#
# Uninstall with "peers uninstall". This script does not uninstall
# anything.

set -eu

system=0
system_set=0
user_set=0
prefix=
prefix_set=0
install_service=1
install_mcp=1
admin_group=peers
admin_group_set=0
join_admin_group=0
no_admin_group=0

# fail reports the step that failed and exits. Every step that can fail calls
# it explicitly, so the message names the part of the installation that stopped
# rather than leaving the caller with a bare shell exit status.
fail() {
	printf 'install.sh: %s\n' "$1" >&2
	exit 1
}

# ask_tty writes the prompt to the controlling terminal and reads one line from
# it, never from stdin: when the script is piped into sh, stdin is the script
# itself. It prints the answer without its newline and returns 0 when a line was
# read, so an empty answer is distinct from a terminal that could not be read.
ask_tty() {
	printf '%s' "$1" 2>/dev/null >/dev/tty || return 1
	_reply=
	if ! _reply=$( { read -r _line && printf '%s' "$_line"; } 2>/dev/null </dev/tty ); then
		printf '\n' 2>/dev/null >/dev/tty || true
		return 1
	fi
	printf '\n' 2>/dev/null >/dev/tty || true
	printf '%s' "$_reply"
	return 0
}

usage() {
	cat <<'EOF'
usage: sh ./install.sh [--system | --user] [--prefix <dir>] [--no-service]
                       [--no-mcp] [--admin-group <name>] [--join-admin-group]
                       [--no-admin-group]

Install peers from the extracted release archive.

  --system        install for the whole host; the script runs itself through
                  sudo when it is not root
  --user          install for the account that runs the script; the account
                  must not be root
  --prefix <dir>  install the binary in <dir> instead of $HOME/.local/bin
                  for a user-level install or /usr/local/bin for a
                  system-level install
  --no-service    do not install the Broker service
  --no-mcp        do not register the MCP server with Claude Code
  --admin-group <name>
                  manage the system-level Broker through <name> instead of
                  the default peers group; passed to
                  "service install --system"
  --join-admin-group
                  add the account that ran sudo to the admin group without
                  asking
  --no-admin-group
                  do not ask to add the account that ran sudo to the admin
                  group
  -h, --help      print this usage and exit

Uninstall with "peers uninstall". This script does not uninstall
anything.
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
archive_binary=$script_dir/peers

while [ "$#" -gt 0 ]; do
	case $1 in
		--system)
			system=1
			system_set=1
			;;
		--user)
			user_set=1
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
		--admin-group)
			if [ "$#" -lt 2 ]; then
				printf 'install.sh: --admin-group needs a group name\n' >&2
				usage >&2
				exit 2
			fi
			admin_group=$2
			admin_group_set=1
			shift
			;;
		--admin-group=*)
			admin_group=${1#--admin-group=}
			admin_group_set=1
			;;
		--join-admin-group)
			join_admin_group=1
			;;
		--no-admin-group)
			no_admin_group=1
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

if [ "$system_set" -eq 1 ] && [ "$user_set" -eq 1 ]; then
	printf 'install.sh: --system and --user cannot be used together\n' >&2
	usage >&2
	exit 2
fi

if [ "$prefix_set" -eq 1 ] && [ -z "$prefix" ]; then
	printf 'install.sh: --prefix needs a non-empty directory\n' >&2
	usage >&2
	exit 2
fi

if [ "$admin_group_set" -eq 1 ] && [ -z "$admin_group" ]; then
	printf 'install.sh: --admin-group needs a non-empty group name\n' >&2
	usage >&2
	exit 2
fi

# The service install applies the same rule. A leading hyphen is rejected here
# so the name cannot be mistaken for an option, and the character set matches
# what Linux and macOS accept.
if ! printf '%s\n' "$admin_group" | LC_ALL=C grep -Eq '^[a-z_][a-z0-9_-]*$'; then
	printf 'install.sh: invalid --admin-group name: %s\n' "$admin_group" >&2
	usage >&2
	exit 2
fi

if [ "$join_admin_group" -eq 1 ] && [ "$no_admin_group" -eq 1 ]; then
	printf 'install.sh: --join-admin-group and --no-admin-group cannot be used together\n' >&2
	usage >&2
	exit 2
fi

if ! _current_uid=$(id -u); then
	fail "determining the current user id"
fi

if ! _os=$(uname -s); then
	fail "determining the operating system"
fi

# Choose the install level. An explicit option wins. Root installs at the
# system level without asking. An account with a controlling terminal is asked
# whether to install at the system level, and whether to upgrade the
# system-level installation when the host already has one. An account without a
# terminal gets a user-level install without waiting for input.
if [ "$system_set" -eq 1 ]; then
	system=1
elif [ "$user_set" -eq 1 ]; then
	system=0
elif [ "$_current_uid" -eq 0 ]; then
	system=1
elif true 2>/dev/null </dev/tty; then
	case $_os in
		Darwin) _definition=/Library/LaunchDaemons/io.github.wangzw.peers.plist ;;
		*) _definition=/etc/systemd/system/peers.service ;;
	esac
	_existing_system=0
	# stat takes a bare path on both platforms and succeeds only when it exists.
	if stat "$_definition" >/dev/null 2>&1; then
		_existing_system=1
	fi
	if [ "$_existing_system" -eq 1 ]; then
		_prompt='Upgrade the system-level installation? [Y/n] '
		_default=1
	else
		_prompt='Install for every account on this host? [y/N] '
		_default=0
	fi
	_choice=0
	if _answer=$(ask_tty "$_prompt"); then
		case $_answer in
			[yY]|[yY][eE][sS]) _choice=1 ;;
			'') _choice=$_default ;;
		esac
	fi
	if [ "$_choice" -eq 1 ]; then
		system=1
	else
		system=0
		if [ "$_existing_system" -eq 1 ]; then
			printf 'install.sh: the system-level installation was left unchanged; rerun with --user to install for this account instead.\n'
			exit 0
		fi
	fi
else
	system=0
fi

# A root account that asked for a user-level install is told to drop --user
# rather than silently installing at the system level instead.
if [ "$system" -eq 0 ] && [ "$_current_uid" -eq 0 ]; then
	fail "a user-level install must not run as root; drop --user to install at the system level"
fi

# The admin group belongs to a system-level install. Reject the options on a
# user-level install rather than silently doing nothing. The level is known
# only now: an account without --system may have chosen it at the prompt.
if [ "$system" -eq 0 ]; then
	if [ "$admin_group_set" -eq 1 ]; then
		printf 'install.sh: --admin-group is only valid with --system\n' >&2
		usage >&2
		exit 2
	fi
	if [ "$join_admin_group" -eq 1 ] || [ "$no_admin_group" -eq 1 ]; then
		printf 'install.sh: the admin group options are only valid with --system\n' >&2
		usage >&2
		exit 2
	fi
fi

if [ ! -f "$archive_binary" ]; then
	fail "no peers binary next to $0; extract the whole release archive and run sh ./install.sh from it"
fi

# A system-level install needs root. Recheck the same script under sudo with the
# options the caller gave, so a system-level choice made at the prompt and an
# explicit --system take the same path.
if [ "$system" -eq 1 ] && [ "$_current_uid" -ne 0 ]; then
	set -- --system
	if [ "$prefix_set" -eq 1 ]; then
		set -- "$@" --prefix "$prefix"
	fi
	if [ "$install_service" -eq 0 ]; then
		set -- "$@" --no-service
	fi
	if [ "$install_mcp" -eq 0 ]; then
		set -- "$@" --no-mcp
	fi
	if [ "$admin_group_set" -eq 1 ]; then
		set -- "$@" --admin-group "$admin_group"
	fi
	if [ "$join_admin_group" -eq 1 ]; then
		set -- "$@" --join-admin-group
	fi
	if [ "$no_admin_group" -eq 1 ]; then
		set -- "$@" --no-admin-group
	fi
	if ! command -v sudo >/dev/null 2>&1; then
		fail "a system-level install needs sudo, which was not found; install sudo, or pass --user for a user-level install"
	fi
	_sudo_status=0
	sudo sh "$script_dir/install.sh" "$@" || _sudo_status=$?
	case $_sudo_status in
		0|3) exit "$_sudo_status" ;;
	esac
	printf 'install.sh: the system-level installation did not complete: sudo exited with status %d.\n' "$_sudo_status" >&2
	printf 'See the error above; rerun with --user for a user-level install.\n' >&2
	exit "$_sudo_status"
fi

if [ "$system" -eq 1 ]; then
	if [ -z "$prefix" ]; then
		prefix=/usr/local/bin
	fi
	case $prefix in
		/*) ;;
		*) fail "a system-level --prefix must be an absolute path" ;;
	esac
else
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
if [ "$_os" = "Darwin" ]; then
	xattr -d com.apple.quarantine "$archive_binary" 2>/dev/null || true
fi

# Record the version of a binary already at the target path, so the summary can
# report an upgrade. Only the peers binary answers with the expected
# prefix; anything else is replaced without claiming an upgrade.
_previous_version=
if [ -f "$prefix/peers" ]; then
	if _previous_output=$("$prefix/peers" --version 2>/dev/null); then
		case $_previous_output in
			"peers "*) _previous_version=${_previous_output#peers } ;;
		esac
	fi
fi

# 1. Install the binary.
if [ "$system" -eq 1 ]; then
	if ! install -o root -g 0 -m 0755 "$archive_binary" "$prefix/peers"; then
		fail "installing the binary to $prefix"
	fi
else
	if ! install -m 0755 "$archive_binary" "$prefix/peers"; then
		fail "installing the binary to $prefix"
	fi
fi

# 2. Check that the installed binary actually runs and identifies itself. A
# failure here usually means the archive was built for another platform.
if ! _version_output=$("$prefix/peers" --version 2>&1); then
	fail "running $prefix/peers --version; the binary may be built for another platform"
fi
case $_version_output in
	"peers "*) ;;
	*) fail "unexpected output from $prefix/peers --version: $_version_output" ;;
esac

# Report the upgrade before the service prints its summary; the service does
# not know what was installed before.
if [ -n "$_previous_version" ]; then
	printf 'upgraded from %s\n' "$_previous_version"
fi

# 3. Install the service. service install is repeatable: an existing service is
# restarted with the new binary, so the script does not check first. Exit status
# 3 means the service is installed and running, but the Broker runs degraded;
# service install has already printed the summary and the reason, so the script
# finishes the remaining steps and exits with 3 at the end.
_service_status=0
_service_degraded=0
_service_level="user-level"
if [ "$install_service" -eq 1 ]; then
	if [ "$system" -eq 1 ]; then
		_service_level="system-level"
		if [ "$admin_group_set" -eq 1 ]; then
			"$prefix/peers" service install --system --admin-group "$admin_group" || _service_status=$?
		else
			"$prefix/peers" service install --system || _service_status=$?
		fi
	else
		"$prefix/peers" service install || _service_status=$?
	fi
	case $_service_status in
		0) ;;
		3) _service_degraded=1 ;;
		*) fail "installing the $_service_level service" ;;
	esac
fi

# 4. Install the shell completions, and at the system level the man pages. The
# completion scripts come from the installed binary, so they always match it.
# This runs after the service so that a completion or man page that cannot be
# written does not fail an otherwise complete installation: each failure is a
# warning and the script keeps its exit status. A user-level install skips the
# zsh completion, because a user-level file only becomes active after the
# account adds it to fpath; the caller is told how. The fish completion is
# installed only when fish itself is installed. New files and directories are
# group- and world-readable regardless of root's umask. DESTDIR prefixes the
# system-level share directory, for staged installs and tests; production
# leaves it unset.
umask 022
_share=${DESTDIR:-}/usr/local/share
_completion_bash_dir=
_completion_fish_dir=
if [ "$system" -eq 1 ]; then
	_completion_bash_dir=$_share/bash-completion/completions
	_completion_zsh_dir=$_share/zsh/site-functions
	_completion_fish_dir=$_share/fish/vendor_completions.d
	_man_dir=$_share/man/man1
elif [ -n "${HOME:-}" ]; then
	_completion_bash_dir=$HOME/.local/share/bash-completion/completions
	_completion_fish_dir=$HOME/.config/fish/completions
else
	printf 'install.sh: warning: HOME is not set; the shell completions were not installed\n' >&2
fi

# install_completion writes one completion script. It returns non-zero when the
# directory or the file cannot be written, and the caller turns that into a
# warning.
install_completion() {
	_shell=$1
	_dir=$2
	_name=$3
	if ! mkdir -p "$_dir"; then
		return 1
	fi
	if ! "$prefix/peers" completion "$_shell" > "$_dir/$_name"; then
		# Do not leave a partial file where the completion belongs.
		rm -f "$_dir/$_name"
		return 1
	fi
	return 0
}

if [ -n "$_completion_bash_dir" ]; then
	if ! install_completion bash "$_completion_bash_dir" peers; then
		printf 'install.sh: warning: could not write the bash completion to %s\n' \
			"$_completion_bash_dir/peers" >&2
	fi
fi
if [ -n "$_completion_fish_dir" ] && command -v fish >/dev/null 2>&1; then
	if ! install_completion fish "$_completion_fish_dir" peers.fish; then
		printf 'install.sh: warning: could not write the fish completion to %s\n' \
			"$_completion_fish_dir/peers.fish" >&2
	fi
fi

if [ "$system" -eq 1 ]; then
	if ! install_completion zsh "$_completion_zsh_dir" _peers; then
		printf 'install.sh: warning: could not write the zsh completion to %s\n' \
			"$_completion_zsh_dir/_peers" >&2
	fi
	if ! mkdir -p "$_man_dir"; then
		printf 'install.sh: warning: could not create the man directory %s\n' "$_man_dir" >&2
	else
		for _page in "$script_dir"/man/*.1; do
			[ -e "$_page" ] || continue
			if ! install -o root -g 0 -m 0644 "$_page" "$_man_dir/"; then
				printf 'install.sh: warning: could not install the man page %s\n' \
					"${_page##*/}" >&2
			fi
		done
	fi
else
	printf 'install.sh: zsh completion was not installed; enable it with: %s completion zsh > ~/.zfunc/_peers (create ~/.zfunc and add it to fpath).\n' "$prefix/peers"
fi

# 5. The system-level service creates the admin group. Put the account that ran
# sudo into it, so that account can manage the Broker without sudo after
# logging in again. The answer comes from the controlling terminal, never from
# stdin: when the script is piped into sh, stdin is the script itself. A
# missing terminal, an empty answer and any answer other than y or yes all
# leave the group alone and print the command that would join it. The account
# is added at most once.
_admin_group_user=
if [ "$system" -eq 1 ] && [ "$install_service" -eq 1 ] && [ "$no_admin_group" -eq 0 ]; then
	_sudo_user=${SUDO_USER:-}
	if [ -n "$_sudo_user" ] && [ "$_sudo_user" != "root" ]; then
		if ! _user_groups=$(id -Gn "$_sudo_user" 2>/dev/null); then
			fail "listing the groups of $_sudo_user"
		fi
		case " $_user_groups " in
			*" $admin_group "*) _in_admin_group=1 ;;
			*) _in_admin_group=0 ;;
		esac
		if [ "$_in_admin_group" -eq 0 ]; then
			_join=0
			if [ "$join_admin_group" -eq 1 ]; then
				_join=1
			# true, not the special builtin ":", so a missing terminal fails
			# this test instead of exiting the whole shell.
			elif true 2>/dev/null </dev/tty; then
				if printf 'Add %s to the %s group so %s can manage the Broker without sudo? [y/N] ' \
					"$_sudo_user" "$admin_group" "$_sudo_user" 2>/dev/null >/dev/tty; then
					if _answer=$( { read -r _reply && printf '%s' "$_reply"; } 2>/dev/null </dev/tty ); then
						case $_answer in
							[yY]|[yY][eE][sS]) _join=1 ;;
						esac
					fi
					printf '\n' 2>/dev/null >/dev/tty || true
				fi
			fi
			if [ "$_join" -eq 1 ]; then
				if [ "$_os" = "Darwin" ]; then
					if ! dseditgroup -o edit -a "$_sudo_user" -t user "$admin_group"; then
						fail "adding $_sudo_user to the $admin_group group"
					fi
				else
					if ! usermod -aG "$admin_group" "$_sudo_user"; then
						fail "adding $_sudo_user to the $admin_group group"
					fi
				fi
				_admin_group_user=$_sudo_user
			elif [ "$_os" = "Darwin" ]; then
				printf 'install.sh: to add %s to the %s group, run: sudo dseditgroup -o edit -a %s -t user %s\n' \
					"$_sudo_user" "$admin_group" "$_sudo_user" "$admin_group"
			else
				printf 'install.sh: to add %s to the %s group, run: sudo usermod -aG %s %s\n' \
					"$_sudo_user" "$admin_group" "$admin_group" "$_sudo_user"
			fi
		fi
	fi
fi

# 6. Register the MCP server with Claude Code. A system-level install registers
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
			printf '  claude mcp add --scope user peer -- %s mcp\n' "$prefix/peers"
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
			printf '  claude mcp add --scope user peer -- %s mcp\n' "$prefix/peers"
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
				if [ -n "$_existing" ] && [ "$_existing" != "$prefix/peers" ]; then
					_mcp_state="already registered with a different command; left unchanged"
					printf 'install.sh: the peer MCP server is already registered with %s; leaving it unchanged.\n' "$_existing"
					printf 'install.sh: to point it at this install, run: claude mcp remove -s user peer, then rerun this script.\n'
				else
					_mcp_state="already registered"
					printf 'install.sh: the peer MCP server is already registered; leaving it unchanged.\n'
				fi
			else
				if [ "$system" -eq 1 ]; then
					if ! sudo -u "$_target_user" -H "$_claude_path" mcp add --scope user peer -- "$prefix/peers" mcp; then
						fail "registering the MCP server for $_target_user"
					fi
				else
					if ! "$_claude_path" mcp add --scope user peer -- "$prefix/peers" mcp; then
						fail "registering the MCP server"
					fi
				fi
				_mcp_state="registered"
			fi
		fi
	fi
fi

# 7. A user-level prefix is often outside PATH. Say so instead of leaving the
# caller with "peers: command not found" in the next terminal.
if [ "$system" -eq 0 ]; then
	case ":${PATH:-}:" in
		*":$prefix:"*) ;;
		*) printf 'install.sh: %s is not on PATH; add it to PATH to run peers by name.\n' "$prefix" ;;
	esac
fi

# 8. Summarize what the script alone knows. service install printed the binary,
# version, configuration, logs and service commands. Without a service, the
# script reports the binary it placed.
if [ "$install_service" -eq 0 ]; then
	printf '\npeers %s installed (no service)\n' "${_version_output#peers }"
	printf '  %-12s%s\n' 'binary:' "$prefix/peers"
fi
printf '  %-12s%s\n' 'MCP:' "$_mcp_state"
if [ -n "$_admin_group_user" ]; then
	printf "\n%s was added to %s; log in again or run \`newgrp %s\` before managing the Broker without sudo.\n" \
		"$_admin_group_user" "$admin_group" "$admin_group"
fi
printf '\nReopen any Claude Code session that is already open to load the peer MCP server.\n'

# A degraded service is installed and running, so the installation succeeded;
# the exit status tells the caller that the Broker needs attention.
if [ "$_service_degraded" -eq 1 ]; then
	exit 3
fi
