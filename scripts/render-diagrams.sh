#!/usr/bin/env bash
# Render every Mermaid diagram in the documentation files that differ from
# origin/main. A diagram that fails to parse or to render fails the check: an
# unrenderable diagram is invisible to the reader and does not show up in a
# text diff.
#
# A missing renderer or comparison base is an error, never a silent skip.
set -euo pipefail

# Portable root resolution: no readlink -f, which macOS lacks.
root="$(dirname "${BASH_SOURCE[0]}")/.."

if ! command -v mmdc >/dev/null 2>&1; then
  echo "docs-check: mmdc not found on PATH; install @mermaid-js/mermaid-cli to render diagrams" >&2
  exit 1
fi

base="origin/main"
if ! git -C "$root" rev-parse --verify --quiet "$base" >/dev/null; then
  {
    echo "docs-check: $base not found; the Mermaid check compares changed documentation against origin/main."
    echo "docs-check: fetch it with 'git fetch origin main' (HEAD is not used as a fallback)."
  } >&2
  exit 1
fi

changed="$(
  {
    git -C "$root" diff --name-only "$base" -- docs
    git -C "$root" ls-files --others --exclude-standard -- docs
  } | sort -u | grep -E '\.md$' || true
)"

if [ -z "$changed" ]; then
  echo "docs-check: no documentation changed against $base; nothing to render"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

rendered=0
failed=0
failed_files=""

while IFS= read -r file; do
  [ -f "$root/$file" ] || continue
  if ! grep -q '^```mermaid' "$root/$file"; then
    continue
  fi

  slug="$(printf '%s' "$file" | tr '/ ' '__')"
  mkdir -p "$tmp/$slug"
  # Create the file at the opening fence so a body-less diagram still reaches
  # mmdc and fails there, instead of being skipped silently.
  awk -v dir="$tmp/$slug" '
    /^```mermaid[[:space:]]*$/ { inblock = 1; n++; f = sprintf("%s/block-%d.mmd", dir, n); printf "" > f; next }
    inblock && /^```[[:space:]]*$/ { inblock = 0; next }
    inblock { print > f }
  ' "$root/$file"

  for diagram in "$tmp/$slug"/block-*.mmd; do
    [ -f "$diagram" ] || continue
    rendered=$((rendered + 1))
    block="${diagram##*/block-}"
    block="${block%.mmd}"
    if ! mmdc -q -i "$diagram" -o "$diagram.svg" >"$diagram.log" 2>&1; then
      failed=$((failed + 1))
      failed_files="$failed_files
  $file (block $block)"
      echo "docs-check: failed to render a diagram from $file" >&2
      sed 's/^/    /' "$diagram.log" >&2
    fi
  done
done <<< "$changed"

if [ "$rendered" -eq 0 ]; then
  echo "docs-check: no Mermaid diagrams in the changed documentation"
  exit 0
fi

if [ "$failed" -gt 0 ]; then
  printf 'docs-check: %d of %d Mermaid diagram(s) failed to render:%s\n' \
    "$failed" "$rendered" "$failed_files" >&2
  exit 1
fi

echo "docs-check: rendered $rendered Mermaid diagram(s) from the changed documentation"
