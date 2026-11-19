---
title: Manage a Broker by file permission
linkTitle: Management permissions
weight: 6
---

The tools can change a Broker only as far as the calling account could change the Broker's configuration file by hand. The Broker takes the account from the process credentials the operating system reports for the local socket; it does not rely on what the caller says.

## The rule

| Operation | Required access to the configuration file |
|---|---|
| `list_agents`, `list_topics`, `list_brokers`, `join_topic`, `leave_topic`, `send_message` | none |
| `get_config` | read |
| `set_config`, `create_pairing_key`, `connect_broker`, `disconnect_broker` | write |
| Full `agent-peer status` output, `agent-peer audit` | read |

A user Broker's configuration file belongs to the account that runs it, so these checks pass for that account without any setup. On a system Broker, the file is `/etc/agent-peer/config.yaml`, created with owner root and mode 0600. Read access also lets an account read the full status and the audit log; write access lets it change settings and manage pairing. Both levels are ordinary file permissions, not a separate list of administrators.

The check identifies an operating-system account, not a person or a model. Any process running under the same account can make the same calls.

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

- Claude Code's own permission settings decide whether a tool call needs your approval. The file check and the session's approval are independent; both must allow the call.
- The tools cannot set `pairing.static_key` or read its value. `get_config` reports only whether it is set, and `set_config` always fails for it. Edit the configuration file to set, replace or delete it; the Broker applies the change without a restart.
- A permission error changes nothing: the configuration, the peers and the valid pairing keys stay as they were.
