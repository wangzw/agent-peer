---
title: Getting Started
linkTitle: Getting Started
weight: 2
---

agent-peer connects Claude Code sessions running on different hosts. A session joins a Broker on its own host through MCP tools, and two Brokers paired with a pairing key let sessions that share a topic exchange messages.

## Before you start

agent-peer runs on macOS and Linux, on amd64 and arm64; native Windows is not supported. Claude Code v2.1.224 or later is required.

## Install

agent-peer is distributed as a `tar.gz` archive on [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest). Download the archive for your platform and architecture together with `checksums.txt` from the same release:

```sh
VERSION=0.1.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

Replace `<os>_<arch>` with `darwin_amd64`, `darwin_arm64`, `linux_amd64` or `linux_arm64`. The checksum and extraction examples below use the arm64 archive; replace `arm64` with `amd64` on an amd64 host.

Check the archive against `checksums.txt` before using it. On Linux:

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

On macOS, use `shasum` instead:

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

Create a directory and extract the archive into it: the archive has no top-level directory, so without one its files, including `LICENSE`, `README.md` and `third_party/`, land directly in the current directory. The macOS binaries are not signed or notarized, so on macOS, if a browser downloaded the archive, clear the quarantine attribute from the extracted binary before running it:

```sh
VERSION=0.1.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer  # only if a browser downloaded the archive
```

On Linux, extract the archive without the `xattr` step:

```sh
VERSION=0.1.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_linux_arm64.tar.gz -C agent-peer-${VERSION}
```

If you use only a user-level Broker, a per-user directory such as `~/.local/bin` on your `PATH` is enough. If you install a system-level service, use the `sudo install` step in [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}) instead of the `mv` below:

```sh
VERSION=0.1.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

The printed version matches the version you downloaded.

To build from source you need Go 1.27. Clone the repository and build:

```sh
VERSION=0.1.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

Without `-ldflags`, `agent-peer --version` prints `agent-peer dev`. Installing with `go install …@version` is not supported, because `go.mod` uses a `replace` directive that points at a vendored dependency.

## Register the MCP server

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

## Start the Broker

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

## Send a message on one host

Open two sessions in the same project directory and enable the agent-peer tools in both.

This works without extra setup when both sessions run in the same git repository that has an `origin` address, or under the same project configuration file `.agent-peer.yaml`. The default topic comes from that file in the working directory or an ancestor, or, when there is none, from the workspace's git `origin` address. The origin is normalized to a name such as `github.com/org/repo`, so every clone of the same repository gets the same topic.

When neither condition holds, the sessions join no topic and do not appear in each other's `list_agents` result. In that case, have both sessions call `join_topic` with the same topic name.

In one session, ask the model to call `list_agents`. The result gives your own address and the other session as `name@alias`, for example `frontend@workstation-17443`. Ask it to call `send_message` with that address and a plain-text message.

The sending session reports success once the message is written into the receiving session's inbox socket. An idle receiving session starts working on the message without user input. The message carries the sender's agent address, and a session can call `send_message` with that address to send a message back.

## Send a message between two hosts

At least one host must be able to reach port 17443 on the other. Before pairing, make sure the Broker is running on both hosts, each with its default configuration.

1. On the host that can be reached, ask the model to call `create_pairing_key` and approve the call, or run `agent-peer pairing-key create` in a terminal.
2. The result gives a one-time key, its expiry time, and this Broker's addresses. The key and an address are shown separately; the addresses are not part of the key. Pass both to the other host through a channel outside agent-peer.
3. On the other host, ask the model to call `connect_broker` with the address from step 2 and the key, and approve the call, or run `agent-peer peer connect <address> --key <key>`.
4. The result shows the accepting Broker's alias and fingerprint. Sessions on both hosts that share a topic can now see each other and send messages.

`create_pairing_key`, `connect_broker` and the other management tools, and their terminal forms, need write permission on the Broker's configuration file. Your own user Broker's file belongs to your account, so this holds. On a system Broker an ordinary account has no write permission by default. Either host may run a system Broker, and on that side both creating the key and connecting need write permission, so ask the administrator to run `sudo agent-peer pairing-key create` and `sudo agent-peer peer connect <address> --key <key>`. The administrator can also grant write access instead; [Manage a Broker by file permission]({{< relref "guides/management-permissions.md" >}}) covers the permission rules.

The side that calls `connect_broker` or runs `agent-peer peer connect` is the dialer and re-establishes the link after a network drop; the accepting side does not dial. [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}) covers the alternatives, such as an expired key, an alias conflict and an address the other side cannot reach.
