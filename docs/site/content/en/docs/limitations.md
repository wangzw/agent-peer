---
title: Design choices and caveats
weight: 7
---

agent-peer does what the rest of this site describes. This page explains the design choices behind how it works and the caveats that follow from them.

## Design choices

- Messages carry plain text only. Files, session history and context are not part of the protocol, so each delivered message is a piece of text the receiving side can read and judge on its own. See the [security model]({{< relref "security.md" >}}) for how a message from another host is treated.
- Messages are not cached, persisted or redelivered. A send either writes into the target session's inbox socket and reports success, returns not sent after a bounded wait, or reports that the outcome is unknown because a request went out but no definite answer came back. There is no store to replay, expire or reconcile, and the sending agent decides whether to try again when a send fails or its outcome is unknown. See [Links]({{< relref "concepts/links.md" >}}).
- Brokers connect directly. There is no relay and no central registry, so no third party holds the list of hosts or sees the traffic; the requirement that follows is that at least one of the two hosts can reach the other. See [Links]({{< relref "concepts/links.md" >}}) and [Pairing]({{< relref "concepts/pairing.md" >}}).
- agent-peer does not interoperate with external A2A agents. Broker-to-Broker messages reuse the A2A Message body as their wire format, but A2A tasks, streaming and callbacks are outside the design: a message from another agent is simply another message.
- agent-peer runs on macOS and Linux, on amd64 and arm64. Native Windows is not supported because inbox connections there require an authentication line that agent-peer does not write. See the [security model]({{< relref "security.md" >}}).
- An account runs at most one Broker. A second start with another listen address fails, so an account's agents, topics and configuration have one owner and one place to look. Accounts and groups come from the system's account database: on Linux the Broker queries it through `getent`, which follows the NSS configuration, and falls back to the local files with a warning when `getent` is missing, fails or returns output it cannot validate. See [Broker]({{< relref "concepts/broker.md" >}}).
- agent-peer relies on interfaces Claude Code does not publicly commit to: the inbox socket message format and the session registry. A Claude Code upgrade can change how registration or delivery works.
- Rate limiting, duplicate suppression and the pending-message queue are rules of the receiving Claude Code, not of agent-peer. agent-peer does not rate-limit sending, count hops or detect loops; the receiving Claude Code identifies the sender, applies its token bucket, drops a repeat and holds the queue. The [security model]({{< relref "security.md" >}}) gives the full rules, and Claude Code can change them.
- The macOS binaries are not signed or notarized. If a browser downloaded the archive, clear the quarantine attribute before running the binary:

  ```sh
  xattr -d com.apple.quarantine agent-peer
  ```
- The operating-system account is the isolation boundary. Any process under the same account can register any of that account's sessions as an agent and act as it; between accounts, a user Broker rejects calls from other accounts, and a system Broker accepts a registration only for an inbox socket owned by the calling account. See the [security model]({{< relref "security.md" >}}).

## Caveats

- A session that runs headless and also skips permission prompts cannot receive messages from agent-peer. The message is held first and then dropped because the sender declares no permission category, while the sending side has already returned success. See the [security model]({{< relref "security.md" >}}) for what an undeclared sender means.
- Some sessions cannot become agents: sessions that do not bind an inbox socket, sessions for which Claude Code keeps no session registry record (including interactive sessions started from another session's command line), and sessions whose name is unusable. See [Agents]({{< relref "concepts/agents.md" >}}).
- When the receiving session runs in a mode that skips permission prompts and has no inbound policy set, each message from agent-peer needs individual approval before it enters the session. See the [security model]({{< relref "security.md" >}}).
- A message that arrives while the receiving session is in the middle of a turn may not reach it as a separate turn.
