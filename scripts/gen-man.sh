#!/usr/bin/env bash
# Generate the man pages for the peers command tree from the cobra root command
# into man/ at the repository root. Hidden commands get no page.
#
# With --check, generate into a temporary directory and fail when the committed
# pages differ, without touching the worktree. gen-check runs this mode, so a
# command or flag change that was not followed by 'make gen' fails the gate.

set -euo pipefail

script_dir="$(CDPATH='' cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(dirname "$script_dir")"
man_dir="$repo_root/man"

check=0
case "${1:-}" in
	"")
		;;
	--check)
		check=1
		;;
	*)
		printf '%s\n' "usage: $(basename "$0") [--check]" >&2
		exit 2
		;;
esac
if [ "$#" -gt 1 ]; then
	printf '%s\n' "usage: $(basename "$0") [--check]" >&2
	exit 2
fi

if [ "$check" -eq 1 ]; then
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	target="$tmp/man"
else
	target="$man_dir"
fi

if ! go -C "$repo_root" run -tags peers_genman ./cmd/peers gen-man "$target"; then
	printf '%s\n' "gen-man: generating the man pages failed" >&2
	exit 1
fi

if [ "$check" -eq 0 ]; then
	count="$(find "$target" -maxdepth 1 -type f -name '*.1' | wc -l)"
	printf '%s\n' "gen-man: wrote $count man pages to man/"
	exit 0
fi

if [ ! -d "$man_dir" ]; then
	printf '%s\n' "gen-man: man/ does not exist; run 'make gen'" >&2
	exit 1
fi
if ! diff -ru "$man_dir" "$target"; then
	printf '%s\n' "gen-man: man/ is out of date; run 'make gen' and commit the result" >&2
	exit 1
fi
printf '%s\n' "gen-man: man/ matches the command tree"
