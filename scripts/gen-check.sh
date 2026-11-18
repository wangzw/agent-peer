#!/usr/bin/env bash
# Verify that the generated protobuf code committed under internal/ is exactly
# what `buf generate` produces from the .proto files.
#
# Generation goes to a fresh temporary directory, so the working tree is never
# modified and uncommitted handwritten Go changes cannot affect the result.
# The comparison covers the generated set as a whole: every file `buf generate`
# produces must exist unchanged in the worktree, and every *.pb.go or
# *_grpc.pb.go file in the worktree must be one this run produced. Comparing
# against the whole set is what catches a stale *_grpc.pb.go left behind after
# its service is removed: its .proto still exists, so checking only for a
# neighbouring .proto would accept it.

set -euo pipefail

script_dir="$(CDPATH='' cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(dirname "$script_dir")"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Generate from the repository root without writing into the worktree.
if ! ( cd "$repo_root" && buf generate --output "$tmp" ); then
	printf '%s\n' "gen-check: 'buf generate' failed" >&2
	exit 1
fi

status=0
fail() {
	printf 'gen-check: %s\n' "$*" >&2
	status=1
}

generated_root="$tmp/internal"
worktree_root="$repo_root/internal"
generated_list="$tmp/generated.paths"

# Every file the generation produced must exist unchanged in the worktree, and
# its path is recorded for the reverse comparison below.
if [ ! -d "$generated_root" ]; then
	fail "'buf generate' produced no output under internal/"
	: > "$generated_list"
else
	: > "$generated_list"
	while IFS= read -r -d '' generated; do
		rel="${generated#"$tmp"/}"
		printf '%s\n' "$rel" >> "$generated_list"
		target="$repo_root/$rel"
		if [ ! -f "$target" ]; then
			fail "missing generated file: $rel (produced by 'buf generate' but absent from the worktree)"
		elif ! cmp -s "$generated" "$target"; then
			fail "out-of-date generated file: $rel (differs from what 'buf generate' produces)"
		fi
	done < <(find "$generated_root" -type f -print0)
fi

# Every generated Go file in the worktree must be one this run produced. A
# handwritten .go file has neither suffix and is ignored.
if [ -d "$worktree_root" ]; then
	worktree_list="$tmp/worktree.paths"
	: > "$worktree_list"
	while IFS= read -r -d '' found; do
		printf '%s\n' "${found#"$repo_root"/}" >> "$worktree_list"
	done < <(find "$worktree_root" -type f \( -name '*.pb.go' -o -name '*_grpc.pb.go' \) -print0)

	# comm needs both inputs sorted under the same collation.
	LC_ALL=C sort -o "$generated_list" "$generated_list"
	LC_ALL=C sort -o "$worktree_list" "$worktree_list"
	stale_list="$tmp/stale.paths"
	LC_ALL=C comm -13 "$generated_list" "$worktree_list" > "$stale_list"
	while IFS= read -r stale; do
		fail "stale generated file: $stale (present in the worktree but not produced by 'buf generate')"
	done < "$stale_list"
fi

if [ "$status" -ne 0 ]; then
	exit "$status"
fi

printf '%s\n' "gen-check: generated code matches the protos under internal/"
