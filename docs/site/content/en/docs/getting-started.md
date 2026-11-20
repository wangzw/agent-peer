---
title: Getting Started
linkTitle: Getting Started
weight: 2
---

agent-peer connects Claude Code sessions running on different hosts. A session joins a Broker on its own host through MCP tools, and two Brokers paired with a pairing key let sessions that share a topic exchange messages.

## Before you start

agent-peer runs on macOS and Linux, on amd64 and arm64; native Windows is not supported. Claude Code v2.1.224 or later is required.

## Install

There are four ways to install agent-peer, in order of convenience: with one command from GitHub Releases; from the release archive with the `install.sh` it contains; by hand from the same archive; or by building from source. To keep an agent-peer installed from a release up to date, run `agent-peer update`; [Update agent-peer]({{< relref "guides/update.md" >}}) covers it. A binary built without a version number reports `dev` and is not updated this way.

Whatever way you install it, the MCP server must be registered for your account; reopen any Claude Code session that is already open so that it loads the new server, and check the registration with `claude mcp get peer`.

### Install with one command

The command below is the online installation: it downloads the release for this host, verifies it and runs the `install.sh` it contains, which installs the binary, installs the Broker as a service and registers the MCP server for your account:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

For a system-level installation, run the same command with `sudo` and `--system`:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --system
```

Do not run the user-level command with `sudo`.

To install a particular version, set it first and pass `--version`:

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --version "$VERSION"
```

For a system-level installation of a particular version:

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --version "$VERSION" --system
```

`--version` must come before every other argument. It names a published version to install; when that version has no release archive to install, the script stops with `version <version> not found`. Every other argument is passed unchanged to the archive's `install.sh`, so the options in the next section work here as well.

The command needs macOS or Linux on amd64 or arm64; `curl` to fetch the script, while the script itself uses `curl` or `wget` to download the archive; `tar`; `sha256sum` or `shasum`; and access to github.com. The system-level form needs `sudo`.

Unless `--version` names a release, the script resolves the latest stable release; it downloads this platform's archive and the same release's `checksums.txt` into a temporary directory, verifies the archive, extracts it and runs the `install.sh` it contains. Every download the script makes goes over HTTPS; with either `curl` or `wget`, it refuses a redirect to an http address. If a download fails or the checksum does not match, it stops and leaves the host unchanged, and the temporary directory is removed when the script exits or is interrupted. An interrupted download of the script itself runs nothing, because the work starts on its last line.

Piping a script into a shell trusts it to the same degree as downloading the archive by hand from GitHub Releases. Releases are not signed, and `checksums.txt` comes from the same release as the archive, so a matching checksum catches a damaged download but does not prove that the release is trustworthy. To inspect the script first, download it, read it, and run it from the directory that holds it:

```sh
curl -fsSLO https://github.com/wangzw/agent-peer/releases/latest/download/install.sh
sh install.sh
```

The script does the same work as the `install.sh` in the archive, described in the next section.

### Install from the release archive

agent-peer is distributed as a `tar.gz` archive on [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest). Download the archive for your platform and architecture together with `checksums.txt` from the same release:

```sh
VERSION=0.2.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

Replace `<os>_<arch>` with `darwin_amd64`, `darwin_arm64`, `linux_amd64` or `linux_arm64`. The checksum and extraction examples below use the arm64 archive; replace `arm64` with `amd64` on an amd64 host.

Check the archive against `checksums.txt` before using it. On Linux:

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

On macOS, use `shasum` instead:

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

Create a directory and extract the archive into it: the archive has no top-level directory, so without one its files, including `LICENSE`, `README.md` and `third_party/`, land directly in the current directory:

```sh
VERSION=0.2.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
```

Replace `darwin` with `linux` on Linux.

Enter the extracted directory and run the script:

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sh ./install.sh
```

For a system-level installation, run the same script as root, usually through `sudo`:

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sudo sh ./install.sh --system
```

