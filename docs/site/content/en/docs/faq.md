---
title: FAQ
weight: 8
---

## How do I install agent-peer?

The command installs the latest release and registers the MCP server for your account; it asks for the level when it can:

```sh
curl -fsSL https://github.com/wangzw/peers/releases/latest/download/install.sh | sh
```

The release archive also contains an `install.sh`; after extracting it, run `sh ./install.sh`, which asks the same question. You can also install the binary by hand. The [Install agent-peer]({{< relref "guides/install.md" >}}) guide covers the level question and every way to install, including system-level installation.

If the host already has a system Broker you can use, register the MCP server only instead of installing a user-level Broker; [Run a system Broker]({{< relref "guides/system-broker.md" >}}) gives the command.

## How do I uninstall agent-peer?

Run `agent-peer uninstall` to remove this account's installation; a system-level installation is removed with `sudo agent-peer uninstall`. The command asks whether to delete configuration, identity, audit and service logs, and keeps them unless you answer `y`. [Uninstall agent-peer]({{< relref "guides/uninstall.md" >}}) covers the levels, the questions and removing only the service.

## Where is the configuration file?

A user Broker reads `~/.agent-peer/config.yaml` and a system Broker reads `/etc/agent-peer/config.yaml`. `agent-peer service install` generates the file for its level when it does not exist. Without a file, every key takes its default and the Broker runs normally. See [Configuration keys]({{< relref "reference/configuration.md" >}}) for the generated file and how to change it.

## Which platforms are supported?

macOS and Linux, on amd64 and arm64. Native Windows is not supported. See [known limitations]({{< relref "limitations.md" >}}).

## Which Claude Code version do I need?

Claude Code v2.1.224 or later. See [known limitations]({{< relref "limitations.md" >}}) for how agent-peer depends on Claude Code.

## Is there a central server?

No. Brokers connect directly, messages are not relayed, and there is no central registry. One of the two hosts must be able to reach the other. See [Links]({{< relref "concepts/links.md" >}}).

## Do I have to start a Broker by hand?

A session never starts a Broker. If the host already has a system Broker you can use, register the MCP server only; the session connects to it. Otherwise, start one yourself: install a user-level service with `agent-peer service install`, which starts it immediately and starts it at login from then on, or run `agent-peer broker` in a terminal outside Claude Code. The install script already installs and starts the user-level service, so an installation done with it needs no further step. A Broker started inside a session exits with code 6. See [Run the Broker as a service]({{< relref "guides/run-as-service.md" >}}).

## What happens when no Broker is running?

Tool calls return an error that says what to do, and the guidance depends on the case; the MCP server process does not start a Broker. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods, `agent-peer service install` and `agent-peer broker` in a terminal outside Claude Code. A session that was registered on the system Broker and loses it is told only to contact the administrator. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. Starting the Broker does not require reconnecting the MCP server. Terminal commands that need a Broker, such as `agent-peer peer list`, behave the same way: they look for a running Broker and never start one. See [Command line]({{< relref "reference/cli.md" >}}).

## Can one account run more than one Broker?

No. An account runs at most one Broker, and starting another one with a different listen address fails. See [Broker]({{< relref "concepts/broker.md" >}}).

## Why did agent addresses change after upgrading?

Starting with v0.2.0, the default alias no longer contains the listen port: on a host named `workstation`, a system Broker reports `workstation` instead of the old `workstation-17443`, and the user Broker of account alice reports `workstation.alice`. A Broker that you upgraded from v0.1.0 and never gave `broker.alias` therefore usually reports a different alias, and the agent addresses your peers see change with it, unless a peer already uses a local alias for it; a session that saved an old address has to use the new one. A peer handles a collision with an alias it already knows by giving the renamed Broker a local alias of its own, so an existing address does not start pointing to a different Broker. To keep the old alias, read it before upgrading, either by saying “Read the broker.alias configuration key.” (the model calls `get_config`) or from the part after `@` in the agent addresses that `agent-peer status` prints, and set `broker.alias` in the configuration file to it after upgrading, for example back to `workstation-17443`. With the default port the old alias is the full hostname followed by `-17443`, shortened to 24 characters. This is the same however you upgraded: with the online install command, with `install.sh` from a release archive, with `agent-peer update`, or by replacing the binary by hand.

## How do I update agent-peer?

Run `agent-peer update` in a terminal, or say “Update the Broker.” in a session and approve the call (the model calls `update_broker`). A system-level installation is updated without `sudo` by a member of the admin group; an account outside the group uses `sudo agent-peer update`, or joins the group first. When the same account also runs a user-level Broker, `sudo agent-peer update` reaches the system-level Broker. The update verifies the download and replaces the binary; a Broker that runs as a service is restarted with the new version, and one that is not running, or a user-level Broker that does not run under a service manager, takes it up at its next start. Sessions that are already open must reconnect with `/mcp` or be restarted. See [Update agent-peer]({{< relref "guides/update.md" >}}).

