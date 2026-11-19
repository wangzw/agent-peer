# agent-peer

agent-peer lets Claude Code sessions running on different hosts discover each other and exchange text messages.

Claude Code's built-in cross-session messaging covers sessions on one host. agent-peer runs Brokers on the hosts involved, and a session joins a Broker on its own host through MCP tools. The users of two hosts pair their Brokers with a pairing key. After that, sessions on both sides that share a topic can see each other and send messages. Sessions on the same host that share a topic can use the same tools. The receiving Broker writes each message into the target session's inbox socket: an idle session picks it up and starts working on it without user input, and the session's own inbound controls and permission prompts still apply.

## Status

0.1.0 is the first release. It implements the requirements of milestones M1 to M4 in the [roadmap](docs/roadmap.md). Each requirement's implementation status is recorded in the status line of the requirement document ([requirements](docs/prd/README.md)).

## Install

agent-peer is distributed as a `tar.gz` archive on [GitHub Releases](https://github.com/wangzw/agent-peer/releases). Pick the archive for your platform and architecture:

| Platform | Architecture | Archive |
|---|---|---|
| macOS | amd64 | `agent-peer_0.1.0_darwin_amd64.tar.gz` |
| macOS | arm64 | `agent-peer_0.1.0_darwin_arm64.tar.gz` |
| Linux | amd64 | `agent-peer_0.1.0_linux_amd64.tar.gz` |
| Linux | arm64 | `agent-peer_0.1.0_linux_arm64.tar.gz` |

Download the archive together with `checksums.txt` from the same release and verify the archive before using it. On Linux:

```sh
grep agent-peer_0.1.0_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

On macOS, use `shasum` instead:

```sh
grep agent-peer_0.1.0_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

Create an empty directory and extract the archive into it. The archive has no top-level directory, so without one, LICENSE, README.md, third_party/ and THIRD_PARTY_LICENSES/ land directly in the current directory together with the binary.

The macOS binaries are not signed or notarized, so on macOS, if you downloaded it with a browser, clear the quarantine attribute from the extracted binary before running it; otherwise macOS blocks the binary:

```sh
mkdir agent-peer-0.1.0
tar -xzf agent-peer_0.1.0_darwin_arm64.tar.gz -C agent-peer-0.1.0
xattr -d com.apple.quarantine agent-peer-0.1.0/agent-peer
```

On Linux, extract the archive without the `xattr` step:

```sh
mkdir agent-peer-0.1.0
tar -xzf agent-peer_0.1.0_linux_arm64.tar.gz -C agent-peer-0.1.0
```

Then install the binary in one of two ways. If you only use the user-level Broker, a per-user directory such as `~/.local/bin` is enough:

```sh
mkdir -p ~/.local/bin
mv agent-peer-0.1.0/agent-peer ~/.local/bin/
agent-peer --version
```

If you want to run `sudo agent-peer service install --system`, install the binary with `sudo install -o root -g 0 -m 0755` instead of `mv`. The binary and every directory above it must be owned by `root` and not writable by group or others, such as `/usr/local/bin`. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so the installation is refused; choose a directory owned by `root` instead. The system-level service starts the binary as `root`, and an ordinary account that can modify the binary or one of its parent directories could take over the service:

```sh
sudo install -o root -g 0 -m 0755 agent-peer-0.1.0/agent-peer /usr/local/bin/agent-peer
agent-peer --version
```

In both cases, `agent-peer --version` prints `agent-peer 0.1.0`.

To build from source you need Go 1.27. Clone the repository and build:

```sh
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=0.1.0" -o agent-peer ./cmd/agent-peer
```

Without `-ldflags`, `agent-peer --version` prints `agent-peer dev`. Installing with `go install …@version` is not supported, because `go.mod` uses a `replace` directive that points at a vendored dependency.

## Register the MCP server with Claude Code

Register the MCP server once for your account so that it is available in all your projects:

```sh
claude mcp add --scope user peer -- agent-peer mcp
```

If `agent-peer` is not on your `PATH`, use its absolute path in place of `agent-peer`.

The server name must be `peer`. The model sees the tools as `mcp__peer__<tool>`, and permission rules are written with that name.

Check the registration with:

```sh
claude mcp get peer
```

The output shows `Scope: User config`. A session started afterwards lists `peer` in `/mcp`.

The MCP server does not start a Broker. Start one in either of two ways: run `agent-peer service install`, which installs and starts it immediately and from then on starts it at login (at boot for a system-level service installed with `--system`); or run `agent-peer broker` in a terminal outside the Claude Code session. When no Broker answers, a tool call returns an error that names these two ways to start one, or asks the administrator to start the system-level Broker when one is installed. Once a Broker is running, the same MCP connection uses it; no reconnect is needed.

## Usage

### Sessions on the same host

Start two sessions in the same project directory. Both join the same default topic automatically: the topic comes from the project configuration file `.agent-peer.yaml`, or, when there is none, from the workspace's git origin address. Ask the model to list the agents with `list_agents`, then send a message with `send_message`.

### Sessions on different hosts

At least one of the two hosts must be able to reach port 17443 on the other. On the host that can be reached, ask the model to call `create_pairing_key`, and pass the key and the displayed address to the other host through a channel outside the system. On the other host, ask the model to call `connect_broker` with the `address` and the `key`. After that, sessions on both hosts that share a topic can see each other.

### Tools

| Tool | Description |
|---|---|
| `list_agents` | Return your own address and every visible agent with its shared topics and status; `topic`, `broker` and `name` are optional filters, and all given filters must match. A rename is reported as a former name for 10 minutes. |
| `send_message` | Send a text message to an agent address. Return success, a `Not sent` error, or a `Delivery unknown` outcome. |
| `list_topics` | List the topics you have joined and the known public topics with their descriptions. |
| `join_topic` | Join a topic, optionally declaring it public with a description, and return the updated topic list. |
| `leave_topic` | Leave a topic and return the updated topic list. |
| `list_brokers` | List every known Broker with its alias, fingerprint, advertised addresses, whether it is a peer and its status; for a peer, state whether this side dialed or accepted. |
| `connect_broker` | Pair with another Broker using its `address`, written `address:port` or `hostname(address:port)`, and a pairing `key`, with an optional local alias; return its alias and fingerprint. |
| `disconnect_broker` | Remove the pairing with a Broker. |
| `create_pairing_key` | Create a one-time pairing key and return it with its expiry time, together with this Broker's addresses to give to the other side. |
| `get_config` | Read configuration keys and their current values; for a secret key, report only whether it is set. |
| `set_config` | Change a configuration key and return the old and new values; changing a secret key always fails. |

### Terminal commands

| Command | Description |
|---|---|
| `agent-peer status` | Show agents, their topic memberships, peers and links. |
| `agent-peer audit [--limit N]` | Stream the audit log, with the newest N entries when `--limit` is given; 0 means no limit. |
| `agent-peer config get [<key>]` | Print the value of one configuration key, or every key and its value when no key is given; for a secret key, report only whether it is `set` or `unset`. |
| `agent-peer config set <key> <value>` | Change a configuration key and print its old and new values. |
| `agent-peer pairing-key create` | Create a one-time pairing key and print it with its expiry time and this Broker's addresses. |
| `agent-peer peer list` | List every known Broker with its alias, fingerprint, whether it is a peer, status, direction and addresses. |
| `agent-peer peer connect <address> --key <key> [--alias <alias>]` | Pair with another Broker at `address`, written `address:port` or `hostname(address:port)`, using a pairing key, with an optional local alias. |
| `agent-peer peer disconnect <broker>` | Remove the pairing with a Broker, named by alias or fingerprint. |
| `agent-peer service install [--system]` | Install the Broker as a user-level service started at login, or as a system-level service started at boot, and start it immediately. |
| `agent-peer service uninstall [--system]` | Stop and remove the installed service. Configuration, identity and audit logs are kept, and the command can be repeated when nothing is installed. |
| `agent-peer broker [--system] [--listen <addr>]` | Run a Broker in the foreground; `--system` selects the system-level form and requires root, and `--listen` overrides the configured listen address for this run only. |

When an administrator runs these commands with `sudo`, a system-level Broker authorizes them against root's permissions.

On macOS, a user-level service runs in launchd's `gui` domain, which exists only in a graphical login session; install it from such a session, or use `--system` on a host without one.

Without read permission on the configuration file, `agent-peer status` still lists every peer and its connection state; it only omits the agents of other accounts.

### Files

| File | User-level | System-level |
|---|---|---|
| Configuration | `~/.agent-peer/config.yaml` | `/etc/agent-peer/config.yaml` |
| Audit log | `~/.agent-peer/audit.log` | `/var/log/agent-peer/audit.log` |

## Platform

agent-peer runs on macOS and Linux, on amd64 and arm64. Claude Code v2.1.224 or later is required.

## Known limitations

- A session that runs headless and skips permission prompts does not receive messages from this system: the receiving Claude Code holds the message and drops it because the sender did not declare a permission category, while the sender's call has already returned success.
- Some sessions cannot become agents: sessions that do not bind an inbox socket, sessions for which Claude Code keeps no session registry record (including interactive sessions started from another session's command line), and sessions whose name is unusable.
- When the receiving session runs in a mode that skips permission prompts, messages from this system need individual approval before they enter the session.
- The version of Claude Code on which each behavior was observed is recorded for each item in the [roadmap](docs/roadmap.md).

## Development

The build gates live in the Makefile. `make lint`, `make test` and `make docs-check` run on any supported platform. The end-to-end suite (`make e2e`) needs Linux and a working Docker daemon and uses containers, including for the system-level tests; do not run it on macOS. Pass the job count explicitly, for example `make e2e JOBS=4`.

## How it works

agent-peer is a point-to-point system. It does not interoperate with external A2A agents, and it does not support native Windows. Messages carry plain text only, and nothing is cached, persisted or redelivered: a send is synchronous and returns success, not sent, or unknown.

The inbox socket message format and the session registry are Claude Code internals, and neither has a public compatibility promise. Rate limiting, Claude Code's own duplicate suppression and the queue limit for held messages are handled by the receiving Claude Code, and agent-peer does not change these behaviors. The receiving Broker also discards a resent copy of a message by its message identifier before writing it. The operating system account is the isolation boundary: any process running under the same account can register any of that account's sessions as an agent and act as it. On Linux, account and group lookups go through getent, so entries from NSS sources such as LDAP or SSSD are visible; when getent is unavailable, the Broker falls back to reading the local account files. The macOS binaries are not signed or notarized; [Install](#install) shows how to clear the quarantine attribute.

- A Broker runs in one of two forms. A system Broker runs as a service with privileges and serves the sessions of all accounts on the host that are allowed to use it. A user Broker runs under an ordinary account and serves that account's sessions only. An account runs at most one Broker instance.
- Only sessions that enable the agent-peer tools take part. Each session's MCP server process registers it with a Broker as an agent, reporting the session's inbox socket path from its environment. The Broker finds the session's record in Claude Code's session registry, takes the agent's name from the session name and its status from the record, and follows changes to the record, so a rename is picked up and clearing the conversation changes nothing. An agent is identified by its session's inbox socket, and its disambiguator is computed the same way as the identifier Claude Code shows in its own session list. A session whose name is empty after Claude Code's normalization or contains `@` or `*` does not become an agent. The Broker takes the calling account from the process credentials the operating system reports for the local socket, and accepts a registration only for an inbox socket that account owns.
- By default a Broker listens on port 17443 on all local addresses, IPv6 included when enabled. The port is an ordinary default value with no special treatment. If the configured address cannot be bound, the Broker fails to start; it never picks another address or port. Other Brokers reach it at its advertised addresses, which by default are the addresses of the network interface that carries the default route; the user can replace them with configured addresses or add extra ones.
- Brokers connect directly. They do not relay messages, and there is no central registry. Two hosts can hold a two-way link as long as one of them can reach the other: the host that can reach the other initiates pairing, and from then on only the dialer (the Broker that initiated pairing) re-establishes the link when it drops.
- Users establish trust pair by pair. The accepting side generates a one-time pairing key and passes it, together with its Broker's address, to the other side through a channel outside the system; the address is not secret and may travel separately. Alternatively, a reusable pre-provisioned static key that the user or administrator writes into the Broker's configuration file can be used; the system never generates it. Once the handshake succeeds, each Broker recognizes the other by its public-key fingerprint. No certificate authority is involved.
- Topics decide visibility. Two sessions can see and message each other only when they share at least one topic. A session joins a default topic taken from its project configuration file or, failing that, from its workspace's git origin address, so sessions working in the same project need no extra step. Automatic joining can be turned off.
- Sending is a synchronous, point-to-point call. A message counts as sent once it is written to the target session's inbox socket. If the message cannot be written, the call retries or waits for the link to come back, then returns within a bounded time reporting that it was not sent; if a delivery request went out but no definite answer came back, it reports that the outcome is unknown.
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
| [Roadmap](docs/roadmap.md) | Priorities, milestones, findings of the feasibility experiment, verification findings |
| [Glossary](docs/glossary.md) | Terms the project gives a meaning of its own |
| [References](docs/references.md) | Public external sources the documents cite |

For the overall picture, start with the [system overview](docs/prd/overview.md) and the [architecture](docs/design/architecture.md).

## License

agent-peer is released under the [Apache License 2.0](LICENSE).
