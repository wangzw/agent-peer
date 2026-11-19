---
title: 文件与路径
weight: 4
---

Broker 把配置、身份、对端表、名录和审计日志放在固定位置。位置取决于部署形态：用户级 Broker 只服务一个账户；系统级 Broker 以 root 运行，服务本机上获准使用它的各个账户。

## 用户级 Broker

| 路径 | 内容 |
|---|---|
| `~/.agent-peer/config.yaml` | 配置文件。 |
| `~/.agent-peer/identity.key` | Broker 的私钥，权限 `0600`。 |
| `~/.agent-peer/identity.crt` | 与私钥配对的自签名证书，权限 `0600`。 |
| `~/.agent-peer/peers.json` | 已配对的对端，权限 `0600`。 |
| `~/.agent-peer/directory.json` | 本 Broker 已知的名片与公开 topic 声明。 |
| `~/.agent-peer/audit.log` | 审计日志，权限 `0600`。 |
| `/tmp/agent-peer-<uid>/broker.sock` | 本机 socket，权限 `0600`。 |
| `/tmp/agent-peer-<uid>/broker.lock` | 账户锁，保证每个账户至多一个 Broker。 |

`<uid>` 是账户的数字 ID。运行时目录权限为 `0700`。身份文件在 Broker 第一次启动时创建；`peers.json` 和 `directory.json` 在第一次有内容需要保存时出现。以 root 运行用户级 Broker 时（例如 root 在终端里启动的那一个），运行时目录是 Linux 上的 `/run/agent-peer`、macOS 上的 `/var/run/agent-peer`，与系统级 Broker 相同。

## 系统级 Broker

| 路径 | 内容 |
|---|---|
| `/etc/agent-peer/config.yaml` | 配置文件。`agent-peer service install --system` 在该文件不存在时创建它。 |
| `/var/lib/agent-peer/identity.key` | Broker 的私钥，权限 `0600`。 |
| `/var/lib/agent-peer/identity.crt` | 与私钥配对的自签名证书，权限 `0600`。 |
| `/var/lib/agent-peer/peers.json` | 已配对的对端，权限 `0600`。 |
| `/var/lib/agent-peer/directory.json` | 本 Broker 已知的名片与公开 topic 声明。 |
| `/var/log/agent-peer/audit.log` | 审计日志，权限 `0600`。 |
| `/run/agent-peer/broker.sock` | 本机 socket，Linux。 |
| `/var/run/agent-peer/broker.sock` | 本机 socket，macOS。 |
| `/run/agent-peer/broker.lock` 或 `/var/run/agent-peer/broker.lock` | 账户锁，与 socket 在同一目录。 |

运行时目录权限为 `0755`。socket 的属组与权限取自 `broker.local_socket_group` 和 `broker.local_socket_mode`；权限默认为 `0666`，本机任何账户都能连接，随后由配置文件的权限决定各账户能做什么。

## 服务定义

`agent-peer service install` 写入一个服务定义：

| 平台 | 形态 | 路径 |
|---|---|---|
| Linux | 用户级 | `~/.config/systemd/user/agent-peer.service`；`XDG_CONFIG_HOME` 为绝对路径时是 `$XDG_CONFIG_HOME/systemd/user/agent-peer.service` |
| Linux | 系统级 | `/etc/systemd/system/agent-peer.service` |
| macOS | 用户级 | `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist` |
| macOS | 系统级 | `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist` |

## 日志

Broker 把运行日志写到标准错误，把审计日志写到上面的审计文件。

- Linux 服务把日志写入 systemd journal：用户级服务用 `journalctl --user -u agent-peer.service`，系统级服务用 `journalctl -u agent-peer.service`。
- macOS 用户级服务写入 `~/Library/Logs/agent-peer/broker.log`；系统级守护进程写入 `/var/log/agent-peer/broker.log`。
- 在终端里启动的 Broker，运行日志写进那个终端。

## 权限

用户级 Broker 以该账户身份运行。Broker 以 `0700` 创建数据目录，以 `0600` 创建文件，并以 `0600` 绑定本机 socket，所以只有该账户能访问它们。

系统级 Broker 以 root 运行。配置文件的权限决定其他账户能使用哪些管理工具：读权限允许读取配置、完整状态与审计日志；写权限允许修改配置和管理配对。管理员可以用 `chgrp` 和 `chmod` 把文件的相应权限授予一个组。Broker 检查调用方对实际文件的访问权限，所以经工具能做的事不超出直接编辑文件能做的事。

socket 及其权限见[配置键]({{< relref "docs/reference/configuration" >}})。项目配置文件另见[项目配置文件]({{< relref "docs/reference/project-config" >}})，[使用指南]({{< relref "docs/guides" >}})介绍怎样安装服务。
