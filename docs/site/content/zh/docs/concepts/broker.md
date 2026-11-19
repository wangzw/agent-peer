---
title: Broker
weight: 1
---

Broker 是主机上的守护进程。它登记自己所服务的 Agent 会话，维护与其他 Broker 的链路，并把收到的消息写入目标会话的 inbox socket。启用了 agent-peer 工具的会话，在名字可用的前提下登记到本机的一个 Broker。

## 两种形态

Broker 以两种形态之一运行。

**系统级 Broker** 以特权身份作为系统服务运行。一台主机上至多一个，服务主机上所有获准使用它的账户下的会话。它由管理员安装。

**用户级 Broker** 以普通账户运行，只服务这个账户下的会话。一个账户至多运行一个实例。它由用户的服务管理器启动，或由用户在终端里启动；MCP 服务进程只负责连接。能使用本机系统级 Broker 的账户不需要用户级 Broker；正在运行的用户级 Broker 优先于系统级 Broker，除非会话的工具配置指定了某个 Broker。

每个实例有自己的指纹、对端和配置。同一台主机上的两个 Broker 在网络中是彼此独立的 Broker，不是同一个进程的两个部分。

## 地址与身份

默认情况下，Broker 监听所有本机地址上的 17443 端口，启用 IPv6 时也包括 IPv6 地址。其他 Broker 经它的对外地址连到它；对外地址默认取承载默认路由的网络接口的地址。监听地址为空的 Broker 不监听，只能主动连接其他 Broker。

指纹就是身份：它是 Broker 公钥的摘要，Broker 重启、改名或更换监听地址之后都不变。别名是显示名，出现在 Agent 地址的 `@` 之后。

Broker 默认不公开。公开的 Broker 发布一张名片，写明自己的指纹、别名和对外地址列表，连通网络内的每个 Broker 都能得知。是否与公开 Broker 配对，仍然由用户决定。

## 配置

配置是一个 YAML 文件。用户级 Broker 读取 `~/.agent-peer/config.yaml`，系统级 Broker 读取 `/etc/agent-peer/config.yaml`。读取或修改配置的工具遵循操作系统对这个文件的权限：读权限允许读取配置、完整状态和审计日志，写权限允许修改设置、生成一次性配对密钥、配对和解除配对。列出 Agent 与 topic、加入 topic、发送消息都不需要这些权限。

Broker 如何决定可见范围，见[topic]({{< relref "/docs/concepts/topics" >}})；如何信任另一个 Broker，见[配对]({{< relref "/docs/concepts/pairing" >}})；对端之间的连接，见[链路]({{< relref "/docs/concepts/links" >}})。安装服务见[把 Broker 安装为服务]({{< relref "/docs/guides/run-as-service" >}})，多账户部署见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})；配置键与文件路径见[配置键]({{< relref "/docs/reference/configuration" >}})和[文件与路径]({{< relref "/docs/reference/files" >}})。
