---
title: 运行系统级 Broker
linkTitle: 系统级 Broker
weight: 5
---

系统级 Broker 以 root 特权作为服务运行，服务一台主机上多个账户的 Agent。用户级 Broker 只服务一个账户。一台主机至多运行一个系统级 Broker。

## 安装并启用

把二进制装进 root 拥有的目录，再以 root 安装服务。命令见[把 Broker 安装为服务]({{< relref "run-as-service.md" >}})。系统级服务在开机时启动。

系统级 Broker 取的是 root 的账户锁，所以 root 名下已有 Broker 在运行（例如 root 会话拉起的那一个）时，服务会退出，并每 5 秒重试一次。先退出 root 名下启用 peer 工具的会话，或在其中用 `/mcp` 停用 peer，再停掉那个 Broker 并启动服务；会话还开着时，Broker 停下约 250 毫秒后就会被重新拉起。完整顺序见[把 Broker 安装为服务]({{< relref "run-as-service.md" >}})。

系统级 Broker 使用 `/etc/agent-peer/config.yaml`、`/var/log/agent-peer/audit.log` 和 `/var/lib/agent-peer`。安装时如果配置文件不存在，会以属主 root、权限 0600 创建它。

## 允许账户使用它

每个账户各自注册 MCP 服务，例如 `claude mcp add --scope user peer -- agent-peer mcp`。注册按账户进行，不按主机进行。

会话通过本机 socket 访问系统级 Broker：Linux 上是 `/run/agent-peer/broker.sock`，macOS 上是 `/var/run/agent-peer/broker.sock`。两个设置决定哪些账户能连接：

| 键 | 默认值 | 作用 |
|---|---|---|
| `broker.local_socket_group` | 空 | socket 的属组。空表示沿用运行时目录的属组。组必须存在。 |
| `broker.local_socket_mode` | `0666` | socket 的权限位。 |

要限定到某个组，就把 socket 的属组设为这个组，并把 socket 权限设为允许该组访问的值，例如 `broker.local_socket_group: agent-peer` 配 `broker.local_socket_mode: 0660`。组外的账户连不上，它们的会话会改用或启动自己的用户级 Broker。

## 会话登记到哪个 Broker

会话的 MCP 服务进程按以下顺序选定 Broker：

1. 会话环境里 `AGENT_PEER_SOCKET` 指定的 socket（如果设置了）。
2. 本账户正在运行的用户级 Broker。
3. 本账户有权使用的系统级 Broker。

三者都不可用时，MCP 服务进程会启动一个用户级 Broker。因此，如果希望所有账户都用系统级 Broker，就不要让这些账户运行用户级 Broker。

一个账户有权使用系统级 Broker，但它自己的用户级 Broker 正在运行，会话会登记到用户级 Broker 上。把 `AGENT_PEER_SOCKET` 指向系统级 socket，可以让该会话改用系统级 Broker。

## 多个账户共用一个身份

同一个系统级 Broker 服务的所有 Agent 共用它的指纹、别名和对端。不同账户的 Agent 只要加入同一个 topic，就能互相看见并互发消息。因此系统级 Broker 配对一次，所有被服务的账户就都连到了对方；另一台主机只需要这一次配对，就能访问这些账户。

Broker 的管理权限由 `/etc/agent-peer/config.yaml` 的文件权限决定，与运行服务的账户无关。授予读或写权限的做法见[以配置文件权限管理 Broker]({{< relref "management-permissions.md" >}})。
