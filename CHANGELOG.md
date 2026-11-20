# Changelog

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-11-20

### Added

- Terminal commands for every Broker management operation: `agent-peer config get` and `agent-peer config set` read and change configuration, `agent-peer pairing-key create` creates a one-time pairing key, and `agent-peer peer list`, `agent-peer peer connect` and `agent-peer peer disconnect` list, pair with and unpair Brokers. Each command is authorized against the account that runs it, as the corresponding agent-facing tool is, and writes the same audit entries.
- Broker self-update, the `update_broker` tool and the `agent-peer update` terminal command. A Broker that runs under a service manager can be asked to install the latest official release: it downloads and verifies the release, replaces its own binary, replies, and exits so that the service manager starts the new version. The `update_broker` tool lets a session start this upgrade; Claude Code asks for confirmation before the call runs, unless the tool has been allowed in advance. The `agent-peer update` terminal command asks the selected Broker to update itself, and the Broker authorizes the request against the caller's write permission on its configuration file; the command replaces the binary locally only for a user-level Broker that does not run under a service manager, or when no Broker is running, after checking write permission on the binary's directory.
- A system-level admin group. Installing a system-level Broker creates the `agent-peer` group, which `--admin-group <name>` renames, and gives a newly generated configuration file root ownership, the group as its group, and mode `0660`. Members of the group manage the Broker without `sudo` when their account runs no user-level Broker; an account that also runs one uses `sudo` to reach the system-level Broker. The install script asks whether to add the installing account to the group and defaults to no; `--join-admin-group` adds it without asking. An existing installation keeps its configuration file unchanged: rerun the install script, or `sudo agent-peer service install --system`, to create the group, then run the `chgrp` and `chmod 0660` commands it prints. Skip that step when `broker.listen` uses a port below 1024, because such a Broker refuses to start while another account can write the file.
- An install script in every release archive. It installs the binary, installs and starts the Broker as a service, and registers the MCP server with Claude Code, for the account that runs it or, with `--system`, for the account that invoked `sudo`. Running it again is safe and does not register the MCP server twice.
- One-command online installation: `curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh` downloads the latest release for the platform, verifies it against `checksums.txt`, and runs the archive's install script. `--version <version>` installs a published version, and a system-level installation runs the same command with `sudo` and `--system`.

### Changed

- Installing a user-level or system-level service now generates a configuration file that lists every configuration item when no file exists: items with defaults are written with their values, items without a fixed default are shown commented out with an example, and each item has a one-line description. An existing file is left unchanged.
- `set_config` now replaces a commented-out placeholder for the key in place with the new value, instead of adding a second, active line elsewhere in the section.
- A Broker with no alias configured now derives its default alias from the hostname and the account that runs it: a system-level Broker uses the first segment of the hostname, such as `buildbox`, and a user-level Broker uses that segment followed by the account name, such as `buildbox.alice`. The default no longer depends on the listen port. A Broker upgraded from v0.1.0 without an explicit `broker.alias` therefore gets a new alias, and its agents get new addresses. To keep the old alias, read it before upgrading, from the `get_config` tool for `broker.alias` or from the part after `@` in the agent addresses that `agent-peer status` prints, and set `broker.alias` to it after upgrading. With the default port it is the full hostname followed by `-17443`, shortened to 24 characters, for example `buildbox-17443`.
- Brokers now check GitHub for a newer official release shortly after start and every 24 hours by default; `update.check` turns the check off and `update.check_interval` changes the interval. When a newer release exists, `agent-peer status` prints its version, and a tool result in each session ends with a one-line notice, once per session and version. The Broker never installs a release on its own; an update always starts from `agent-peer update` or the `update_broker` tool.
- A system-level Broker applies a `broker.listen` port below 1024 only when the configuration file is writable by root alone: a non-root caller cannot set such a port through the tools or a terminal command, a reload keeps the old value and writes a `config_rejected` audit entry, and the Broker refuses to start when another account can write the file.

### Removed

- The `AGENT_PEER_SOCKET` environment variable, a breaking change. A session and a terminal command now select their Broker by operating-system account alone: the user-level Broker running for the calling account if there is one, otherwise the system-level Broker if the account may use its socket. An account that also runs a user-level Broker reaches the system-level Broker with `sudo`. Setting the variable no longer affects the selection.

### Fixed

- Release bodies are now generated from the changelog. goreleaser did not load the extracted notes, so v0.1.0 was published with an empty body, and the body was added by hand afterwards.
- The README install steps now branch before moving the binary, so following the user-level and system-level paths in order no longer fails at the second step; the extraction step now uses an empty directory because the archive has no top-level directory.
- Writing the configuration file now refuses to follow a symbolic link: a symlink at the configuration path makes the write fail and leaves its target unchanged.

## [0.1.0] - 2026-11-19

### Added

- Cross-session messaging on one host: sessions that enable the agent-peer tools register with the local Broker as agents, and can list the agents visible to them and send messages to those agents.
- A user-level Broker that serves one account's sessions, with at most one Broker instance running per account.
- Topics that limit which agents can see and message each other, including a default topic derived from the project configuration file or the workspace's git origin so that sessions in the same project share a topic without extra steps.
- Broker pairing with one-time pairing keys and reusable static keys, direct links that the dialer re-establishes after a drop, cross-host message delivery, and an outcome-unknown result when a delivery request may or may not have been processed.
- Broker cards and public topics, which distribute public Brokers and public topics through the connected network while a private Broker stays unknown to Brokers it is not paired with.
- Reading and changing Broker configuration through the MCP tools, subject to the same file permissions as editing the configuration by hand, and an audit log of the changes.
- Terminal commands `agent-peer status` and `agent-peer audit` for viewing Broker status and the audit log on the host.
- A system-level Broker that serves the sessions of multiple accounts, and installation of a user-level Broker as a service that starts at login or a system-level Broker as a service that starts at boot; uninstall stops the service before removing its definition and keeps configuration, identity and audit logs.
- Handling of incompatible protocol versions on both the local interface and links: a tool call fails with a prompt to restart the Broker when the calling session's MCP server process and the running Broker use incompatible local protocol versions, and when two Brokers use incompatible link protocol versions the accepting side rejects the connection while the dialing side backs off and shows the reason when listing Brokers.
- Release archives for macOS and Linux on amd64 and arm64, with SHA-256 checksums.

### Known limitations

Known limitations are listed in the [Known limitations section of the README](README.md#known-limitations).