## Can I turn off update checks?

Yes. Run `agent-peer config set update.check false`; the `Update` line and the session notice disappear immediately, and no further checks are made. See [Update agent-peer]({{< relref "guides/update.md" >}}) for how checks and updates work, or the [security model]({{< relref "security.md" >}}) for what a check sends to GitHub.

## How do two hosts start trusting each other?

One host must be able to reach the other. On the host that can be reached, say “Create a pairing key.” and approve the call (the model calls `create_pairing_key`) or run `agent-peer pairing-key create`, and give it, with the displayed addresses, to the other host through a channel outside the system. The other host pairs by saying “Connect to the Broker at `<address>` with the key `<key>`.” and approving the call (the model calls `connect_broker`), or by running `agent-peer peer connect <address> --key <key>`, using that one-time key or a pre-provisioned static key configured on the accepting host, and from then on each side recognizes the other by fingerprint. See [Pairing]({{< relref "concepts/pairing.md" >}}) and [Pair two hosts]({{< relref "guides/pair-hosts.md" >}}).

## What is a topic, and do I need to create one?

A topic is the scope in which agents can see and message each other. Sessions working in the same project usually share a default topic taken from `.agent-peer.yaml` or the git origin address, so there is nothing to create. See [Topics]({{< relref "concepts/topics.md" >}}).

## Can another host see my sessions without pairing?

No. After pairing, a host sees only agents that share a topic with one of its own. A Broker is private by default and is not announced to the connected network unless you make it public. See the [security model]({{< relref "security.md" >}}).

## Is traffic between Brokers encrypted?

Yes. Links use TLS 1.3 with self-signed certificates, and a Broker's identity is its public-key fingerprint. See the [security model]({{< relref "security.md" >}}).

## Can I send files or session context?

No. Messages carry plain text only; files, session history, and context are not transferred. See [known limitations]({{< relref "limitations.md" >}}).

## Does a successful send mean the other agent read the message?

No. Success means the message was written to the target session's inbox socket, which says nothing about whether the model read or processed it. If a delivery request went out but no definite answer came back, the call returns an error that begins with `Delivery unknown`. See [MCP tools]({{< relref "reference/mcp-tools.md" >}}).

## Why did my second identical message disappear?

The receiving Claude Code drops a message from the same agent to the same session whose content repeats the previous one within 30 seconds; this rule belongs to Claude Code and may change. The receiving Broker also drops a copy that a retry within one send produces, but two separate sends are not deduplicated. See the [security model]({{< relref "security.md" >}}).

## Who can change the Broker's configuration?

An account with write permission on the Broker's configuration file can change it with `set_config` or `agent-peer config set`, create one-time pairing keys with `create_pairing_key` or `agent-peer pairing-key create`, and pair or unpair with `connect_broker` and `disconnect_broker`, or `agent-peer peer connect` and `agent-peer peer disconnect`. Read permission allows reading the configuration and the audit log. The terminal commands judge permission by the account running them. A member of the admin group manages a system-level Broker without `sudo` when the account runs no user-level Broker of its own; an administrator who runs a command with `sudo` is authorized against root's permissions. See [Management permissions]({{< relref "guides/management-permissions.md" >}}), especially [The admin group]({{< relref "guides/management-permissions.md" >}}#the-admin-group).

## What happens if the port is already in use?

A Broker started in the foreground fails to start. A new install or a reinstall fails when another process holds the address, says so, and registers no service. A Broker that is already installed as a service and finds the port taken when it starts enters local-only degraded run: agents on this host still work, creating a pairing key fails, and peers cannot connect to it. The Broker never chooses another address or port; free the port or change the listen address, and it recovers without a restart. See [Configuration keys]({{< relref "reference/configuration.md" >}}) and [agent-peer service install]({{< relref "reference/cli.md" >}}#agent-peer-service-install).

## How do I see logs and audit records?

Use `agent-peer status` for agents, topics, peers, and links, and `agent-peer audit` for the audit log. When the Broker runs degraded, `agent-peer status` shows the reason and the fix. Install the Broker as a service and the service manager keeps its run log: on Linux, `journalctl --user -u agent-peer.service`, or `journalctl -u agent-peer.service` for a system-level service; on macOS, `~/Library/Logs/agent-peer/broker.log`, or `/var/log/agent-peer/broker.log` for a system-level service. A Broker run in a terminal writes its log to that terminal. See the Logs section of [Files and paths]({{< relref "reference/files.md" >}}).

## Can a headless session receive messages?

A headless session that also skips permission prompts cannot. The message is held first and then dropped because the sender declares no permission category, while the sending side has already returned success. See [known limitations]({{< relref "limitations.md" >}}).
