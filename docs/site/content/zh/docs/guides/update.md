---
title: 更新 agent-peer
linkTitle: 更新 agent-peer
weight: 7
---

agent-peer 会检查自己有没有新版本，由你在终端或会话里决定是否安装。检查是自动的；安装前默认先确认，除非加 `--yes`；会话里 Claude Code 默认会请你确认 `update_broker` 调用。升级还会改变没有设置 `broker.alias` 的 Broker 的默认别名，对端看到的 Agent 地址可能随之变化，原因见[升级后 Agent 地址为什么变了？]({{< relref "/docs/faq" >}})。

## 检查新版本

Broker 的正式发布版本启动后会检查 GitHub：先等约一分钟，检查一次，此后默认每 24 小时检查一次。检查在 Broker 进程里进行，一个 Broker 覆盖连到它的所有会话。

有新版本时，两个地方会显示。`agent-peer status` 的最后一行是：

```text
Update: 0.2.1 available (current 0.2.0); run agent-peer update
```

有可用新版本时，某次工具结果的末尾会附上：

```text
agent-peer 0.2.1 is available (this Broker runs 0.2.0). Ask the agent to call update_broker, or run `agent-peer update` in a terminal.
```

同一个版本在一个会话中只提示一次；会话开着期间出了新版本，之后的工具调用会再提示一次。MCP 服务在第一次工具调用时向 Broker 查询更新状态，此后最多每 10 分钟查询一次。不是正式发布版本的构建，例如不带 `-ldflags` 从源码构建的二进制，不检查，也不会出现这两行。

两个配置键控制检查：`update.check` 打开或关闭检查，`update.check_interval` 设置间隔，默认 24 小时，最短 1 小时。取值与默认值见[配置键]({{< relref "/docs/reference/configuration" >}})。

## 更新二进制

在终端运行：

```sh
agent-peer update
```

命令先拒绝不是正式发布版本的二进制，然后在改动任何东西之前询问。回答 `y` 或 `yes` 才会继续；其他回答会取消更新，二进制与服务都保持原样。确认之后才选 Broker；随后做什么，取决于选中的 Broker 以及它怎样运行。

加 `--yes` 可以跳过询问。标准输入不是终端时，例如在脚本或计划任务里，命令要求带 `--yes`，否则拒绝执行，并给出 `standard input is not a terminal; run "agent-peer update --yes" to update without asking`。已经是最新版本时，命令打印 `agent-peer 0.2.0 is up to date`，直接退出，不做任何改动。

命令的完整说明见[命令行]({{< relref "/docs/reference/cli" >}})。

### 更新用户级安装

本账户的用户级 Broker 在服务管理器下运行时，命令请这个 Broker 自己更新。Broker 下载适用于本平台与架构的发布包，与 `checksums.txt` 比对，运行新二进制核对它报出的版本是否符合预期，在保留原有权限模式与属主的前提下替换自己的二进制，然后退出；服务管理器以新版本重新启动它。命令要等到新 Broker 报出新版本才返回，输出如下：

```text
agent-peer 0.2.0 is installed. Update to the latest release? [y/N] y
The Broker is restarting to version 0.2.1.
The Broker is running agent-peer 0.2.1.
Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically.
```

用户级 Broker 不在服务管理器下运行时，Broker 会拒绝请求，命令改为就地替换二进制。正在运行的 Broker 会一直用旧版本，直到你重启它；新二进制在 Broker 下次启动时生效。

### 更新系统级安装

