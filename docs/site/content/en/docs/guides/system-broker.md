---
title: Run a system Broker
linkTitle: System Broker
weight: 5
---

A system Broker runs as a service with root privileges and serves agents of several accounts on one host. A user Broker serves one account. A host runs at most one system Broker.

## Install and enable it

Install the binary in a root-owned directory and install the service as root. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the commands. The system service starts at boot.

The system Broker takes root's account lock, so a Broker already running for root, such as one a root session started, makes the service exit and retry every 5 seconds. Exit root's sessions that have the peer tools enabled, or disable `peer` in them with `/mcp`, then stop that Broker and start the service; a session left open starts a Broker again about 250 ms after it stops. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the full order.

The system Broker uses `/etc/agent-peer/config.yaml`, `/var/log/agent-peer/audit.log` and `/var/lib/agent-peer`. The install creates the configuration file with owner root and mode 0600 if it does not exist.

## Let accounts use it

Each account registers the MCP server for itself, for example with `claude mcp add --scope user peer -- agent-peer mcp`. Registration is per account, not per host.

A session reaches the system Broker through the local socket `/run/agent-peer/broker.sock` on Linux, or `/var/run/agent-peer/broker.sock` on macOS. Two settings decide which accounts can connect:

| Key | Default | Effect |
|---|---|---|
| `broker.local_socket_group` | empty | Group of the socket. Empty keeps the runtime directory's group. The group must exist. |
| `broker.local_socket_mode` | `0666` | Permission bits of the socket. |

To limit use to a group, set the group and a mode that gives that group access, for example `broker.local_socket_group: agent-peer` with `broker.local_socket_mode: 0660`. Accounts outside the group cannot connect; their sessions use or start their own user Broker.

## Which Broker a session uses

A session's MCP server process selects a Broker in this order:

1. The socket named by `AGENT_PEER_SOCKET`, if the session's environment sets it.
2. The user Broker of this account, if one is running.
3. The system Broker, if this account may use it.

If none is available, the MCP server process starts a user Broker. A host where every account should use the system Broker therefore must not have a user Broker running for those accounts.

An account that may use the system Broker but has its own user Broker running registers on the user Broker. Point `AGENT_PEER_SOCKET` at the system socket to choose the system Broker for that session.

## One identity for several accounts

All agents served by one system Broker share its fingerprint, alias and peers. Agents from different accounts that join the same topic see each other and can exchange messages. Pairing the system Broker once therefore connects every served account to the other side, and another host needs only that one pairing to reach all of them.

Management of the Broker is decided by the permissions on `/etc/agent-peer/config.yaml`, not by the account that runs the service. [Manage a Broker by file permission]({{< relref "management-permissions.md" >}}) explains how to grant read or write access.
