---
title: Files and paths
weight: 4
---

The Broker keeps its configuration, identity, peer table, directory and audit log in fixed locations. The locations are chosen by the deployment form: a user Broker serves one account, and a system Broker runs as root and serves every permitted account on the host.

## User-level Broker

| Path | Contents |
|---|---|
| `~/.agent-peer/config.yaml` | Configuration. |
| `~/.agent-peer/identity.key` | The Broker's private key, mode `0600`. |
| `~/.agent-peer/identity.crt` | The self-signed certificate that matches the key, mode `0600`. |
| `~/.agent-peer/peers.json` | Paired peers, mode `0600`. |
| `~/.agent-peer/directory.json` | Broker cards and public-topic declarations this Broker knows. |
| `~/.agent-peer/audit.log` | Audit log, mode `0600`. |
| `/tmp/agent-peer-<uid>/broker.sock` | Local socket, mode `0600`. |
| `/tmp/agent-peer-<uid>/broker.lock` | Account lock that allows one Broker per account. |

`<uid>` is the numeric account ID. The runtime directory is mode `0700`. The identity files are created at the Broker's first start; `peers.json` and `directory.json` appear when the Broker first has something to store. When a user Broker runs as root (for example one that root started in a terminal), the runtime directory is `/run/agent-peer` on Linux or `/var/run/agent-peer` on macOS, the same directory the system Broker uses.

## System-level Broker

| Path | Contents |
|---|---|
| `/etc/agent-peer/config.yaml` | Configuration. Created by `agent-peer service install --system` if it does not exist. |
| `/var/lib/agent-peer/identity.key` | The Broker's private key, mode `0600`. |
| `/var/lib/agent-peer/identity.crt` | The matching self-signed certificate, mode `0600`. |
| `/var/lib/agent-peer/peers.json` | Paired peers, mode `0600`. |
| `/var/lib/agent-peer/directory.json` | Broker cards and public-topic declarations this Broker knows. |
| `/var/log/agent-peer/audit.log` | Audit log, mode `0600`. |
| `/run/agent-peer/broker.sock` | Local socket, on Linux. |
| `/var/run/agent-peer/broker.sock` | Local socket, on macOS. |
| `/run/agent-peer/broker.lock` or `/var/run/agent-peer/broker.lock` | Account lock, in the same directory as the socket. |

The runtime directory is mode `0755`. The socket's group and mode come from `broker.local_socket_group` and `broker.local_socket_mode`; the mode defaults to `0666`, which lets every local account connect, and the file permission on the configuration then decides what each account may do.

## Service definitions

`agent-peer service install` writes one service definition:

| Platform | Form | Path |
|---|---|---|
| Linux | user | `~/.config/systemd/user/agent-peer.service`, or `$XDG_CONFIG_HOME/systemd/user/agent-peer.service` when `XDG_CONFIG_HOME` is set to an absolute path |
| Linux | system | `/etc/systemd/system/agent-peer.service` |
| macOS | user | `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist` |
| macOS | system | `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist` |

## Logs

The Broker writes its run log to standard error and the audit log to the audit file above.

- A Linux service logs to the systemd journal: `journalctl --user -u agent-peer.service` for the user-level service, or `journalctl -u agent-peer.service` for the system-level service.
- A macOS user-level service writes to `~/Library/Logs/agent-peer/broker.log`; the system-level daemon writes to `/var/log/agent-peer/broker.log`.
- A Broker started in a terminal writes its run log to that terminal.

## Permissions

A user Broker runs as its account. The Broker creates the data directory with mode `0700`, creates its files with mode `0600`, and binds the local socket with mode `0600`, so only that account can reach them.

A system Broker runs as root. The configuration file's permissions decide what other accounts may do with the management tools and the terminal commands: read access allows reading the configuration, the full status and the audit log; write access allows changing the configuration and managing pairings. The administrator delegates by granting a group access to the file, for example with `chgrp` and `chmod`. The Broker checks the caller's access to the actual file, so the tools and the terminal commands never let an account do more than editing the file directly would.

The socket and its mode are described under [Configuration keys]({{< relref "docs/reference/configuration" >}}). The project configuration file is described separately under [Project configuration file]({{< relref "docs/reference/project-config" >}}), and [Guides]({{< relref "docs/guides" >}}) covers installing the service.
