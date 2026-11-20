---
title: Manage a Broker by file permission
linkTitle: Management permissions
weight: 7
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
| `update_broker`, `agent-peer update` through the Broker | write |

`agent-peer update` checks write permission on the binary's directory instead when it replaces the binary locally, because no Broker is running or the selected user Broker does not run as a service.

A user Broker's configuration file belongs to the account that runs it, so these checks pass for that account without any setup. A system Broker's file is `/etc/agent-peer/config.yaml`. A new installation creates it with owner root, the default group `agent-peer` and mode `0660`; an existing file is left unchanged. Read access also lets an account read the full status and the audit log; write access lets it change settings and manage pairing. Both levels are ordinary file permissions, not a separate list of administrators.

Members of the group that owns the configuration file manage the Broker without `sudo`. An account that runs the terminal commands with `sudo` is checked as root, so an administrator can manage the Broker that way whatever the file's group is. If this account also runs a user-level Broker, its terminal commands connect to that Broker; run them with `sudo` to manage the system Broker. The section [Which Broker a session or command uses]({{< relref "system-broker.md" >}}) describes the order.

The check identifies an operating-system account, not a person or a model. Any process running under the same account can make the same calls, whether through the tools or from the terminal.

## The admin group

`agent-peer service install --system` creates the `agent-peer` group by default when it does not exist, and uses an existing group without changing its members. Pass `--admin-group <name>` to use another group instead; the name may start only with a lowercase letter or an underscore and may contain only lowercase letters, digits, underscores and hyphens. New configuration files belong to the group with mode `0660`, so members of the group manage the Broker without `sudo`. The group is trusted as if its members could edit the configuration file by hand; it does not make them root.

The release archive's install script asks whether to add the account that ran `sudo` to the group, and does not add it by default. `--join-admin-group` adds that account without asking, and `--no-admin-group` neither asks nor adds it. Without a terminal, the script adds nothing and prints the command to join the group. A new membership applies from the next call through the Broker, because the Broker reads the account's groups from the account database on each check. Log in again, or run `newgrp agent-peer`, before the account's own processes use the new membership, for example to edit the configuration file directly. To add another account, run `sudo usermod -aG agent-peer <account>` on Linux, or `sudo dseditgroup -o edit -a <account> -t user agent-peer` on macOS; if you set `--admin-group`, use that group name instead.

For an existing installation, give the file to the group with the command the install output prints:

```sh
sudo chgrp agent-peer /etc/agent-peer/config.yaml
sudo chmod 0660 /etc/agent-peer/config.yaml
```

Use `0640` instead of `0660` to grant read access only.

Skip giving the file to the group when `broker.listen` uses a port below 1024, because such a Broker refuses to start while another account can write the file. Read-only access (`0640`) is not subject to this limit, because the rule looks only at whether the group or others can write the file.

Two limits come with the group. A port below 1024 in `broker.listen` takes effect only while the configuration file can be written by root alone, so a group that can write the file cannot move the Broker to a privileged port. A file that a group or others can write and that sets such a port makes the Broker refuse to start; when the file is changed to that combination while the Broker runs, the Broker keeps the old value instead of applying the new port. A change made by editing the file directly is recorded in the audit log with no account, because only changes through the local socket carry the caller's account. Installing and uninstalling the system-level service still needs root.

Being allowed to use the Broker and being allowed to manage it are separate. The settings in [Run a system Broker]({{< relref "system-broker.md" >}}) decide which accounts can connect to the local socket; the permissions on the configuration file decide which of them can read or change the Broker.

## Other gates

- Claude Code's own permission settings decide whether a tool call needs your approval. They apply to the tools only: the terminal commands do not go through Claude Code's approval. The file check is independent of both; a tool call runs only when the file check and the approval both allow it.
- Do not add `update_broker` to Claude Code's auto-approve list. That confirmation is the last manual check before an upgrade, and for a system-level installation the Broker replaces a binary that runs as root.
- Neither the tools nor the terminal commands can set `pairing.static_key` or read its value. `get_config` and `agent-peer config get` report only whether it is set, and `set_config` and `agent-peer config set` always fail for it. Edit the configuration file to set, replace or delete it; the Broker applies the change without a restart.
- A permission error changes nothing: the configuration, the peers and the valid pairing keys stay as they were. When a terminal command lacks the required permission, the Broker's English error, which includes the path of the configuration file, is written to standard error, and the command exits with code 1.
