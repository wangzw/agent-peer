---
title: Manage a Broker by file permission
linkTitle: Management permissions
weight: 6
---

The tools and the terminal commands can change a Broker only as far as the calling account could change the Broker's configuration file by hand. The Broker takes the account from the process credentials the operating system reports for the local socket; it does not rely on what the caller says.

## The rule

| Operation | Required access to the configuration file |
|---|---|
| `list_agents`, `list_topics`, `join_topic`, `leave_topic`, `send_message` | none |
| `list_brokers`, `agent-peer peer list` | none |
| `get_config`, `agent-peer config get`, full `agent-peer status` output, `agent-peer audit` | read |
| `set_config`, `agent-peer config set` | write |
| `create_pairing_key`, `agent-peer pairing-key create` | write |
| `connect_broker`, `agent-peer peer connect` | write |
| `disconnect_broker`, `agent-peer peer disconnect` | write |

A user Broker's configuration file belongs to the account that runs it, so these checks pass for that account without any setup. On a system Broker, the file is `/etc/agent-peer/config.yaml`, created with owner root and mode 0600. Read access also lets an account read the full status and the audit log; write access lets it change settings and manage pairing. Both levels are ordinary file permissions, not a separate list of administrators.

An administrator who runs the terminal commands with `sudo` is checked as root, so with the file at its default mode 0600, root can manage the Broker without granting anything. An ordinary account has no write access to a system Broker by default and cannot create a pairing key from a session; in that case the administrator creates one with `sudo agent-peer pairing-key create`.

The check identifies an operating-system account, not a person or a model. Any process running under the same account can make the same calls, whether through the tools or from the terminal.

## Grant access on a system Broker

Create a group, add the accounts, and give the group access to the configuration file:

```sh
sudo groupadd agent-peer-admins
sudo usermod -aG agent-peer-admins "$USER"
sudo chgrp agent-peer-admins /etc/agent-peer/config.yaml
sudo chmod 0640 /etc/agent-peer/config.yaml
```

Use `0640` for read access and `0660` for write access. The Broker reads the account's groups from the account database on each check, so a new membership applies to the next call; no session restart is needed.

Being allowed to use the Broker and being allowed to manage it are separate. The settings in [Run a system Broker]({{< relref "system-broker.md" >}}) decide which accounts can connect to the local socket; the permissions on the configuration file decide which of them can read or change the Broker.

## Other gates

- Claude Code's own permission settings decide whether a tool call needs your approval. They apply to the tools only: the terminal commands do not go through Claude Code's approval. The file check is independent of both; a tool call runs only when the file check and the approval both allow it.
- Neither the tools nor the terminal commands can set `pairing.static_key` or read its value. `get_config` and `agent-peer config get` report only whether it is set, and `set_config` and `agent-peer config set` always fail for it. Edit the configuration file to set, replace or delete it; the Broker applies the change without a restart.
- A permission error changes nothing: the configuration, the peers and the valid pairing keys stay as they were. When a terminal command lacks the required permission, the Broker's English error, which includes the path of the configuration file, is written to standard error, and the command exits with code 1.
