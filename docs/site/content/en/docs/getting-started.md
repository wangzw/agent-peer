---
title: Getting Started
linkTitle: Getting Started
weight: 2
---

agent-peer connects Claude Code sessions running on different hosts. A session joins a Broker on its own host through MCP tools, and two Brokers paired with a pairing key let sessions that share a topic exchange messages.

## Before you start

agent-peer runs on macOS and Linux, on amd64 and arm64; native Windows is not supported. Claude Code v2.1.224 or later is required.

## Install

Install agent-peer with one command:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

The command downloads the release for this host, verifies it, installs the binary, installs the Broker as a service and registers the MCP server for your account. Reopen any Claude Code session that is already open so that it loads the new server, and check the registration with `claude mcp get peer`.

For a system-level installation, run the same command with `sudo` and `--system`; do not run the user-level command with `sudo`:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --system
```

[Install agent-peer]({{< relref "guides/install.md" >}}) covers a particular version, the release archive, the script's options, manual installation and building from source. [Update agent-peer]({{< relref "guides/update.md" >}}) covers keeping it up to date.

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

`create_pairing_key`, `connect_broker` and the other management tools, and their terminal forms, need write permission on the Broker's configuration file. Your own user Broker's file belongs to your account; for a system Broker, [the admin group]({{< relref "guides/management-permissions.md" >}}#the-admin-group) covers who holds that permission.

The side that calls `connect_broker` or runs `agent-peer peer connect` is the dialer and re-establishes the link after a network drop; the accepting side does not dial. [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}) covers the alternatives, such as an expired key, an alias conflict and an address the other side cannot reach.
