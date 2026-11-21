#!/usr/bin/env bash
# Verify that the generated protobuf code committed under internal/ is exactly
# what `buf generate` produces from the .proto files.
#
# Generation goes to a fresh temporary directory, so the working tree is never
# modified and uncommitted handwritten Go changes cannot affect the result.
# The plugins named in buf.gen.yaml are built from the tool directives in
# go.mod, and the template handed to buf is derived from buf.gen.yaml itself,
# so that file stays the only list of generation plugins.
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

# buf.gen.yaml is the only list of generation plugins. Build each "go tool"
# plugin it names, once, into a private directory, then hand buf a copy of the
# file whose plugin command points at that binary. buf would otherwise run
# "go tool protoc-gen-go" once per proto directory in parallel; concurrent go
# commands share the build cache, which writes the tool binary while another
# copy executes it, and that fails with "text file busy"
# (golang.org/issue/22315). A binary built here is closed before buf starts it
# and is never rewritten, so parallel gen-check runs do not race either.
plugin_dir="$tmp/plugins"
plugin_template="$tmp/buf.gen.yaml"
mkdir -p "$plugin_dir"

# A tool directive gives the module path that go build needs; buf.gen.yaml
# names the tool by the last path component.
tool_module() {
	awk -v name="$1" '
		/^tool \(/ { in_tool = 1; next }
		in_tool && /^\)/ { in_tool = 0 }
		in_tool {
			path = $1
			base = path
			sub(/.*\//, "", base)
			if (base == name) { print path; exit }
		}
	' "$repo_root/go.mod"
}

plugin_names="$(sed -n 's/.*local: \[[^]]*"go", *"tool", *"\([^"]*\)".*/\1/p' "$repo_root/buf.gen.yaml")"
if [ -z "$plugin_names" ]; then
	printf '%s\n' "gen-check: buf.gen.yaml names no 'go tool' plugin" >&2
	exit 1
fi
for name in $plugin_names; do
	module="$(tool_module "$name")"
	if [ -z "$module" ]; then
		printf '%s\n' "gen-check: go.mod has no tool directive for $name" >&2
		exit 1
	fi
	if ! ( cd "$repo_root" && go build -o "$plugin_dir/$name" "$module" ); then
		printf '%s\n' "gen-check: 'go build $module' failed" >&2
		exit 1
	fi
done

# Replace only the command of each local plugin. Every other line, including
# strategy: all and the output options, stays exactly as committed.
sed -E "s|local: \[[^]]*\"go\", *\"tool\", *\"([^\"]*)\"[^]]*\]|local: [\"$plugin_dir/\1\"]|" \
	"$repo_root/buf.gen.yaml" > "$plugin_template"
if grep -q '"tool"' "$plugin_template"; then
	printf '%s\n' "gen-check: could not rewrite every 'go tool' plugin in buf.gen.yaml" >&2
	exit 1
fi

# Generate from the repository root without writing into the worktree.
if ! ( cd "$repo_root" && buf generate --template "$plugin_template" --output "$tmp" ); then
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

# The man pages are the other committed generated artifact. scripts/gen-man.sh
# --check regenerates them into a temporary directory and diffs them against
# man/, so a command or flag change that was not followed by 'make gen' fails
# here as well.
if ! bash "$script_dir/gen-man.sh" --check; then
	printf '%s\n' "gen-check: generated man pages do not match the command tree" >&2
	exit 1
fi

printf '%s\n' "gen-check: generated code matches the protos under internal/"
