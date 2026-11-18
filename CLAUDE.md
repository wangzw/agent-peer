# agent-peer

A cross-host communication system for agents. A host runs a Broker either as a system-wide service that serves every account or as a user-level process that serves one account. Claude Code sessions join their Broker through MCP tools, and once two Brokers are paired, agents on different hosts can discover each other and exchange messages.

## Language

- **Documents under `docs/` are written in Chinese.** This covers the requirements, design documents, architecture decision records, glossary, references and roadmap.
- **Everything else is written in English.** This covers source code, identifiers, comments, log and error messages, MCP tool names and descriptions, tests, commit messages, `README.md`, this file, skills under `.claude/`, and configuration files.
- When English text names a concept registered in `docs/glossary.md`, use that entry's English counterpart rather than a fresh translation.

## Documentation

- design: docs/design
- requirements: docs/prd
- adr: docs/adr
- glossary: docs/glossary.md
- references: docs/references.md
- language: zh-CN

`docs/roadmap.md` records priorities and milestones and belongs to none of the documentation trees above. ADRs are part of the design: a design change that adopts, replaces or reverses a significant decision adds or revises the record under `docs/adr` in the same change.
