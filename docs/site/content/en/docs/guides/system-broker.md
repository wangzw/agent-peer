---
title: Run a system Broker
linkTitle: System Broker
weight: 5
---

A system Broker runs as a service with root privileges and serves agents of several accounts on one host. A user Broker serves one account. A host runs at most one system Broker.

## Install and enable it

Install the binary in a directory that root owns and that group and others cannot write, then install the service as root. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the commands. The install starts the Broker immediately, and it starts at boot from then on.

The system Broker takes root's account lock. If a user Broker is already running as root, the system-level install refuses and asks you to stop it first.

The system Broker uses `/etc/agent-peer/config.yaml`, `/var/log/agent-peer/audit.log` and `/var/lib/agent-peer`. The install creates the configuration file with owner root and mode 0600 if it does not exist.

## Let accounts use it

Each account registers the MCP server for itself, for example with `claude mcp add --scope user peer -- agent-peer mcp`. Registration is per account, not per host.

A session reaches the system Broker through the local socket `/run/agent-peer/broker.sock` on Linux, or `/var/run/agent-peer/broker.sock` on macOS. Two settings decide which accounts can connect:

| Key | Default | Effect |
|---|---|---|
| `broker.local_socket_group` | empty | Group of the socket. Empty keeps the runtime directory's group. The group must exist. |
| `broker.local_socket_mode` | `0666` | Permission bits of the socket. |

To limit use to a group, set the group and a mode that gives that group access, for example `broker.local_socket_group: agent-peer` with `broker.local_socket_mode: 0660`. Accounts outside the group cannot connect; they need to start their own user Broker, as a service or in a terminal. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the commands.

## Which Broker a session uses

A session's MCP server process selects a Broker in this order:

1. The socket named by `AGENT_PEER_SOCKET`, if the session's environment sets it.
2. The user Broker of this account, if one is running.
3. The system Broker, if this account may use it.

If none is available, tool calls return an error that says what to do; the MCP server process does not start a Broker. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods. A session that was registered on the system Broker and loses it is told only to contact the administrator, with no suggestion to start a user Broker. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. A host where every account should use the system Broker therefore needs no user Broker for those accounts; installing a user-level service on such an account moves its sessions off the system Broker.

An account that may use the system Broker but has its own user Broker running registers on the user Broker. Point `AGENT_PEER_SOCKET` at the system socket to choose the system Broker for that session.

The terminal commands `agent-peer config`, `agent-peer pairing-key` and `agent-peer peer`, like `agent-peer status`, choose a Broker in the same order, look only for a Broker that is running, and never start one. If this account has a user Broker running, point `AGENT_PEER_SOCKET` at the system socket or run the command with `sudo` to manage the system Broker.

## One identity for several accounts

All agents served by one system Broker share its fingerprint, alias and peers. Agents from different accounts that join the same topic see each other and can exchange messages. Pairing the system Broker once therefore connects every served account to the other side, and another host needs only that one pairing to reach all of them. The system Broker's default alias is the first segment of the hostname and carries no account name, so the agents of every account it serves have addresses of the form `name@workstation`. When account alice starts its own user Broker, the addresses of its sessions become `name@workstation.alice`, because those sessions now register with a different Broker.

Management of the Broker is decided by the permissions on `/etc/agent-peer/config.yaml`, not by the account that runs the service. [Manage a Broker by file permission]({{< relref "management-permissions.md" >}}) explains how to grant read or write access. An administrator can also manage the Broker with the terminal commands under `sudo`, which the Broker checks as root; the file's default 0600 mode already lets root manage it.
