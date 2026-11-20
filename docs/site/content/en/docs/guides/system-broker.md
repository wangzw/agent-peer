---
title: Run a system Broker
linkTitle: System Broker
weight: 5
---

A system Broker runs as a service with root privileges and serves agents of several accounts on one host. A user Broker serves one account. A host runs at most one system Broker.

## Install and enable it

Install the binary in a directory that root owns and that group and others cannot write, then install the service as root; [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the manual commands. From an extracted release archive, `sudo sh ./install.sh --system` does both in one step; without an archive, the online system-level command does the same:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --system
```

Run it from the account whose Claude Code should use the system Broker: the install registers the MCP server only for the account that invoked `sudo`. It starts the Broker immediately, and it starts at boot from then on.

The system Broker takes root's account lock. If a user Broker is already running as root, the system-level install refuses and asks you to stop it first.

The system Broker uses `/etc/agent-peer/config.yaml`, `/var/log/agent-peer/audit.log` and `/var/lib/agent-peer`. If the configuration file does not exist, the install generates it as a template that lists every configuration key, with owner root, the default group `agent-peer` and mode `0660`; an existing file is left unchanged. Members of that group manage the Broker without `sudo` when the account runs no user-level Broker of its own; [Manage a Broker by file permission]({{< relref "management-permissions.md" >}}#the-admin-group) describes the group and its limits.

## Let accounts use it

Each account registers the MCP server for itself; registration is per account, not per host. The system-level install registers it for the account that invoked `sudo`. Other accounts need only the registration, with the installed binary's absolute path:

```sh
claude mcp add --scope user peer -- /usr/local/bin/agent-peer mcp
```

Do not run the user-level install on these accounts: it installs a user-level service, and the account's sessions then use its user Broker instead of the system Broker. If you already did, remove the user-level service with `agent-peer service uninstall`.

A session reaches the system Broker through the local socket `/run/agent-peer/broker.sock` on Linux, or `/var/run/agent-peer/broker.sock` on macOS. Two settings decide which accounts can connect:

| Key | Default | Effect |
|---|---|---|
| `broker.local_socket_group` | empty | Group of the socket. Empty keeps the runtime directory's group. The group must exist. |
| `broker.local_socket_mode` | `0666` | Permission bits of the socket. |

To limit use to a group, set the group and a mode that gives that group access, for example `broker.local_socket_group: agent-peer-users` with `broker.local_socket_mode: 0660`. Use a group other than the admin group, the one that owns the configuration file and can manage the Broker: membership there also grants management, and being allowed to connect and being allowed to manage are separate. Accounts outside the group cannot connect; they need to start their own user Broker, as a service or in a terminal. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) gives the commands.

## Which Broker a session or command uses

A session's MCP server process selects a Broker in this order:

1. The user Broker of this account, if one is running.
2. The system Broker, if this account may use it.

If none is available, tool calls return an error that says what to do; the MCP server process does not start a Broker. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods. A session that was registered on the system Broker and loses it is told only to contact the administrator, with no suggestion to start a user Broker. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. A host where every account should use the system Broker therefore needs no user Broker for those accounts; installing a user-level service on such an account moves its sessions off the system Broker.

An account that may use the system Broker but has its own user Broker running registers on the user Broker. To have its sessions use the system Broker instead, do not run a user-level Broker on that account.

The terminal commands `agent-peer config`, `agent-peer pairing-key` and `agent-peer peer`, like `agent-peer status`, choose a Broker in the same order, look only for a Broker that is running, and never start one. If this account has a user Broker running, use `sudo` to manage the system Broker; root selects the system Broker directly.

## One identity for several accounts

All agents served by one system Broker share its fingerprint, alias and peers. Agents from different accounts that join the same topic see each other and can exchange messages. Pairing the system Broker once therefore connects every served account to the other side, and another host needs only that one pairing to reach all of them. The system Broker's default alias is the first segment of the hostname and carries no account name, so the agents of every account it serves have addresses of the form `name@workstation`. When account alice starts its own user Broker, the addresses of its sessions become `name@workstation.alice`, because those sessions now register with a different Broker.

Management of the Broker is decided by the permissions on `/etc/agent-peer/config.yaml`, not by the account that runs the service. There are two ways to manage it: membership in the group that owns the file, and the terminal commands under `sudo`, which the Broker checks as root. [Manage a Broker by file permission]({{< relref "management-permissions.md" >}}#the-admin-group) describes the group and its limits.
