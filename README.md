# agent-peer

agent-peer lets Claude Code sessions running on different hosts discover each other and exchange text messages.

Claude Code's built-in cross-session messaging covers sessions on one host. agent-peer runs Brokers on the hosts involved, and a session joins a Broker on its own host through MCP tools. The users of two hosts pair their Brokers with a pairing key. After that, sessions on both sides that share a topic can see each other and send messages. Sessions on the same host that share a topic can use the same tools. The receiving Broker writes each message into the target session's inbox socket: an idle session picks it up and starts working on it without user input, and the session's own inbound controls and permission prompts still apply.

## Status

The project is at the design stage. The repository holds documentation only, and there is no runnable implementation yet. Requirements, design and architecture decisions are written down, and implementation follows the milestones in the [roadmap](docs/roadmap.md).

The premise has been checked by hand: a message carrying only a sender attribution, written to the inbox socket of a Claude Code v2.1.327 session, woke the idle session, and the session read it. The roadmap records what that experiment found and what still has to be confirmed during implementation.

## Design in brief

- A Broker runs in one of two forms. A system Broker runs as a service with privileges and serves the sessions of all accounts on the host that are allowed to use it. A user Broker runs under an ordinary account and serves that account's sessions only; a session's MCP server process starts one when no usable Broker is available. An account runs at most one Broker instance.
- Only sessions that enable the agent-peer tools take part. Each session's MCP server process registers it with a Broker as an agent, reporting the session's inbox socket path from its environment. The Broker finds the session's record in Claude Code's session registry, takes the agent's name from the session name and its status from the record, and follows changes to the record, so a rename is picked up and clearing the conversation changes nothing. An agent is identified by its session's inbox socket, and its disambiguator is computed the same way as the identifier Claude Code shows in its own session list. A session whose name is empty after Claude Code's normalization or contains `@` or `*` does not become an agent. The Broker takes the calling account from the process credentials the operating system reports for the local socket, and accepts a registration only for an inbox socket that account owns.
- By default a Broker listens on port 17443 on all local addresses, IPv6 included when enabled. The port is an ordinary default value with no special treatment. If the configured address cannot be bound, the Broker fails to start; it never picks another address or port. Other Brokers reach it at its advertised addresses, which by default are the addresses of the network interface that carries the default route; the user can replace them with configured addresses or add extra ones.
- Brokers connect directly. They do not relay messages, and there is no central registry. Two hosts can hold a two-way link as long as one of them can reach the other: the host that can reach the other initiates pairing, and from then on only the dialer (the Broker that initiated pairing) re-establishes the link when it drops.
- Users establish trust pair by pair. The accepting side generates a one-time pairing key and passes it, together with its Broker's address, to the other side through a channel outside the system; the address is not secret and may travel separately. Alternatively, a reusable pre-provisioned static key that the user or administrator writes into the Broker's configuration file can be used; the system never generates it. Once the handshake succeeds, each Broker recognizes the other by its public-key fingerprint. No certificate authority is involved.
- Topics decide visibility. Two sessions can see and message each other only when they share at least one topic. A session joins a default topic taken from its project configuration file or, failing that, from its workspace's git origin address, so sessions working in the same project need no extra step. Automatic joining can be turned off.
- Sending is a synchronous, point-to-point call that carries plain text. A message counts as sent once it is written to the target session's inbox socket. If the message cannot be written, the call retries or waits for the link to come back, then returns within a bounded time reporting that it was not sent; if a delivery request went out but no definite answer came back, it reports that the outcome is unknown. Nothing is cached, persisted or redelivered.
- The receiving Broker labels each message with the sender's agent address as the receiver sees it, so a reply to that address always resolves.
- Managing a Broker through the tools needs the same file permission as editing its configuration by hand: write access to the configuration file to change settings, create one-time pairing keys, pair or unpair; read access to read the configuration and the audit log, and to see status beyond the caller's own agents. Listing, joining topics and sending need neither.
- A Broker is private by default. A Broker set to public becomes known, with its advertised addresses, to every Broker in the connected network; pairing with it is still the user's decision.

## Documentation

The documents are written in Chinese.

| Document | Contents |
|---|---|
| [Requirements](docs/prd/README.md) | What the system must do as seen from outside, one file per capability, plus user journeys |
| [Design](docs/design/README.md) | Architecture overview and the design of each module |
| [Architecture decision records](docs/adr/README.md) | For each significant decision: the options weighed, the one adopted, and the cost accepted |
| [Roadmap](docs/roadmap.md) | Priorities, milestones, findings of the feasibility experiment, items still to verify |
| [Glossary](docs/glossary.md) | Terms the project gives a meaning of its own |
| [References](docs/references.md) | Public external sources the documents cite |

For the overall picture, start with the [system overview](docs/prd/overview.md) and the [architecture](docs/design/architecture.md).

## Platform

The design targets macOS and Linux. Native Windows is not supported. Claude Code v2.1.224 or later is required, the minimum version that has cross-session messaging.

## License

agent-peer is released under the [Apache License 2.0](LICENSE).
