---
name: commit-convention
description: "agent-peer Commit Message Convention: English Conventional Commits, scopes taken from the module table and the documentation tree, and repository-specific rules"
triggers:
  - creating a commit
  - writing a commit message
  - git commit
  - committing changes
globs:
  - "**/*"
---

# agent-peer Commit Message Convention

Follows [Conventional Commits](https://www.conventionalcommits.org/). The convention is enforced in review; there is no commitlint config or commit-msg hook in this repository.

**This repository is currently documentation only; the implementation will follow.** It holds requirements documents, design documents, architecture decision records, a glossary, a references file and a roadmap; the Go implementation follows the module table in `docs/design/README.md`. A design module's slug is the Go package name, so one scope names both the module's document and its code. Extend this convention from this repository's own directory structure; do not import another project's scope list.

**Language: English, always.** The documents are written in Chinese; commit messages are written in English. Module names, tool names, configuration keys, frame names, REQ, ADR and REF identifiers, file paths and the English counterparts registered in the glossary (`docs/glossary.md`) keep their exact spelling. When a change concerns a registered term, use the term's English counterpart rather than a fresh translation, and do not put Chinese prose in the message.

## Format

```text
<type>(<scope>): <description>

[optional body]

[optional footer(s)]
```

- **Header** is required and must be a single line.
- **Keep the header short: aim for 72 characters or less.** This is a guideline, not a hard limit: a header that needs a few more characters to stay clear is better than a truncated one. Do not sacrifice meaning for the count.
- **Scope is required.** Every change belongs to a named module or a named part of the documentation tree; without a scope the header cannot say which one.
- **Description** uses the imperative mood, lowercase, no trailing period: "retry delivery with the same message id", not "retried ..." or "retries ...".
- **Body** states the problem, the decision and its cost. For a substantive change it is expected, not optional: the reader of `git log` has the diff and needs the reason. A change confined to one obvious line may omit it.
- **Footer** carries `BREAKING CHANGE:` notices and references to durable external identifiers such as public issue numbers. REQ, ADR and REF identifiers belong in the body at the sentence that relies on them, not in the footer.

## Types

| Type | When to use |
| --- | --- |
| `feat` | New capability or behavior in code. A new design document, requirements document or ADR is `docs`. |
| `fix` | Correcting a defect in code, or a wrong, contradictory or broken statement, contract or check in a document. |
| `docs` | Documentation content: requirements, design documents, ADRs, glossary, references, roadmap. |
| `refactor` | Restructuring code without changing behavior, or restructuring documents without changing what they assert: moving sections, splitting or merging files. |
| `test` | Adding or updating tests with no change to the code under test. |
| `perf` | Performance improvement in code. |
| `style` | Formatting only: `gofmt`, punctuation or link syntax; no change in behavior or in what a document asserts. |
| `chore` | Repository configuration, tooling, skills and dependencies. |

A commit that implements a behavior and updates the document asserting it is `feat` or `fix`, not `docs`: the document moves with the code that changed what it says.

## Scopes

Scope names the module or the part of the tree the change belongs to. The module table in `docs/design/README.md` is the membership authority for module scopes; read that table rather than relying on a list here, which would go stale.

### Modules

- the module name — e.g. `broker`, `link`, `pairing`, `topic`; the change is confined to that module's Go package, its design document `docs/design/modules/design-<module>.md`, or both
- `cmd` — the `cmd/agent-peer` entry point and command wiring that belongs to no module

### Design documents

- `architecture` — `docs/design/architecture.md`
- `design` — a change that spans several design documents and belongs to no single module, such as a cross-document contract alignment

### Decision records

- `adr` — `docs/adr/`. ADRs are part of the design: a commit whose point is the decision itself takes `adr`; an ADR added or revised alongside a module change stays under that module's scope

### Requirements documents

- `prd` — anything under `docs/prd/`: the overview, capability documents and journeys

### Shared documents

- `glossary` — `docs/glossary.md`
- `references` — `docs/references.md`
- `roadmap` — `docs/roadmap.md`

### Repository

- `repo` — `CLAUDE.md`, `.claude/skills/`, `.gitignore`, `go.mod`, CI configuration and other repository-level configuration
- `deps` — vendored third-party source under `third_party/` and dependency version changes
- `e2e` — `test/e2e/`, the end-to-end tests, the node agent and the compose topologies

**One scope per commit.** When a module change also updates an ADR, the requirements, the glossary and the roadmap for consistency, the scope stays the module; incidental consistency edits elsewhere do not get a scope of their own. A change that touches several modules for one reason takes the scope of the module whose contract changed, or `design` when it is a documentation-only alignment.

## Examples

```text
docs(prd): require a local alias when a peer's alias collides

docs(design): align the delivery error codes across broker and link

feat(inbox): deliver a signed message to a session inbox socket

feat(pairing): bind the pairing proof to both certificate fingerprints

fix(topic): drop a roster whose digest is outside the shared set

refactor(link): move frame dispatch out of the read loop

test(directory): cover reconciliation after a revocation

docs(adr): record synchronous delivery without a retry queue

docs(glossary): register the pre-provisioned static key entry

chore(deps): vendor claude-code-socket-transport at 480bd83c0bf1

chore(repo): add the commit message convention skill

feat(mcpserver)!: remove list_topics

BREAKING CHANGE: the `list_topics` tool is removed. A session that listed
its topics with it must rely on the list returned by `join_topic` and
`leave_topic` instead. The tool table in the mcpserver design document is
updated in this commit.
```

## Rules

1. **One logical change per commit.** A single change routinely spans a Go package, its design document, an ADR, a requirement's status line, the glossary and the roadmap. That is one logical change; the file count does not make it several. Do not bundle unrelated changes.
2. **Each commit leaves code and documents consistent.** Every document and index the change makes stale is updated in the same commit: a code change that alters what a design document asserts updates that document; implementing a requirement updates its status line and, where one exists, its end-to-end test reference; a term rename reaches the glossary, the requirements, the architecture document, every module document, the ADR records and the roadmap; revising an ADR rechecks every design document that cites it. A leftover occurrence is a defect, not a follow-up.
3. **No generic messages.** "update docs", "fix typo", "wip" and "misc changes" are rejected.
4. **Breaking changes** get `!` after the scope and a `BREAKING CHANGE:` footer. A change is breaking when it removes or renames something a user, a session's tool configuration, another Broker or another document consumes: an MCP tool name or parameter, a configuration key, the one-time pairing key format, a link frame or its meaning, the local interface version, a requirement id, or a glossary term used by an interface. The commit updates the documents that define the contract.
5. **A decision belongs to an ADR; the commit says what changed.** ADRs are part of the design. When a change adopts, replaces or reverses an architecturally significant decision, add or revise the record under `docs/adr/` in the same commit, and update the design documents that cite it. A record is revised in place and carries no history; the commit body states the decision and its cost, not the debate that produced it.
6. **Verify before committing.** For a change that touches documents, run the documentation structural checker from the repository root and leave it at exit 0:

   ```bash
   python3 ~/.claude/skills/document-standards/scripts/check_documents.py
   ```

   Render every diagram in a changed document. A Mermaid diagram that fails to parse is invisible in a text diff and must not ship; render each changed document's diagrams with `mmdc` and confirm every one succeeds.

   For a change that touches Go code, leave these clean, with no warning ignored:

   ```bash
   go build ./...
   go vet ./...
   go test -race ./...
   ```

7. **A `fix` commit carries the test that reproduces the defect.** For a code defect, the commit includes a test that fails before the fix and passes after it. Automated tests never start Claude Code: the inbox socket and the session registry are simulated. For a document defect, state in the body what was wrong and how the correction was verified.
8. **Keep `main` linear.** Rebase the feature branch onto `main`, then merge with `git merge --ff-only`; never create a merge commit.
9. **Do not rewrite commits already on `main`.** A mistake in a published commit is corrected by a new commit, not by rebasing `main`.

## Content constraints

The subject and body say what this change decides and why it was needed, for the person who will dig through `git log` later. They do not narrate the process that produced the change.

1. **No internal process references anywhere**: subject, body or footer. No finding IDs, no round names ("the third review round"), no internal plan or verification states, no milestone names from the roadmap as justification, and no document filename cited as authority. Naming a file the commit itself changes is describing the diff, not a citation. The footer's reference field is for durable external identifiers such as public issue numbers only.
2. **The body is a concluding why**: the problem, the decision and its cost. No module biography, no process history (review rounds, verification states, follow-up markers). One sentence of "before this change" context is fine.
3. **Numbers that describe this diff** ("from two retries to five") are fine; they are checkable against the diff. Numbers describing the current state outside this diff ("N modules now …") are not.
4. These constraints bind every commit; commits already on `main` are never rewritten to meet them.

Good body (a concluding why):

```text
fix(broker): answer a repeated delivery from the dedupe table

A delivery request retried after a lost reply was written to the target
session a second time, because the visibility check and the inbox write ran
before the message id was looked up. The lookup now comes first, and a
repeated id returns the first result without touching the inbox. The table
keeps an id for the configured dedupe window, so a retry after that window is
delivered again.
```

Bad body (process narration and artifacts):

```text
fix(broker): address reviewer finding on duplicate delivery

The second review round flagged that retries could deliver twice (the
dup-delivery finding from the M2 hardening pass), and after a follow-up
discussion the implementation decided to check the dedupe table first,
closing the finding per the agreed disposition in the review ledger.
```
