---
title: Getting Started
linkTitle: Getting Started
weight: 2
---

agent-peer connects Claude Code sessions running on different hosts. A session joins a Broker on its own host through MCP tools, and two Brokers paired with a pairing key let sessions that share a topic exchange messages.

## Before you start

agent-peer runs on macOS and Linux, on amd64 and arm64; native Windows is not supported. Claude Code v2.1.224 or later is required. Every Claude Code behavior this site describes is noted with the version it was observed on, and there is no compatibility promise for other versions. See [design choices and caveats]({{< relref "limitations.md" >}}) and the [overview]({{< relref "/docs" >}}) for details.

## Install

There is no formal release yet. Build agent-peer from source; you need Go 1.27.

```sh
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -o agent-peer ./cmd/agent-peer
```

Put the binary in a directory on your `PATH`, or remember its absolute path for the next step.

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

The sending session reports success once the message is written into the receiving session's inbox socket. On Claude Code v2.1.327, an idle receiving session was observed to start working on the message without user input; there is no compatibility promise for other versions. The message carries the sender's agent address, and a session can call `send_message` with that address to send a message back.

## Send a message between two hosts

At least one host must be able to reach port 17443 on the other. Before pairing, make sure the Broker is running on both hosts, each with its default configuration.

1. On the host that can be reached, ask the model to call `create_pairing_key`. Approve the call.
2. The result gives a one-time key, its expiry time, and this Broker's addresses. The key and an address are shown separately; the addresses are not part of the key. Pass both to the other host through a channel outside agent-peer.
3. On the other host, ask the model to call `connect_broker` with the address from step 2 and the key. Approve the call.
4. The result shows the accepting Broker's alias and fingerprint. Sessions on both hosts that share a topic can now see each other and send messages.

`create_pairing_key`, `connect_broker` and the other management tools need write permission on the Broker's configuration file. Your own user Broker's file belongs to your account, so this holds. [Manage a Broker by file permission]({{< relref "guides/management-permissions.md" >}}) covers the system Broker case.

The side that calls `connect_broker` is the dialer and re-establishes the link after a network drop; the accepting side does not dial. [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}) covers the alternatives, such as an expired key, an alias conflict and an address the other side cannot reach.