The user-level form installs the binary into `~/.local/bin`, installs and starts the user-level Broker service, and registers the MCP server for your account. The system-level form installs the binary into `/usr/local/bin`, installs and starts the system-level Broker service, and registers the MCP server for the account that ran `sudo`; it also creates the `agent-peer` group and asks whether to add that account to it. [The admin group]({{< relref "guides/management-permissions.md" >}}#the-admin-group) covers the group, the question and its effect.

The script accepts these options:

| Option | Effect |
|---|---|
| `--prefix <dir>` | Install the binary into `<dir>` instead of the default, `~/.local/bin` for a user-level installation or `/usr/local/bin` for a system-level one. |
| `--no-service` | Do not install the Broker service. [Start the Broker](#start-the-broker) yourself. |
| `--no-mcp` | Do not register the MCP server. |
| `--admin-group <name>` | Manage the system Broker through the group `<name>` instead of the default `agent-peer` group; the name may start only with a lowercase letter or an underscore and may contain only lowercase letters, digits, underscores and hyphens. Valid only with `--system`. |
| `--join-admin-group` | Add the account that ran `sudo` to the admin group without asking. Valid only with `--system`. |
| `--no-admin-group` | Do not ask whether to add the account that ran `sudo` to the admin group. Valid only with `--system`. |

Giving both `--join-admin-group` and `--no-admin-group` is a usage error.

The script is safe to run again: it replaces the binary, restarts the service with it, and does not register the MCP server twice. If the server is already registered with a different command, the script keeps the existing registration; to change it, run `claude mcp remove -s user peer` and run the script again. It never accesses the network; everything it installs comes from the extracted archive.

With `--no-mcp`, the script skips the registration, and you run the registration command in the manual section yourself. When it cannot find `claude`, or runs as root without `sudo` and has no account to register for, it prints the `claude mcp add` command to run.

If the configuration file for the level does not exist, `service install` creates it with every configuration key listed: `~/.agent-peer/config.yaml` for a user-level service, and `/etc/agent-peer/config.yaml` for a system-level one. [Configuration keys]({{< relref "reference/configuration.md" >}}) describes the file.

On macOS, the script clears the quarantine attribute from the binary itself, so a browser download needs no extra step. If the user-level install directory is not on `PATH`, the script says so; add it to `PATH` to run `agent-peer` by name. A user-level service on macOS needs a graphical login session; on a host without one, use `--system` or `--no-service`.

A system-level installation requires the binary's directory and every directory above it to be owned by root and not writable by group or others. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to your account, so the installation is refused; create a root-owned directory with `sudo mkdir -p <dir>` and pass it with `--prefix` instead.

### Install manually

Download, verify and extract the archive as described in the previous section, then install the binary by hand. The commands below run from the directory that contains the extracted `agent-peer-${VERSION}` directory, so run `cd ..` first if you entered it in the previous section.

On macOS, a browser download carries the quarantine attribute, and macOS blocks the binary until it is removed. Clear it from the extracted binary if a browser downloaded the archive:

```sh
VERSION=0.2.0
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer
```

A command-line download with `curl` or `wget` does not set the attribute.

If you install a system-level service, use the `sudo install` step in [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}) instead of the `mv` below.

Move the binary into a directory on your `PATH`:

```sh
VERSION=0.2.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

The printed version matches the version you downloaded.

#### Register the MCP server

Register the server once for your account:

```sh
claude mcp add --scope user peer -- agent-peer mcp
```

The server name must be `peer`. Claude Code shows the tools as `mcp__peer__<tool>`, and you write permission rules with that name. If `agent-peer` is not on your `PATH`, use its absolute path.

Check the registration:

```sh
claude mcp get peer
```

The output shows `Scope: User config`. A session started afterwards lists `peer` in `/mcp`.

#### Start the Broker

Installing with one command or with the archive script starts the service for you. If you installed with `--no-service` or by hand, start the Broker yourself.

Sessions connect to a Broker but never start one. If the host already has a system Broker you can use, skip this step; the session connects to it.

Otherwise, install a user-level service; the install starts it right away, and from then on it starts at login:

```sh
agent-peer service install
```

A user-level install must not run as root, and on macOS it needs a graphical login session; on a host without one, install the system-level service with `agent-peer service install --system`, as root or through an administrator; see [Run a system Broker]({{< relref "guides/system-broker.md" >}}). Alternatively, run the Broker in a terminal outside Claude Code and leave it running:

```sh
agent-peer broker
```

A Broker started inside a session exits with code 6. When no Broker is available, tool calls return an error that says what to do, and the guidance depends on the case. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods. A session that was using the system Broker and loses it is told only to contact the administrator. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. Starting the Broker does not require reconnecting the MCP server. [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}) covers both service forms and uninstalling it.

### Build from source

To build from source you need Go 1.27. Clone the repository and build:

```sh
VERSION=0.2.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

Without `-ldflags`, `agent-peer --version` prints `agent-peer dev`, and `agent-peer update` does not update such a binary. Installing with `go install …@version` is not supported, because `go.mod` uses a `replace` directive that points at a vendored dependency.

The build leaves `agent-peer` in the current directory; follow the manual installation steps above to put it on `PATH`, register the MCP server and start the Broker.

## Send a message on one host

Open two sessions in the same project directory and enable the agent-peer tools in both.

This works without extra setup when both sessions run in the same git repository that has an `origin` address, or under the same project configuration file `.agent-peer.yaml`. The default topic comes from that file in the working directory or an ancestor, or, when there is none, from the workspace's git `origin` address. The origin is normalized to a name such as `github.com/org/repo`, so every clone of the same repository gets the same topic.

When neither condition holds, the sessions join no topic and do not appear in each other's `list_agents` result. In that case, have both sessions call `join_topic` with the same topic name.

In one session, ask the model to call `list_agents`. The result gives your own address and the other session as `name@alias`, for example `frontend@workstation.alice`; on a system Broker the address is `frontend@workstation`. Ask it to call `send_message` with that address and a plain-text message.

The sending session reports success once the message is written into the receiving session's inbox socket. An idle receiving session starts working on the message without user input. The message carries the sender's agent address, and a session can call `send_message` with that address to send a message back.

## Send a message between two hosts

At least one host must be able to reach port 17443 on the other. Before pairing, make sure the Broker is running on both hosts, each with its default configuration.

1. On the host that can be reached, ask the model to call `create_pairing_key` and approve the call, or run `agent-peer pairing-key create` in a terminal.
2. The result gives a one-time key, its expiry time, and this Broker's addresses. The key and an address are shown separately; the addresses are not part of the key. Pass both to the other host through a channel outside agent-peer.
3. On the other host, ask the model to call `connect_broker` with the address from step 2 and the key, and approve the call, or run `agent-peer peer connect <address> --key <key>`.
4. The result shows the accepting Broker's alias and fingerprint. Sessions on both hosts that share a topic can now see each other and send messages.

`create_pairing_key`, `connect_broker` and the other management tools, and their terminal forms, need write permission on the Broker's configuration file. Your own user Broker's file belongs to your account, so this holds. On a system Broker the admin group holds write permission: a member of the group runs the commands on that side directly, and an account that is not in the group asks an administrator to add it to the group or to run the commands on its behalf. An account whose own user-level Broker is running uses `sudo` to reach the system Broker. [The admin group]({{< relref "guides/management-permissions.md" >}}#the-admin-group) covers the group and the permission rules.

The side that calls `connect_broker` or runs `agent-peer peer connect` is the dialer and re-establishes the link after a network drop; the accepting side does not dial. [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}) covers the alternatives, such as an expired key, an alias conflict and an address the other side cannot reach.
