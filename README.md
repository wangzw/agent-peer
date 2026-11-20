# agent-peer

agent-peer lets Claude Code sessions running on different hosts discover each other and exchange text messages.

Claude Code's built-in cross-session messaging covers sessions on one host. agent-peer runs Brokers on the hosts involved, and a session joins a Broker on its own host through MCP tools. The users of two hosts pair their Brokers with a pairing key. After that, sessions on both sides that share a topic can see each other and send messages. Sessions on the same host that share a topic can use the same tools. The receiving Broker writes each message into the target session's inbox socket: an idle session picks it up and starts working on it without user input, and the session's own inbound controls and permission prompts still apply.

## Status

The current release is 0.2.0. It implements the requirements of milestones M1 to M4 in the [roadmap](docs/roadmap.md). Each requirement's implementation status is recorded in the status line of the requirement document ([requirements](docs/prd/README.md)).

## Install

### One-command installation

Install the latest release with one command:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

The script detects the platform and architecture, downloads the matching archive, verifies it against the release's `checksums.txt`, and runs the install script from the archive. For a system-level installation, run the same command with `sudo`; the MCP server is registered for the account that invoked `sudo`:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --system
```

To install a published version instead of the latest, pass `--version <version>` before any other option, using a published version number such as `0.2.0`. A version that does not exist makes the command fail with `version <version> not found` and leaves the host unchanged:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --version <version>
```

Options that follow `--version <version>` are passed to the archive's install script.

Piping the script to a shell carries the same trust as downloading the archive and running it by hand: the releases are not signed. To inspect the script first, download it, read it, and run it from the directory that holds it:

```sh
curl -fsSLO https://github.com/wangzw/agent-peer/releases/latest/download/install.sh
sh install.sh
```

### Install from the archive

