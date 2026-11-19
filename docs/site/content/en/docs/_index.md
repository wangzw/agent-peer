---
title: Overview
weight: 1
---

## What it solves

Claude Code's built-in cross-session messaging reaches sessions on one host. agent-peer extends the same idea across hosts: sessions on different hosts discover each other and exchange plain-text messages.

## How it works

Each host runs a Broker, a daemon that registers the agent sessions on that host and keeps its links to other Brokers. A session enables the agent-peer tools and registers with its local Broker, which makes it an agent. Two users pair their Brokers with a pairing key. After that, agents on both sides that share at least one topic can see each other and send messages. A message that arrives for a session is written into that session's inbox socket. On Claude Code v2.1.327, an idle session was observed to start working on it without user input; there is no compatibility promise for other versions.

```mermaid
flowchart LR
  subgraph hostA["Host A"]
    sessionA1["Claude Code session"]
    sessionA2["Claude Code session"]
    brokerA["Broker"]
    sessionA1 --- brokerA
    sessionA2 --- brokerA
  end
  subgraph hostB["Host B"]
    sessionB1["Claude Code session"]
    brokerB["Broker"]
    sessionB1 --- brokerB
  end
  brokerA <-->|paired link| brokerB
```

Sessions on the same host can also use the tools to message each other. The path is the same, with both sessions registered on one Broker.

## Platform requirements

agent-peer runs on macOS and Linux, on amd64 and arm64. Native Windows is not supported.

Claude Code v2.1.224 or later is required. The Claude Code behavior this project relies on was observed on v2.1.327; there is no compatibility promise for other versions.

To learn the vocabulary, continue with [Concepts]({{< relref "/docs/concepts" >}}). To set up a host, see the [Guides]({{< relref "/docs/guides" >}}); the [Reference]({{< relref "/docs/reference" >}}) lists tools, commands and configuration keys.
