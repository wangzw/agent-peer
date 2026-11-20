---
title: 更新 agent-peer
linkTitle: 更新 agent-peer
weight: 7
---

agent-peer 会检查自己有没有新版本，由你在终端里决定是否安装。检查是自动的；除非加 `--yes`，安装前会先确认。升级还会改变没有设置 `broker.alias` 的 Broker 的默认别名，对端看到的 Agent 地址可能随之变化，原因见[升级后 Agent 地址为什么变了？]({{< relref "/docs/faq" >}})。

## 检查新版本

Broker 的正式发布版本启动后会检查 GitHub：先等约一分钟，检查一次，此后默认每 24 小时检查一次。检查在 Broker 进程里进行，一个 Broker 覆盖连到它的所有会话。

有新版本时，两个地方会显示。`agent-peer status` 的最后一行是：

```text
Update: 0.1.2 available (current 0.1.1); run agent-peer update
```

有可用新版本时，某次工具结果的末尾会附上：

```text
agent-peer 0.1.2 is available (this host runs 0.1.1). Update it with `agent-peer update` in a terminal, using sudo for a system-level installation.
```

同一个版本在一个会话中只提示一次；会话开着期间出了新版本，之后的工具调用会再提示一次。MCP 服务在第一次工具调用时向 Broker 查询更新状态，此后最多每 10 分钟查询一次。不是正式发布版本的构建，例如不带 `-ldflags` 从源码构建的二进制，不检查，也不会出现这两行。

两个配置键控制检查：`update.check` 打开或关闭检查，`update.check_interval` 设置间隔，默认 24 小时，最短 1 小时。取值与默认值见[配置键]({{< relref "/docs/reference/configuration" >}})。

## 更新二进制

在终端运行：

```sh
agent-peer update
```

命令打印已安装版本与可用版本，以及发布页面，并在改动任何东西之前询问。回答 `y` 或 `yes` 才会继续；其他回答会取消更新，二进制与服务都保持原样。有可用新版本时，输出如下：

```text
agent-peer 0.1.1 is installed; version 0.1.2 is available.
Release: https://github.com/wangzw/agent-peer/releases/tag/v0.1.2
Update to 0.1.2? [y/N] y
agent-peer 0.1.2 is installed.
Restarted the agent-peer service.
User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user.
Open Claude Code sessions must reconnect with /mcp or be restarted to use the new version.
```

加 `--yes` 可以跳过询问。标准输入不是终端时，例如在脚本或计划任务里，命令要求带 `--yes`，否则拒绝执行，并给出 `standard input is not a terminal; run "agent-peer update --yes" to update without asking`。已经是最新版本时，不需要 `--yes`，在非终端里也能运行。

下载任何内容之前，命令先确认自己能否写入二进制所在的目录。之后下载适用于本平台与架构的发布包，与 `checksums.txt` 比对，再运行新二进制，核对它报出的版本是否符合预期。全部通过后，它才替换二进制，并保留原有的权限模式与属主。替换之前的任何一步失败，命令都会停下，二进制与服务都不改动。替换之后重启服务失败时，新二进制已经装好，按报错手动重启服务即可。

已是最新版本时，命令打印 `agent-peer 0.1.1 is up to date`，直接退出，不做任何改动。

命令的完整说明见[命令行]({{< relref "/docs/reference/cli" >}})。

## 更新系统级安装

系统级的二进制放在你的账户不能写入的目录里，所以要用 `sudo` 运行：

```sh
sudo agent-peer update
```

写权限检查在任何下载之前进行，普通账户会立即失败，命令会提示运行 `sudo agent-peer update`。`sudo` 默认会丢弃代理环境变量；需要代理时，用 `sudo -E` 保留，或在 `env_keep` 中加上这样一行：`env_keep += "HTTP_PROXY HTTPS_PROXY NO_PROXY"`。用 `sudo -E` 时，命令是：

```sh
sudo -E agent-peer update
```

系统级更新只重启系统级服务。其他账户如果从同一个二进制运行用户级 Broker，要各自重启自己的 Broker，例如以该账户执行 `agent-peer service install`，它们的会话才会用上新版本。命令会在重连提示之前打印这一行提示。

## 重启自己启动的 Broker

更新命令会重启已安装为服务的 Broker。没有安装为服务时，它打印 `The Broker is not installed as a service; restart it to use the new version.`。按你原来启动的方式停掉再启动，例如在终端重新运行 `agent-peer broker`，让它运行新的二进制。

## 重连已打开的会话

更新不会重连已经打开的会话。每个打开的会话会继续运行旧版本的 MCP 服务，直到你用 `/mcp` 重连或重开会话。更新命令的最后一行会提醒这一点。

## 按安装方式选择命令

| 安装方式 | 命令 | 服务的处理 |
|---|---|---|
| 用户级：在线安装、`sh ./install.sh`，或手工放进 `~/.local/bin` 的二进制 | `agent-peer update` | 装有用户级服务时，自动重启该服务。 |
| 系统级：带 `--system` 的在线安装、`sudo sh ./install.sh --system`，或手工 `sudo install` 到 `/usr/local/bin` | `sudo agent-peer update` | 只重启系统级服务。 |
| 用 `-ldflags` 以发布版本号从源码构建 | `agent-peer update` | 命令把二进制替换为发布包里的二进制。 |

从源码构建时不带 `-ldflags`，二进制报出的版本是 `dev`。它不是正式发布版本，命令会拒绝执行，并给出 `version "dev" is not a release build; only release builds can update themselves`。要就地更新，请安装正式发布版本。

二进制是符号链接时，命令会解析链接，替换它指向的文件。

## 关闭更新检查

要让 Broker 不再检查：

```sh
agent-peer config set update.check false
```

提示会立刻消失：`agent-peer status` 不再打印 `Update` 一行，会话也不再出现更新提示。无法访问 GitHub 的主机可以一直关着检查；只有你自己运行 `agent-peer update` 时，命令才会访问 GitHub。

[安全模型]({{< relref "/docs/security" >}})说明检查发出的网络访问。
