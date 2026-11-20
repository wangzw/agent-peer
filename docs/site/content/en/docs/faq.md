---
title: FAQ
weight: 8
---

## How do I install agent-peer?

Download the archive for your platform from [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest), check it against `checksums.txt`, and put the binary on your `PATH`. Then follow the [quick start]({{< relref "getting-started.md" >}}) to register the MCP server with the name `peer`.

## Where is the configuration file?

A user Broker reads `~/.agent-peer/config.yaml` and a system Broker reads `/etc/agent-peer/config.yaml`. `agent-peer service install` generates the file for its level when it does not exist. Without a file, every key takes its default and the Broker runs normally. See [Configuration keys]({{< relref "reference/configuration.md" >}}) for the generated file and how to change it.

## Which platforms are supported?

macOS and Linux, on amd64 and arm64. Native Windows is not supported. See [design choices and caveats]({{< relref "limitations.md" >}}).

## Which Claude Code version do I need?

Claude Code v2.1.224 or later. See [design choices and caveats]({{< relref "limitations.md" >}}) for how agent-peer depends on Claude Code.

## Is there a central server?

No. Brokers connect directly, messages are not relayed, and there is no central registry. One of the two hosts must be able to reach the other. See [Links]({{< relref "concepts/links.md" >}}).

## Do I have to start a Broker by hand?

A session never starts a Broker. If the host already has a system Broker you can use, install nothing; the session connects to it. Otherwise, start one yourself: install a user-level service with `agent-peer service install`, which starts it immediately and starts it at login from then on, or run `agent-peer broker` in a terminal outside Claude Code. A Broker started inside a session exits with code 6. See [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}).

## What happens when no Broker is running?

Tool calls return an error that says what to do, and the guidance depends on the case. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods. A session that was registered on the system Broker and loses it is told only to contact the administrator. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. Starting the Broker does not require reconnecting the MCP server. Terminal commands that need a Broker, such as `agent-peer peer list`, behave the same way: they look for a running Broker and never start one. See [Command line]({{< relref "reference/cli.md" >}}).

## Can one account run more than one Broker?

No. An account runs at most one Broker, and starting another one with a different listen address fails. See [Broker]({{< relref "concepts/broker.md" >}}).

## Why did agent addresses change after upgrading?

Starting with 0.1.1, the default alias no longer contains the listen port: on a host named `workstation`, a system Broker reports `workstation` instead of the old `workstation-17443`, and the user Broker of account alice reports `workstation.alice`. A Broker that you upgraded from 0.1.0 and never gave `broker.alias` therefore usually reports a different alias, and the agent addresses your peers see change with it, unless a peer already uses a local alias for it; a session that saved an old address has to use the new one. A peer handles a collision with an alias it already knows by giving the renamed Broker a local alias of its own, so an existing address does not start pointing to a different Broker. To keep the old alias, set `broker.alias` in the configuration file, for example back to `workstation-17443`. This is the same however you upgraded: with the online install command, with `install.sh` from a release archive, with `agent-peer update`, or by replacing the binary by hand.

## How do I update agent-peer?

Run `agent-peer update` in a terminal; for a system-level installation, run `sudo agent-peer update`. The command asks GitHub for the latest release, verifies the download, replaces the binary, and restarts the installed service; sessions that are already open must reconnect with `/mcp` or be restarted. See [Update agent-peer]({{< relref "guides/update.md" >}}).

## Can I turn off update checks?

Yes. Run `agent-peer config set update.check false`; the `Update` line and the session notice disappear immediately, and no further checks are made. See [Update agent-peer]({{< relref "guides/update.md" >}}) for how checks and updates work, or the [security model]({{< relref "security.md" >}}) for what a check sends to GitHub.

## How do two hosts start trusting each other?

One host must be able to reach the other. On the host that can be reached, create a one-time pairing key with `create_pairing_key` or `agent-peer pairing-key create` and give it, with the displayed addresses, to the other host through a channel outside the system. The other host pairs with `connect_broker` or `agent-peer peer connect <address> --key <key>`, using that one-time key or a pre-provisioned static key configured on the accepting host, and from then on each side recognizes the other by fingerprint. See [Pairing]({{< relref "concepts/pairing.md" >}}) and [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}).

## What is a topic, and do I need to create one?

A topic is the scope in which agents can see and message each other. Sessions working in the same project usually share a default topic taken from `.agent-peer.yaml` or the git origin address, so there is nothing to create. See [Topics]({{< relref "concepts/topics.md" >}}).

## Can another host see my sessions without pairing?

No. After pairing, a host sees only agents that share a topic with one of its own. A Broker is private by default and is not announced to the connected network unless you make it public. See the [security model]({{< relref "security.md" >}}).

## Is traffic between Brokers encrypted?

Yes. Links use TLS 1.3 with self-signed certificates, and a Broker's identity is its public-key fingerprint. See the [security model]({{< relref "security.md" >}}).

## Can I send files or session context?

No. Messages carry plain text only; files, session history, and context are not transferred. See [design choices and caveats]({{< relref "limitations.md" >}}).

## Does a successful send mean the other agent read the message?

No. Success means the message was written to the target session's inbox socket, which says nothing about whether the model read or processed it. If a delivery request went out but no definite answer came back, the result is "delivery unknown". See [MCP tools]({{< relref "reference/mcp-tools.md" >}}).

## Why did my second identical message disappear?

The receiving Claude Code drops a message from the same agent to the same session whose content repeats the previous one within 30 seconds; this rule belongs to Claude Code and may change. The receiving Broker also drops a copy that a retry within one send produces, but two separate sends are not deduplicated. See the [security model]({{< relref "security.md" >}}).

## Who can change the Broker's configuration?

An account with write permission on the Broker's configuration file can change it with `set_config` or `agent-peer config set`, create one-time pairing keys with `create_pairing_key` or `agent-peer pairing-key create`, and pair or unpair with `connect_broker` and `disconnect_broker`, or `agent-peer peer connect` and `agent-peer peer disconnect`. Read permission allows reading the configuration and the audit log. The terminal commands judge permission by the account running them; when the administrator runs them with `sudo`, a system Broker authorizes them against root's permissions. See [Management permissions]({{< relref "guides/management-permissions.md" >}}).

## What happens if the port is already in use?

The Broker fails to start and reports that the address is unavailable. It never chooses another address or port; free the port or change the listen address. See [Configuration keys]({{< relref "reference/configuration.md" >}}).

## How do I see logs and audit records?

Use `agent-peer status` for agents, topics, peers, and links, and `agent-peer audit` for the audit log. Install the Broker as a service and the service manager keeps its run log: on Linux, `journalctl --user -u agent-peer.service`, or `journalctl -u agent-peer.service` for a system-level service; on macOS, `~/Library/Logs/agent-peer/broker.log`, or `/var/log/agent-peer/broker.log` for a system-level service. A Broker run in a terminal writes its log to that terminal. See the Logs section of [Files and paths]({{< relref "reference/files.md" >}}).

## Can a headless session receive messages?

A headless session that also skips permission prompts cannot. The message is held first and then dropped because the sender declares no permission category, while the sending side has already returned success. See [design choices and caveats]({{< relref "limitations.md" >}}) for the caveats that apply.
