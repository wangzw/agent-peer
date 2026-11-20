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
  offline                     refuse messages from other hosts
  online                      accept messages from other hosts again
  audit [--limit <n>]         read the audit log
  config get [<key>]          show all configuration items, or one
  config set <key> <value>    change a configuration item
  pairing-key create          create a one-time pairing key
  peer list                   list known Brokers and their connection state
  peer connect <address> --key <key> [--alias <alias>]
                              pair with another Broker
  peer disconnect <broker>    remove a pairing
  update [--yes]              update through the selected Broker, or replace
                              the user-level binary locally; --yes skips the question
  service install [--system] [--admin-group <name>]
                              install and start the Broker as a user-level service,
                              or system-level with --system; --admin-group names the
                              group that administers the system-level Broker
  service start               start the installed service for this account, or
                              the system-level service as root
  service stop                stop it without disabling it
  service restart             restart it
  service uninstall [--system]
                              stop and remove only the installed service; configuration,
                              identity and audit logs are kept
  uninstall [--purge]         remove the installation for this account, or the
                              system-level installation as root; --purge deletes
                              configuration, identity, audit and service logs,
                              and at the system level the admin group
```

## agent-peer --version

打印 `agent-peer <version>` 后退出。构建时未注入版本号的二进制报告 `agent-peer dev`。

## agent-peer broker

在前台运行 Broker，直到收到 `SIGINT` 或 `SIGTERM`。在会话的进程树内启动的 Broker 会以退出码 6 失败。

- `--system` 运行系统级 Broker，需要 root。
- `--listen <address>` 只对本次运行覆盖 `broker.listen`。地址写法与配置键相同，例如 `:17443` 或 `127.0.0.1:17443`。空值会被拒绝；要停止监听，把配置文件里的 `broker.listen` 设为空值。

不给 `--listen` 时使用配置的 `broker.listen`。

## agent-peer mcp

在进程的标准输入与标准输出上为一个 Claude Code 会话运行 MCP 服务。Claude Code 按 MCP 注册启动这个进程，通常不必手动运行。本账户有用户级 Broker 在运行时，会话使用这个用户级 Broker；否则，本账户有权连接系统级 Broker 时，会话使用系统级 Broker。选择规则与相关设置见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})。正在运行的用户级 Broker 优先于系统级 Broker；在能使用系统级 Broker 的账户上安装用户级服务，这些账户的会话就会改连用户级 Broker，不再使用系统级 Broker。没有 Broker 可用时，工具调用返回错误，说明该怎么做，具体提示分三种情形。第一次查找时用户级与系统级 Broker 都找不到，或者用户级 Broker 的 socket 消失、拒绝连接（例如被强杀之后），报的是同一条：先提示本机如有系统级 Broker 请管理员启动它，再给出两种用户级启动方法，即 `agent-peer service install` 与在 Claude Code 之外的终端里运行 `agent-peer broker`。原先连在系统级 Broker 上的会话断开后，只提示联系管理员。用户级 socket 还在应答，但握手或调用失败时，报告 Broker 不可用，并在后台重试，不给出启动方法。在会话里启动的 Broker 会以退出码 6 失败。

## agent-peer status

连接正在运行的 Broker，不会启动 Broker，打印两张表：

- `Agents`：每个已登记 Agent 的地址、账户、状态、topic 与曾用名。
- `Peers`：每个对端的别名、指纹、本 Broker 的角色、状态、链路状态、对外地址与详细信息。

某一节为空时打印为 `Agents: none` 或 `Peers: none`。没有 Broker 配置文件的读权限时，只列出调用方自己的 Agent；对端与链路照常显示。本 Broker 下线时，两张表之前先输出一行 `Broker: offline; messages from other hosts are refused. Run agent-peer online to accept them.`

有更新的正式发布版本时，`agent-peer status` 在最后多打印一行：

```text
Update: <v> available (current <c>); run agent-peer update
```

没有可用版本时不打印这一行。关闭 `update.check` 后它立即消失：Broker 不再检查，也不再报告可用版本。两个更新相关的键见[配置键]({{< relref "docs/reference/configuration" >}})。

## agent-peer audit

按时间顺序逐行打印正在运行的 Broker 的审计日志，然后退出。它不启动 Broker，运行期间也不跟随新事件。

- `--limit <n>` 最多读取 `n` 条，取最新的那些，仍按从旧到新打印。`0` 表示全部，也是默认值。上限为 `4294967295`。

读取审计日志需要 Broker 配置文件的读权限。用户级 Broker 下，本账户本来就具备该权限。

## Broker 管理命令

`config`、`pairing-key`、`peer`、`offline` 与 `online` 命令连接正在运行的 Broker，不会启动 Broker。它们按执行命令的操作系统账户选择 Broker，没有按级别指定 Broker 的命令行选项：先找本账户正在运行的用户级 Broker，再找本账户有权使用的系统级 Broker。本账户运行着自己的用户级 Broker、又要管理系统级 Broker 时，必须以 `sudo` 执行命令。`status`、`audit` 与 `update` 遵循同一规则。选择规则与相关设置见[运行系统级 Broker]({{< relref "/docs/guides/system-broker" >}})。

Broker 按执行命令的账户判定每个命令的权限，规则与对应的工具相同。写操作留下的审计记录中，`account` 是执行命令的账户，`actor` 为空。管理员以 `sudo` 执行时，系统级 Broker 按 root 的权限判定。系统级 Broker 管理组的成员在本账户没有运行自己的用户级 Broker 时，不用 `sudo` 也能管理它；管理组见[以配置文件权限管理 Broker]({{< relref "/docs/guides/management-permissions" >}})。这些命令不经过 Claude Code 的权限确认。Broker 拒绝某个命令时，它给出的英文错误写到标准错误，其中包含配置文件的路径，命令以退出码 1 结束。

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

把正在运行的 Broker 已知的全部 Broker 列成表格，包括还不是对端的 Broker。列为 `ALIAS`、`FINGERPRINT`、`PEER`（`yes` 或 `no`）、`STATUS`、`DIRECTION` 与 `ADDRESSES`，按别名排序。`ADDRESSES` 列用逗号连接各地址。没有任何 Broker 时输出 `Brokers: none`。本 Broker 下线时，表格之前同样先输出 `Broker: offline; …` 这一行。

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

## agent-peer offline and online

`agent-peer offline` 让本 Broker 拒绝其他主机发来的消息，`agent-peer online` 恢复接收。两个命令分别输出 `Broker is now offline` 与 `Broker is now online`。

其他主机发往已下线 Broker 上 Agent 的消息被拒绝，错误码为 `broker_offline`，由本 Broker 报告。本机 Agent 之间照常互发，本机 Agent 也能发往其他主机，但对方的回复会被拒绝；需要双向通信时先执行 `agent-peer online`。链路、topic 成员关系、名录与新的配对都照常工作。对端只有在发送被拒绝时才知道本 Broker 已下线，它的 Broker 列表与链路状态都不显示这一点。

这一状态就是配置键 `broker.offline`，重启后保留。命令需要配置文件的写权限，与修改配置相同；没有写权限时以 `permission_denied` 失败，错误中包含配置文件的路径。每次变化都写入审计日志，并记录执行命令的账户。

## agent-peer update

安装最新的正式发布版本：要么请选中的 Broker 自己更新，要么在本地替换二进制。

```text
usage: agent-peer update [--yes]
```

命令只安装比当前版本更新的正式发布版本：草稿与预发布一律忽略，不能指定版本，也不会降级。

- `--yes` 跳过确认问题。不给时，命令询问 `agent-peer <c> is installed. Update to the latest release? [y/N]`（`<c>` 是当前版本），输入 `y` 或 `yes`（不区分大小写）即确认。标准输入不是终端时，没有 `--yes` 就拒绝执行。用户回答 `n` 时，命令输出 `Update canceled; nothing was changed.`，以退出码 0 结束。
- 用法错误以退出码 2 结束，其余失败以退出码 1 结束。更新成功、已是最新与取消更新都以退出码 0 结束。

命令先检查本二进制是不是正式发布版本，未给 `--yes` 时再请求确认，之后才选择 Broker。只有没有 Broker 在运行，或者选中的用户级 Broker 不在服务管理器下运行时，命令才在本地替换二进制；系统级 Broker 不在服务管理器下运行时，命令报错。

### 由 Broker 执行

命令选中正在运行的 Broker 时，由它自己安装更新。系统级 Broker 管理组的成员不用 `sudo` 就能更新它；本账户运行着自己的用户级 Broker 时，用 `sudo agent-peer update` 作用于系统级 Broker。

1. 把请求发给选中的 Broker。Broker 按对配置文件的写权限判定权限，命令不先行检查。权限被拒时，Broker 的英文错误写到标准错误，命令以退出码 1 结束，没有发出任何下载请求。
2. 已经是最新版本时，命令输出 `agent-peer <c> is up to date`，以退出码 0 结束。
3. Broker 下载本平台与架构的发布包和 `checksums.txt`，核对发布包的 SHA-256，取出其中名为 `agent-peer` 的普通文件，并运行新二进制的 `--version`，确认它报告的版本符合预期。然后它替换自己的二进制；符号链接仍指向更新后的文件。
4. Broker 先应答再重启。命令输出 `The Broker is restarting to version <v>.`，并等到重启后的 Broker 报告该版本，因为只有新进程在运行，更新才算完成。成功时输出 `The Broker is running agent-peer <v>.`，随后输出会话提示。重启后的 Broker 未及时报告新版本时，命令失败，并给出该级别服务日志的位置。
5. 会话提示为 `Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically.`

在终端里启动、而不是由服务管理器启动的用户级 Broker 不能替换并重启自己，会把命令交给下面的本地替换。

### 本地替换二进制

没有正在运行的 Broker，或上面的用户级 Broker 无法自我更新时，命令在本地替换二进制：

1. 定位正在运行的可执行文件，并解析符号链接。只替换它们指向的文件。
2. 检查能否写入目标所在的目录，这一步先于任何下载。没有写权限时输出 `cannot write to <dir>: ...`，随后输出 `run "sudo agent-peer update" to update a system-level installation`，以退出码 1 结束。
3. 向 GitHub 查询最新的正式发布版本。已经是最新版本时输出 `agent-peer <c> is up to date`，以退出码 0 结束。
4. 下载本平台与架构的发布包和 `checksums.txt`，核对发布包的 SHA-256，取出其中名为 `agent-peer` 的普通文件，并运行新二进制的 `--version`，确认它报告的版本符合预期。它输出 `agent-peer <c> is installed; version <v> is available.`；发布填了地址时再输出 `Release: <url>`。
5. 在目标所在的目录写入新二进制，把属主与权限改成与原文件相同，再改名到目标路径。旧版本不保留。
6. 不启动也不重启任何服务，因为服务可能是有意停掉的。它输出 `The new binary takes effect the next time the Broker starts.`。

更新成功的输出是下面两种之一。只有发布填了地址时才出现 `Release:` 一行。

由 Broker 执行：

```text
agent-peer 0.2.0 is installed. Update to the latest release? [y/N] y
The Broker is restarting to version 0.2.1.
The Broker is running agent-peer 0.2.1.
Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically.
```

本地替换：

```text
agent-peer 0.2.0 is installed. Update to the latest release? [y/N] y
agent-peer 0.2.0 is installed; version 0.2.1 is available.
Release: https://github.com/wangzw/agent-peer/releases/tag/v0.2.1
The new binary takes effect the next time the Broker starts.
```

失败信息写到标准错误，前缀为 `agent-peer update: `，包括：

- `version "dev" is not a release build; only release builds can update themselves`
- `cannot write to <dir>: ...`，随后是 `run "sudo agent-peer update" to update a system-level installation`
- `standard input is not a terminal; run "agent-peer update --yes" to update without asking`
- `checksum mismatch for <archive>`
- `downloaded binary reported "<x>", want "agent-peer <v>"`
- `the Broker did not report version <v> within <duration>; check the service log with <command or path>`；最后一次查询出错时，原因放在括号里跟在时长后面：`within <duration> (<error>)`
- `the Broker is not running under a service manager, so it cannot start the new binary again; install it as a service and run the update against that service (for example, "agent-peer service install" or "sudo agent-peer service install --system")`
- `another update is already in progress`

经 Broker 更新时，校验和不符、版本不符这类报错出现在 `installing <v> failed: ` 之后，查询最新发布版本失败出现在 `looking up the latest release failed: ` 之后。

## agent-peer service install

写入并启用服务定义，然后立即启动 Broker。它会等 Broker 应答，最多等 20 秒；超时会使安装失败，并给出服务日志的位置。重新安装会用新的二进制替换正在运行的 Broker。输出给出级别、二进制路径、版本、配置文件路径、运行日志与审计日志的位置，以及启动、停止、重启与卸载 Broker 的命令；各项见[安装 agent-peer]({{< relref "/docs/guides/install" >}}#安装输出)。

- 不带 `--system` 时安装用户级服务，账户登录时启动 Broker。不能以 root 运行。在 macOS 上只能在图形登录会话里运行。`~/.agent-peer/config.yaml` 不存在时，生成这个文件，属主为执行安装的账户，权限 `0600`；已存在的文件不改动。
- 带 `--system` 时安装系统级服务，开机时启动 Broker。必须以 root 运行。组 `agent-peer` 不存在时，命令新建它；已存在时直接使用，不改动成员。`/etc/agent-peer/config.yaml` 不存在时，生成这个文件，属主为 `root`，组为 `agent-peer`，权限 `0660`；已存在的文件不改动。`--admin-group <name>` 只在带 `--system` 时有效，用于改用另一个组；组名只能以小写字母或下划线开头，其后只能是小写字母、数字、下划线或连字符。

生成的文件是一份列出全部键的模板：有固定默认值的键写出取值，其余键写成注释并附示例值或空占位。安装写入文件时输出 `generated the configuration file <path>`，文件已存在时输出 `kept the existing configuration file <path>`，系统级安装还会输出 `To delegate administration, run: chgrp <group> <path> && chmod 0660 <path>`。执行这条命令就把已有安装交给该组，组成员随后不用 `sudo` 就能管理 Broker；管理组见[以配置文件权限管理 Broker]({{< relref "/docs/guides/management-permissions" >}})。`broker.listen` 使用小于 1024 的端口时跳过这条命令，因为这类 Broker 在另一个账户能写这个文件时会拒绝启动。

如果这一级别已有 Broker 正在应答，而且不是这个服务启动的，安装会被拒绝，并提示先停掉它。

在 Linux 上，命令使用 systemd，执行 `systemctl daemon-reload`、`systemctl enable` 和 `systemctl restart`。在 macOS 上写入并加载 launchd 作业。`--system` 还要求可执行文件及其上每一级目录属于 root、组和其他人不可写，各级目录对其他账户可进入，并且可执行文件对其他账户可执行。位于临时目录下的可执行文件会被拒绝。Intel Mac 上用 Homebrew 时，`/usr/local/bin` 属于普通用户，要改选一个 root 拥有、组和其他人不可写的目录。

[文件与路径]({{< relref "docs/reference/files" >}})列出服务的文件和位置，[使用指南]({{< relref "docs/guides" >}})介绍怎样安装和运行服务。

发布包里的安装脚本就是用这个命令安装服务的。

## agent-peer service start, stop and restart

启动、停止或重启已安装的服务。作用的级别按执行命令的账户确定，命令不接受 `--system`：root 作用于系统级服务，其他账户作用于本账户的用户级服务。要操作系统级服务，用 `sudo` 执行命令。

`stop` 不禁用服务，下次登录或开机时仍会启动。该级别没有安装服务时，命令以退出码 1 结束，并说明该级别没有安装服务；只有系统级服务的账户还会收到提示，改用 `sudo agent-peer service <command>`。

`start` 与 `restart` 和安装一样等待 Broker 应答并检查是否处于降级运行；退出码规则见 [agent-peer service install](#agent-peer-service-install)。

## agent-peer service uninstall

只停止并卸载已安装的服务。二进制与 MCP 注册都保留，配置、身份与审计日志也保留；要卸载整个安装，用 [agent-peer uninstall](#agent-peer-uninstall)。没有安装过服务时，命令照常成功，并报告未安装，所以可以重复执行。

- 不带 `--system` 时卸载用户级服务。
- 带 `--system` 时卸载系统级服务。必须以 root 运行。

## agent-peer uninstall

卸载一个安装：服务、服务启动的二进制、执行命令的账户的 MCP 注册和运行时目录。

- 以 root 执行时，卸载系统级安装。其他账户卸载自己的用户级安装；主机上同时有系统级安装时，命令先卸载用户级安装，结束后说明另一级别用 `sudo agent-peer uninstall` 卸载。
- 主机上只有系统级安装时，命令询问 `Uninstall the system-wide installation (requires sudo)? [y/N]`。回答 `y` 时命令经 `sudo` 重新执行；回答其他内容或者无法询问时，主机不做改动，输出 `sudo agent-peer uninstall`，以退出码 0 结束。
- 不给 `--purge` 时，命令询问 `Also remove configuration, identity, audit and service logs? [y/N]`，除回答 `y` 外都保留这些数据；无法询问时同样保留。系统级安装回答 `y` 时，同时删除管理组。
- `--purge` 跳过这句询问，一并删除这些数据；系统级安装同时删除管理组。
- 其他账户的 MCP 注册会保留，各自由相应账户用 `claude mcp remove -s user peer` 删除。系统级安装删除执行 `sudo` 的账户的注册；以 root 直接运行时只打印命令。

删除系统级二进制之前，命令会列出用户级 Broker 正在运行的其他账户，然后照常删除。

中途失败时命令停下，不回滚已完成的步骤；错误写明是哪一步失败，再次执行可以接着完成。两个级别都没有安装服务、但 MCP 注册指向这个二进制时，按用户级卸载；什么都没有安装时，命令报告没有安装，以退出码 0 结束。

退出码：成功、没有安装、拒绝经 sudo 卸载系统级安装都是 0；失败为 1；用法错误为 2。
