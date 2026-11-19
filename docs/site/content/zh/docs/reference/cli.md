---
title: 命令行
weight: 2
---

`agent-peer` 二进制负责运行 Broker 和 MCP 服务，并提供终端命令。不带参数运行，或用 `-h`、`--help`、`help`，会打印下面的用法。

```
usage: agent-peer <command> [arguments]

Commands:
  --version                   print the version and exit
  broker [--system] [--listen <address>]
                              run the Broker
  mcp                         run the MCP server for one Claude Code session
  status                      show the state of a running Broker
  audit [--limit <n>]         read the audit log
  service install [--system]  install the Broker as a user-level service, or system-level with --system
```

## agent-peer --version

打印 `agent-peer <version>` 后退出。构建时未注入版本号的二进制报告 `agent-peer dev`。

## agent-peer broker

在前台运行 Broker，直到收到 `SIGINT` 或 `SIGTERM`。

- `--system` 运行系统级 Broker，需要 root。
- `--listen <address>` 只对本次运行覆盖 `broker.listen`。地址写法与配置键相同，例如 `:17443` 或 `127.0.0.1:17443`。空值会被拒绝；要停止监听，把配置文件里的 `broker.listen` 设为空值。

不给 `--listen` 时使用配置的 `broker.listen`。

## agent-peer mcp

在进程的标准输入与标准输出上为一个 Claude Code 会话运行 MCP 服务。Claude Code 按 MCP 注册启动这个进程，通常不必手动运行。会话按以下顺序选定 Broker：`AGENT_PEER_SOCKET` 指定的 socket、本账户的用户级 Broker、系统级 Broker；顺序与相关设置见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})。都不可用时，该进程在后台为本账户启动一个用户级 Broker。

## agent-peer status

连接正在运行的 Broker，不会启动 Broker，打印两张表：

- `Agents`：每个已登记 Agent 的地址、账户、状态、topic 与曾用名。
- `Peers`：每个对端的别名、指纹、本 Broker 的角色、状态、链路状态、对外地址与详细信息。

某一节为空时打印为 `Agents: none` 或 `Peers: none`。没有 Broker 配置文件的读权限时，只列出调用方自己的 Agent，对端一节为空。

## agent-peer audit

按时间顺序逐行打印正在运行的 Broker 的审计日志，然后退出。它不启动 Broker，运行期间也不跟随新事件。

- `--limit <n>` 最多读取 `n` 条，取最新的那些，仍按从旧到新打印。`0` 表示全部，也是默认值。上限为 `4294967295`。

读取审计日志需要 Broker 配置文件的读权限。用户级 Broker 下，本账户本来就具备该权限。

## agent-peer service install

写入并启用服务定义，不启动服务；输出的最后一行是立即启动服务的命令。

- 不带 `--system` 时安装用户级服务，账户登录时启动 Broker。不能以 root 运行。
- 带 `--system` 时安装系统级服务，开机时启动 Broker。必须以 root 运行，并会在 `/etc/agent-peer/config.yaml` 不存在时创建它。

在 Linux 上，命令使用 systemd，执行 `systemctl daemon-reload` 和 `systemctl enable`。在 macOS 上写入 launchd 作业。`--system` 还要求可执行文件及其上每一级目录属于 root、组和其他账户不可写，各级目录对其他账户可进入，并且可执行文件对其他账户可执行。位于临时目录下的可执行文件会被拒绝。

[文件与路径]({{< relref "docs/reference/files" >}})列出服务的文件和位置，[使用指南]({{< relref "docs/guides" >}})介绍怎样安装和运行服务。