对系统级安装，命令请系统级 Broker 自己更新，Broker 做同样的工作：先检查自己能否写入配置文件，再下载并校验发布包，替换自己的二进制，由服务管理器以新版本重新启动。管理组成员不用 `sudo` 就能运行这条命令，见[管理权限]({{< relref "/docs/guides/management-permissions" >}}#管理组)。不在组内的账户会被拒绝，因为 Broker 按配置文件的写权限判定，要改用 `sudo agent-peer update`，或者先加入管理组。本账户同时运行着自己的用户级 Broker 时，命令先选中它，要用 `sudo agent-peer update` 才能到达系统级 Broker。

在终端里手动启动的系统级 Broker 不能自己更新：命令会报错，并给出把 Broker 装成服务的命令。Broker 是否在服务管理器下运行，按服务管理器报告的主进程 PID 判断。

命令要等到重启后的 Broker 报出新版本。超过等待时间仍未报出时，命令报告超时，并给出系统级服务日志的位置。替换之前的任何一步失败，二进制与服务都不改动。

系统级更新只重启系统级服务。其他账户如果从同一个二进制运行用户级 Broker，要各自重启自己的 Broker，才会用上新版本。

由 Broker 执行的更新，下载在 Broker 服务进程里进行，代理环境变量取自那个进程；由命令自己替换二进制时，变量取自命令的环境。

系统级 Broker 从服务环境里读取这些变量：

- Linux：运行 `sudo systemctl edit agent-peer.service`，加入 `[Service]` 与 `Environment=HTTPS_PROXY=http://proxy.example:8080`（需要时加上 `HTTP_PROXY` 与 `NO_PROXY`），然后重启服务。drop-in 在重装后仍然保留。
- macOS：在 `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist` 里加入 `EnvironmentVariables` 字典，写入 `HTTPS_PROXY` 等变量，然后执行 `sudo launchctl bootout system/io.github.wangzw.agent-peer` 与 `sudo launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`。重装会重写 plist，之后要重新加入这个字典。

用户级服务的做法相同：Linux 上用 `systemctl --user edit agent-peer.service` 编辑 drop-in，再用 `systemctl --user restart agent-peer.service` 重启服务；macOS 上在 `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist` 里加入字典，再把服务 `bootout` 后重新 `bootstrap`。重装同样会重写这个 plist，之后要重新加入。

### 没有正在运行的 Broker

没有 Broker 在运行时，命令自己替换二进制，替换前同样检查二进制所在目录是否可写。它不启动也不重启任何服务，因为服务可能是有意停掉的。你确认之后，如果查到更新的发布版本，它打印已安装版本、可用版本与发布页面，替换二进制，最后输出：

```text
The new binary takes effect the next time the Broker starts.
```

按你原来启动的方式启动 Broker，例如在终端重新运行 `agent-peer broker`，让它运行新的二进制。目录不可写时，命令在任何下载之前停下，并提示运行 `sudo agent-peer update`。

### 在会话里更新

会话可以用 `update_broker` 工具更新 Broker，这个工具不带参数。它请会话已登记的 Broker 升级自己，在 Broker 应答后立即返回，给出升级前后的版本，以及 Broker 是否即将重启。已是最新版本时，它说明这一点，不做任何改动。用户级或系统级的 Broker 不在服务管理器下运行时，不能自己更新，工具会提示改用终端命令。

这个工具需要对 Broker 配置文件的写权限，这一权限可以与读权限分开授予。它不需要终端：Claude Code 默认会在调用前请你确认，这次确认是 Broker 的二进制被替换之前最后一道人工检查。不要把 `update_broker` 加入自动批准，以免跳过这道确认。

Broker 重启后，连接会自动恢复。发起调用的会话里，MCP 服务要重开会话才会用上新版本。工具与返回字段见 [MCP 工具]({{< relref "/docs/reference/mcp-tools" >}})。

## 重连已打开的会话

由 Broker 执行的更新会重启 Broker，连接自动恢复。已经打开的会话仍在运行旧的 MCP 服务，每个会话都要用 `/mcp` 重连或重开，才会用上新版本。没有 Broker 运行、由命令替换二进制时，没有连接需要恢复；Broker 下次启动、会话连上它之后就会用上新版本。

## 按安装方式选择命令

| 安装方式 | 命令 | 处理方式 |
|---|---|---|
| 用户级：在线安装、`sh ./install.sh`，或手工放进 `~/.local/bin` 的二进制 | `agent-peer update` | 装有服务时，Broker 替换自己的二进制，服务管理器重启它；没有服务时，命令替换二进制，下次启动时生效。 |
| 系统级：带 `--system` 的在线安装、`sudo sh ./install.sh --system`，或手工 `sudo install` 到 `/usr/local/bin` | 管理组成员且本账户没有运行用户级 Broker 时用 `agent-peer update`；其余情形用 `sudo agent-peer update`；不在组内的，也可以先加入管理组 | Broker 替换自己的二进制，服务管理器重启它。 |
| 没有正在运行的 Broker | `agent-peer update` | 命令替换二进制，不启动也不重启任何服务。 |

用 `-ldflags` 以发布版本号从源码构建的二进制，按它所属安装方式对应的一行处理。不带 `-ldflags` 从源码构建的二进制报出的版本是 `dev`；它不是正式发布版本，命令会拒绝执行，并给出 `version "dev" is not a release build; only release builds can update themselves`。要就地更新，请安装正式发布版本。

二进制是符号链接时，命令会解析链接，替换它指向的文件。

## 关闭更新检查

要让 Broker 不再检查：

```sh
agent-peer config set update.check false
```

提示会立刻消失：`agent-peer status` 不再打印 `Update` 一行，会话也不再出现更新提示。无法访问 GitHub 的主机可以一直关着检查；只有你自己运行 `agent-peer update` 时，命令才会访问 GitHub。

[安全模型]({{< relref "/docs/security" >}})说明检查发出的网络访问。
