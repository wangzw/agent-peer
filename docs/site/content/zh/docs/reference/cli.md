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
  config get [<key>]          show all configuration items, or one
  config set <key> <value>    change a configuration item
  pairing-key create          create a one-time pairing key
  peer list                   list paired Brokers
  peer connect <address> --key <key> [--alias <alias>]
                              pair with another Broker
  peer disconnect <broker>    remove a pairing
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

## Broker 管理命令

`config`、`pairing-key` 与 `peer` 下的命令连接正在运行的 Broker，不会启动 Broker。查找顺序与会话相同：`AGENT_PEER_SOCKET` 指定的 socket、本账户正在运行的用户级 Broker、系统级 Broker。正在运行的用户级 Broker 优先于系统级 Broker，所以本账户的用户级 Broker 在运行时若要管理系统级 Broker，须设置 `AGENT_PEER_SOCKET`，或以 `sudo` 执行命令。顺序与相关设置见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})。

Broker 按执行命令的账户判定每个命令的权限，规则与对应的工具相同。写操作留下的审计记录中，`account` 是执行命令的账户，`actor` 为空。管理员以 `sudo` 执行时，系统级 Broker 按 root 的权限判定。这些命令不经过 Claude Code 的权限确认。Broker 拒绝某个命令时，它给出的英文错误写到标准错误，其中包含配置文件的路径，命令以退出码 1 结束。

这些命令的输出沿用 `agent-peer status` 的约定：空值写 `-`。

## agent-peer config get

打印正在运行的 Broker 的配置项。

- 不带参数时打印全部配置项，每项一行，写成 `<key> = <value>`，按 `key` 排序。
- 带 `key` 时只打印该项。

`pairing.static_key` 只打印 `set` 或 `unset`，不打印取值。未知的键会失败。

需要 Broker 配置文件的读权限；用户级 Broker 下，本账户本来就具备该权限。用法错误以退出码 2 结束，其余失败以退出码 1 结束。

## agent-peer config set

修改一个配置项，并打印改动，写成 `<key>: <old> -> <new>`。

- `key` 与 `value` 都必须给出。
- `pairing.static_key` 不能在这里修改，只能在配置文件里设置。

Broker 以 `--listen` 运行时，该选项固定了 `broker.listen`，`config set` 不能修改它；规则见[配置键]({{< relref "docs/reference/configuration" >}})。

需要 Broker 配置文件的写权限。用法错误以退出码 2 结束，其余失败（包括 Broker 拒绝的键）以退出码 1 结束。

## agent-peer pairing-key create

在正在运行的 Broker 上生成一次性配对密钥，并输出密钥、失效时间与本 Broker 的对外地址：

```text
Key: <key>
Expires: <RFC 3339 UTC>
Addresses:
  <address>
```

每个地址单独一行，缩进两格。`broker.listen` 为空、且启动时没有给 `--listen` 时，Broker 不监听，会拒绝请求，命令以退出码 1 结束。

需要 Broker 配置文件的写权限。用法错误以退出码 2 结束，其余失败以退出码 1 结束。

## agent-peer peer list

把正在运行的 Broker 已知的全部 Broker 列成表格，包括还不是对端的 Broker。列为 `ALIAS`、`FINGERPRINT`、`PEER`（`yes` 或 `no`）、`STATUS`、`DIRECTION` 与 `ADDRESSES`，按别名排序。`ADDRESSES` 列用逗号连接各地址。没有任何 Broker 时输出 `Brokers: none`。

不需要权限。用法错误以退出码 2 结束，其余失败以退出码 1 结束。

## agent-peer peer connect

把本 Broker 与另一个 Broker 配对，并打印一行 `Paired with <alias> (<fingerprint>)`。

- `address` 写成 `address:port` 或 `hostname(address:port)`。
- `--key <key>` 是对方 Broker 上生成的一次性配对密钥，或对方配置的预置静态密钥。
- `--alias <alias>` 指定本地别名。对方的自报别名与已有别名冲突、又没有给出本地别名时，配对失败，并提示提供本地别名。

应答中的警告逐条写到标准错误，每行前缀 `warning: `，命令仍以退出码 0 结束。

需要 Broker 配置文件的写权限。用法错误以退出码 2 结束，其余失败以退出码 1 结束。

## agent-peer peer disconnect

解除与一个 Broker 的配对，并打印一行 `Disconnected <broker>`。

- `broker` 写对端的别名或完整的 64 位十六进制指纹。

需要 Broker 配置文件的写权限。用法错误以退出码 2 结束，其余失败以退出码 1 结束。

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