agent-peer is distributed as a `tar.gz` archive on [GitHub Releases](https://github.com/wangzw/agent-peer/releases). Pick the archive for your platform and architecture:

| Platform | Architecture | Archive |
|---|---|---|
| macOS | amd64 | `agent-peer_0.2.0_darwin_amd64.tar.gz` |
| macOS | arm64 | `agent-peer_0.2.0_darwin_arm64.tar.gz` |
| Linux | amd64 | `agent-peer_0.2.0_linux_amd64.tar.gz` |
| Linux | arm64 | `agent-peer_0.2.0_linux_arm64.tar.gz` |

Download the archive together with `checksums.txt` from the same release and verify the archive before using it. On Linux:

```sh
grep agent-peer_0.2.0_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

On macOS, use `shasum` instead:

```sh
grep agent-peer_0.2.0_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

Create an empty directory and extract the archive into it. The archive has no top-level directory, so without one, LICENSE, README.md, third_party/ and THIRD_PARTY_LICENSES/ land directly in the current directory together with the binary.

The macOS binaries are not signed or notarized, so on macOS, if you downloaded it with a browser, clear the quarantine attribute from the extracted binary before running it; otherwise macOS blocks the binary:

```sh
mkdir agent-peer-0.2.0
tar -xzf agent-peer_0.2.0_darwin_arm64.tar.gz -C agent-peer-0.2.0
xattr -d com.apple.quarantine agent-peer-0.2.0/agent-peer
```

On Linux, extract the archive without the `xattr` step:

```sh
mkdir agent-peer-0.2.0
tar -xzf agent-peer_0.2.0_linux_arm64.tar.gz -C agent-peer-0.2.0
```

#### Install script

The archive contains `install.sh` at its root. Change into the directory that holds the extracted files and run it:

```sh
sh ./install.sh
```

The script installs the binary into `~/.local/bin`, installs and starts the user-level Broker as a service, and registers the MCP server with Claude Code for the account that runs it. For a system-level installation, run it as root, which installs the binary into `/usr/local/bin`, starts the system-level Broker, and registers the MCP server for the account that invoked `sudo`:

```sh
sudo sh ./install.sh --system
```

On a host where a system-level Broker already runs, that Broker serves every account, so a user-level one is usually unnecessary. To install a user-level Broker anyway, set `broker.listen` in `~/.agent-peer/config.yaml` to another port, or leave it empty to run without a TCP listener; otherwise both Brokers use the same port, and the installation stops with the address reported as already in use.

The script accepts these options:

| Option | Effect |
|---|---|
| `--prefix <dir>` | Install the binary into `<dir>` instead of the default (`~/.local/bin` for a user-level installation, `/usr/local/bin` for a system-level one). |
| `--no-service` | Do not install the Broker service. |
| `--no-mcp` | Do not register the MCP server. |

The script is safe to run again: it reinstalls the binary, restarts the service with it, and leaves an existing MCP registration in place. The archive's install script never accesses the network; everything it installs comes from the extracted archive.

### Manual installation

The manual steps below install the binary only; register the MCP server and install the Broker service as described in [Register the MCP server with Claude Code](#register-the-mcp-server-with-claude-code).

If you only use the user-level Broker, a per-user directory such as `~/.local/bin` is enough:

```sh
mkdir -p ~/.local/bin
mv agent-peer-0.2.0/agent-peer ~/.local/bin/
agent-peer --version
```

If you want to run `sudo agent-peer service install --system`, install the binary with `sudo install -o root -g 0 -m 0755` instead of `mv`. The binary and every directory above it must be owned by `root` and not writable by group or others, such as `/usr/local/bin`. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so the installation is refused; choose a directory owned by `root` instead. The system-level service starts the binary as `root`, and an ordinary account that can modify the binary or one of its parent directories could take over the service:

```sh
sudo install -o root -g 0 -m 0755 agent-peer-0.2.0/agent-peer /usr/local/bin/agent-peer
agent-peer --version
```

In both cases, `agent-peer --version` prints `agent-peer 0.2.0`.

To build from source you need Go 1.27. Clone the repository and build:

```sh
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=0.2.0" -o agent-peer ./cmd/agent-peer
```

Without `-ldflags`, `agent-peer --version` prints `agent-peer dev`. Installing with `go install …@version` is not supported, because `go.mod` uses a `replace` directive that points at a vendored dependency.

### Uninstall

Stop and remove the service, remove the MCP registration, and delete the binary:

```sh
agent-peer service uninstall
claude mcp remove -s user peer
```

Add `--system` to `service uninstall` for a system-level service. Configuration, identity and audit logs are kept; remove them separately if they are no longer needed. Delete the installed binary as well, such as `~/.local/bin/agent-peer` or `/usr/local/bin/agent-peer`.

## Register the MCP server with Claude Code

The one-command and archive installations register the server automatically; the steps here apply to a manual installation. Register the MCP server once for your account so that it is available in all your projects:

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

The same pairing works from a terminal: on the host that can be reached, run `agent-peer pairing-key create`; on the other host, run `agent-peer peer connect <address> --key <key>`.

On a system-level Broker, by default, an ordinary account has no write permission on the configuration file, so a session cannot create a pairing key. Ask the administrator to run `sudo agent-peer pairing-key create` and pass the key on.

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
| `update_broker` | Upgrade this Broker to the latest official release and restart it, returning the from and to versions; report that it is already up to date when there is no newer release. Claude Code asks for confirmation before the call runs. |

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
| `agent-peer update [--yes]` | Check GitHub for a newer release and install it. The command asks the selected Broker to update itself; when the selected Broker is the account's user-level Broker and does not run as a service, the command replaces the binary locally. `--yes` skips the confirmation question. |
| `agent-peer service install [--system]` | Install the Broker as a user-level service started at login, or as a system-level service started at boot, and start it immediately. |
| `agent-peer service uninstall [--system]` | Stop and remove the installed service. Configuration, identity and audit logs are kept, and the command can be repeated when nothing is installed. |
| `agent-peer broker [--system] [--listen <addr>]` | Run a Broker in the foreground; `--system` selects the system-level form and requires root, and `--listen` overrides the configured listen address for this run only. |

A terminal command selects its Broker by the account that runs it, with no explicit flag: root connects to the system-level Broker, and another account uses its own user-level Broker first, then the system-level one when none is running. An administrator account that runs no user-level Broker therefore reaches the system-level Broker with the command as written; one that also runs a user-level Broker uses `sudo` to manage the system-level Broker. When an administrator runs these commands with `sudo`, a system-level Broker authorizes them against root's permissions.

On macOS, a user-level service runs in launchd's `gui` domain, which exists only in a graphical login session; install it from such a session, or use `--system` on a host without one.

Without read permission on the configuration file, `agent-peer status` still lists every peer and its connection state; it only omits the agents of other accounts.

### Updating

Each Broker checks GitHub for a newer official release shortly after it starts, then every 24 hours by default. `update.check` turns the check off and `update.check_interval` changes the interval. When a newer release exists, `agent-peer status` prints its version and the first tool result in a session ends with a one-line notice. The Broker never installs a release without confirmation.

Run the update in a terminal:

```sh
agent-peer update
```

The command shows the installed version and asks for confirmation. It asks the Broker selected for the account to update itself. When the selected Broker is a user-level Broker that does not run as a service, or when no Broker is running, the command checks write permission on the binary's directory, then downloads the release archive for this platform, checks it against `checksums.txt`, verifies that the new binary reports the expected version, and replaces this binary. It does not start or restart any service, because the service may have been stopped on purpose, and prints that the new binary takes effect the next time the Broker starts. Without write permission it fails before downloading and suggests `sudo`. `--yes` skips the question and is required when standard input is not a terminal.

For a system-level installation, run the command as an account that administers the Broker, without `sudo`:

```sh
agent-peer update
```

An administrator account that runs no user-level Broker reaches the system-level Broker directly. One that also runs a user-level Broker uses `sudo agent-peer update`, so that root connects to the system-level Broker. The Broker then downloads, checks and replaces its own binary, and the service manager restarts it. The account needs write permission on the Broker's configuration file; an account without it is refused before anything is downloaded. The command succeeds only after the Broker reports the new version.

A session can start the same upgrade with the `update_broker` tool. Claude Code asks for confirmation before the call runs. Do not add `update_broker` to an auto-approve list: that confirmation is the last manual check before an upgrade, and for a system-level installation the Broker replaces a binary that runs as root.

When the update runs through the Broker, the Broker restarts and open sessions reconnect to it automatically. The MCP server inside a session that is already open keeps running the old binary until you reconnect it with `/mcp` or restart the session. A local replacement does not start or restart anything; the new binary takes effect the next time the Broker starts.

Downloads use the proxy environment variables of the process that performs them. A system-level Broker reads them from its service environment:

- Linux: run `sudo systemctl edit agent-peer.service`, add `[Service]` and `Environment=HTTPS_PROXY=http://proxy.example:8080` (plus `HTTP_PROXY` and `NO_PROXY` as needed), then restart the service. The drop-in survives a reinstall.
- macOS: add an `EnvironmentVariables` dictionary to `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist` with `HTTPS_PROXY` and any other variables, then boot the service out and in again. A reinstall rewrites the plist, so add the dictionary again afterwards.

### Administering a system-level Broker without root

Installing a system-level Broker creates the `agent-peer` group and gives the configuration file to it. On a fresh install the install script asks whether to add the account that ran it to the group; answer `y`, or pass `--join-admin-group` to add it without asking. Log in again, or run `newgrp agent-peer`, before using the group membership. Set another group name at install time with `--admin-group <name>`.

Members of the group run the commands that need write permission on the configuration file without `sudo`. An account that runs no user-level Broker selects the system-level Broker directly; an account that also runs one of its own uses `sudo` to reach the system-level Broker:

```sh
agent-peer pairing-key create
agent-peer config set broker.alias buildbox
agent-peer peer connect <address> --key <key>
```

The group is trusted as if its members could edit the configuration file by hand; it does not make them root. Installing and uninstalling the system-level service still need root; updating it does not, because the Broker installs the release itself.

For an existing installation, give the file to the group with the command the install output prints:

```sh
sudo chgrp agent-peer /etc/agent-peer/config.yaml
sudo chmod 0660 /etc/agent-peer/config.yaml
```

Skip giving the file to the group when `broker.listen` uses a port below 1024, because such a Broker refuses to start while another account can write the file.

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
