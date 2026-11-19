---
title: Known limitations
weight: 7
---

agent-peer does what the rest of this site describes, and stops where this page says. The list is long on purpose: knowing the boundary is part of using the system safely.

## Messages

- Messages carry plain text only. Files, session history, and context are not transferred.
- Messages are not cached, persisted, or redelivered. If a host cannot be reached, sending returns an error, and whether to send again is the sending agent's decision.
- A successful send means the message was written to the target session's inbox socket. It does not mean the target model read or processed it, and no later notification says what happened.
- When a delivery request went out and no definite answer came back, the call reports that the outcome is unknown instead of reporting success or failure.
- Rate limiting, duplicate suppression, and the queue limit for pending messages are rules of the receiving Claude Code. See the [security model]({{< relref "security.md" >}}) for how they work and why agent-peer leaves them there.

## Connections

- Brokers connect directly. Messages are not relayed through a third Broker, and there is no central registry. When neither of two hosts can reach the other, for example when both are behind NAT, they cannot communicate.
- The default listen port is 17443. If the configured listen address cannot be bound, the Broker fails to start; it never selects another address or port.
- There is no interoperability with external A2A agents.

## Platform and accounts

- agent-peer runs on macOS and Linux, on amd64 and arm64. Native Windows is not supported.
- At most one Broker runs per account.
- The macOS binaries are not signed or notarized. If a browser downloaded the archive, clear the quarantine attribute before running the binary.
- Linux release binaries are built with `CGO_ENABLED=0` and read accounts only from `/etc/passwd` and `/etc/group`. A system Broker cannot spawn helper processes for accounts supplied by a directory service such as LDAP or SSSD, and cannot use their group membership to decide configuration file permissions. When it cannot determine such an account's home directory, it uses `HOME`.

## Sessions and operation

- The inbox socket message format and the session registry have no public compatibility promise. A Claude Code upgrade may break registration or delivery.
- Some sessions cannot become agents: sessions that do not bind an inbox socket, sessions for which Claude Code keeps no session registry record (including interactive sessions started from another session's command line), and sessions whose name is unusable.
- If the MCP server process crashes, an interactive session does not restart it. A handshake that takes longer than 30 seconds or a failed start marks the service as failed, and the user has to reconnect it in `/mcp`.
- The operating-system account is the isolation boundary. Any process running under the same account can register any of that account's sessions as an agent and act as it.

## Not yet verified in a real session

The following behaviors have not been confirmed in a real Claude Code session running through agent-peer. Do not rely on them until they are confirmed.

- A session registers itself automatically, and a rename, a cleared conversation, or disabling and re-enabling the MCP server is reflected as described.
- The disambiguator in an agent address matches the identifier Claude Code shows for the session, and stays the same after a rename or a cleared conversation.
- The agent list shows each session's status as recorded in the session registry.
- A delivered message wakes an idle session without user input.
- When a busy session reads a delivered message.
- Whether a session that runs in a mode which skips permission prompts and has no inbound policy holds incoming messages for approval, and whether a headless session in that state can receive them.
- The receiving session sees the sender's agent address, a reply to that address reaches the sender, and long names or names with special characters are handled through a disambiguator and truncation.
- The target session's inbound controls decide whether a message is accepted, held, or rejected.
- Subagents call the tools as the main session, and headless or IDE-hosted sessions register like interactive ones.
- Claude Code's permission settings can authorize reading configuration, changing configuration, and creating a pairing key separately.
- After the tools are explained, the model replies to an agent address with `send_message` instead of another tool.
- How often a session goes unregistered because its inbox is bound after its MCP server process starts.
- Whether a sub-session inherits its parent's inbox socket variable, which would register it under the parent's agent.
- The session registry's file change notifications on macOS and Linux, including whether a rename shows up in the list.
- How the receiving Claude Code applies rate limiting and duplicate suppression when messages are written through one Broker process.
