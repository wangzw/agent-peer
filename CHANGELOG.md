# Changelog

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
