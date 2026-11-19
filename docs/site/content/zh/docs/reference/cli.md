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
  service install [--system]  install and start the Broker as a user-level service,
                              or system-level with --system
  service uninstall [--system]
                              stop and remove the installed service; configuration,
                              identity and audit logs are kept
```

## agent-peer --version

打印 `agent-peer <version>` 后退出。构建时未注入版本号的二进制报告 `agent-peer dev`。

## agent-peer broker

在前台运行 Broker，直到收到 `SIGINT` 或 `SIGTERM`。在会话的进程树内启动的 Broker 会以退出码 6 失败。

- `--system` 运行系统级 Broker，需要 root。
- `--listen <address>` 只对本次运行覆盖 `broker.listen`。地址写法与配置键相同，例如 `:17443` 或 `127.0.0.1:17443`。空值会被拒绝；要停止监听，把配置文件里的 `broker.listen` 设为空值。

不给 `--listen` 时使用配置的 `broker.listen`。

## agent-peer mcp

在进程的标准输入与标准输出上为一个 Claude Code 会话运行 MCP 服务。Claude Code 按 MCP 注册启动这个进程，通常不必手动运行。会话按以下顺序选定 Broker：`AGENT_PEER_SOCKET` 指定的 socket、本账户的用户级 Broker、系统级 Broker；顺序与相关设置见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})。正在运行的用户级 Broker 优先于系统级 Broker；在能使用系统级 Broker 的账户上安装用户级服务，这些账户的会话就会改连用户级 Broker，不再使用系统级 Broker。没有 Broker 可用时，工具调用返回错误，说明该怎么做，具体提示分三种情形。第一次查找时用户级与系统级 Broker 都找不到，或者用户级 Broker 的 socket 消失、拒绝连接（例如被强杀之后），报的是同一条：先提示本机如有系统级 Broker 请管理员启动它，再给出两种用户级启动方法，即 `agent-peer service install` 与在 Claude Code 之外的终端里运行 `agent-peer broker`。原先连在系统级 Broker 上的会话断开后，只提示联系管理员。用户级 socket 还在应答，但握手或调用失败时，报告 Broker 不可用，并在后台重试，不给出启动方法。在会话里启动的 Broker 会以退出码 6 失败。

## agent-peer status

连接正在运行的 Broker，不会启动 Broker，打印两张表：

- `Agents`：每个已登记 Agent 的地址、账户、状态、topic 与曾用名。
- `Peers`：每个对端的别名、指纹、本 Broker 的角色、状态、链路状态、对外地址与详细信息。

某一节为空时打印为 `Agents: none` 或 `Peers: none`。没有 Broker 配置文件的读权限时，只列出调用方自己的 Agent；对端与链路照常显示。

## agent-peer audit

按时间顺序逐行打印正在运行的 Broker 的审计日志，然后退出。它不启动 Broker，运行期间也不跟随新事件。

- `--limit <n>` 最多读取 `n` 条，取最新的那些，仍按从旧到新打印。`0` 表示全部，也是默认值。上限为 `4294967295`。

读取审计日志需要 Broker 配置文件的读权限。用户级 Broker 下，本账户本来就具备该权限。

## agent-peer service install

写入并启用服务定义，然后立即启动 Broker。它会等 Broker 应答，最多等 20 秒；超时会使安装失败，并给出服务日志的位置。重新安装会用新的二进制替换正在运行的 Broker。

- 不带 `--system` 时安装用户级服务，账户登录时启动 Broker。不能以 root 运行。在 macOS 上只能在图形登录会话里运行。
- 带 `--system` 时安装系统级服务，开机时启动 Broker。必须以 root 运行，并会在 `/etc/agent-peer/config.yaml` 不存在时创建它。

如果这一级别已有 Broker 正在应答，而且不是这个服务启动的，安装会被拒绝，并提示先停掉它。

在 Linux 上，命令使用 systemd，执行 `systemctl daemon-reload`、`systemctl enable` 和 `systemctl restart`。在 macOS 上写入并加载 launchd 作业。`--system` 还要求可执行文件及其上每一级目录属于 root、组和其他人不可写，各级目录对其他账户可进入，并且可执行文件对其他账户可执行。位于临时目录下的可执行文件会被拒绝。Intel Mac 上用 Homebrew 时，`/usr/local/bin` 属于普通用户，要改选一个 root 拥有、组和其他人不可写的目录。

[文件与路径]({{< relref "docs/reference/files" >}})列出服务的文件和位置，[使用指南]({{< relref "docs/guides" >}})介绍怎样安装和运行服务。

## agent-peer service uninstall

停止并卸载已安装的服务。配置、身份与审计日志都保留。没有安装过服务时，命令照常成功，并报告未安装，所以可以重复执行。

- 不带 `--system` 时卸载用户级服务。
- 带 `--system` 时卸载系统级服务。必须以 root 运行。
