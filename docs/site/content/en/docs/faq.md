---
title: FAQ
weight: 8
---

## How do I install agent-peer?

There is no formal release yet. Build the binary from source as described in the [quick start]({{< relref "getting-started.md" >}}), put it on your `PATH`, and register the MCP server with the name `peer`.

## Which platforms are supported?

macOS and Linux, on amd64 and arm64. Native Windows is not supported. See [known limitations]({{< relref "limitations.md" >}}).

## Which Claude Code version do I need?

Claude Code v2.1.224 or later. The behavior this project relies on was observed on v2.1.327, and there is no compatibility promise for other versions. See the [quick start]({{< relref "getting-started.md" >}}).

## Is there a central server?

No. Brokers connect directly, messages are not relayed, and there is no central registry. One of the two hosts must be able to reach the other. See [Links]({{< relref "concepts/links.md" >}}).

## Do I have to start a Broker by hand?

No. The first session that enables the tools starts a user Broker for your account in the background. To start it at login, install it as a service. See [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}).

## Can one account run more than one Broker?

No. An account runs at most one Broker, and starting another one with a different listen address fails. See [Broker]({{< relref "concepts/broker.md" >}}).

## How do two hosts start trusting each other?

One host must be able to reach the other. On the host that can be reached, create a one-time pairing key and give it, with the displayed addresses, to the other host through a channel outside the system. The other host pairs with `connect_broker`, and from then on each side recognizes the other by fingerprint. See [Pairing]({{< relref "concepts/pairing.md" >}}) and [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}).

## What is a topic, and do I need to create one?

A topic is the scope in which agents can see and message each other. Sessions working in the same project usually share a default topic taken from `.agent-peer.yaml` or the git origin address, so there is nothing to create. See [Topics]({{< relref "concepts/topics.md" >}}).

## Can another host see my sessions without pairing?

No. After pairing, a host sees only agents that share a topic with one of its own. A Broker is private by default and is not announced to the connected network unless you make it public. See the [security model]({{< relref "security.md" >}}).

## Is traffic between Brokers encrypted?

Yes. Links use TLS 1.3 with self-signed certificates, and a Broker's identity is its public-key fingerprint. See the [security model]({{< relref "security.md" >}}).

## Can I send files or session context?

No. Messages carry plain text only; files, session history, and context are not transferred. See [known limitations]({{< relref "limitations.md" >}}).

## Does a successful send mean the other agent read the message?

No. Success means the message was written to the target session's inbox socket, which says nothing about whether the model read or processed it. If a delivery request went out but no definite answer came back, the result is "delivery unknown". See [MCP tools]({{< relref "reference/mcp-tools.md" >}}).

## Why did my second identical message disappear?

An analysis of the v2.1.327 program says the receiving Claude Code drops a message that repeats the previous one from the same sender within 30 seconds; this has not been confirmed by experiment. The rules belong to Claude Code and may change. See the [security model]({{< relref "security.md" >}}) and [known limitations]({{< relref "limitations.md" >}}).

## Who can change the Broker's configuration?

An account with write permission on the Broker's configuration file can change it, create one-time pairing keys, pair, and unpair. Read permission allows reading the configuration and the audit log. See [Management permissions]({{< relref "guides/management-permissions.md" >}}).

## What happens if the port is already in use?

The Broker fails to start and reports that the address is unavailable. It never chooses another address or port; free the port or change the listen address. See [Configuration]({{< relref "reference/configuration.md" >}}).

## How do I see logs and audit records?

Use `agent-peer status` for agents, topics, peers, and links, and `agent-peer audit` for the audit log. A Broker started automatically by the MCP server has no run log; run `agent-peer broker` in a terminal or install it as a service to get one. See [Command line]({{< relref "reference/cli.md" >}}).

## Can a headless session receive messages?

This has not yet been verified in a real session. Whether a headless session receives messages depends on its inbound controls; see [known limitations]({{< relref "limitations.md" >}}) for the open questions about inbound controls and skip-permission mode.
