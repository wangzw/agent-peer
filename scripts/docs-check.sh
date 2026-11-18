#!/usr/bin/env bash
# Documentation gate: run the document standards checker, then render every
# Mermaid diagram in the documentation that differs from origin/main.
#
# A missing checker is tolerated only when CI is exactly "true"; a missing
# renderer dependency is always an error. Both halves run even when the first
# one fails, so the caller sees every problem in one pass.
set -uo pipefail

root="$(dirname "${BASH_SOURCE[0]}")/.."
checker="${HOME:-}/.claude/skills/document-standards/scripts/check_documents.py"
status=0

if [ -f "$checker" ]; then
  if ! python3 "$checker" --root "$root"; then
    echo "docs-check: the document standards check failed" >&2
    status=1
  fi
elif [ "${CI:-}" = "true" ]; then
  echo "docs-check: $checker not found; skipping the document standards check because CI=true"
else
  {
    echo "docs-check: $checker not found."
    echo "docs-check: install the document-standards skill, or set CI=true to skip the document standards check."
  } >&2
  status=1
fi

if ! bash "$root/scripts/render-diagrams.sh"; then
  status=1
fi

exit "$status"
