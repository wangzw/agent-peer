---
title: 把 Broker 安装为服务
linkTitle: 把 Broker 安装为服务
weight: 4
---

Claude Code 会话只连接 Broker，不会启动它。启动 Broker 有两种办法：安装为服务，或者在终端里运行。安装为服务后，它立刻启动，此后随登录或开机启动，持续运行，机器重启后也会恢复。

## 先停掉自己在终端启动的 Broker

如果你在这个账户下用终端运行过 `agent-peer broker`，先在那个终端里停掉它，再安装服务。安装时如果已有 Broker 正在应答，而且不是这个服务启动的，安装会被拒绝，因为这个 Broker 占着账户锁。

## 安装用户级服务

主机上已经有你能使用的系统级 Broker 时，不需要安装用户级服务。

用拥有会话的账户运行安装命令：

```sh
agent-peer service install
```

命令写入单元文件或 plist，启用服务，并立即启动 Broker。它会等 Broker 应答，最多等 20 秒；Broker 没有应答时，安装失败，并给出服务日志的位置。重新安装会用新的二进制替换正在运行的 Broker。用户级安装不能用 root 运行，因为单元属于一个账户。位于临时目录里的二进制会被拒绝安装，用户级与系统级都如此。

在装有 systemd 的 Linux 上，单元文件是 `~/.config/systemd/user/agent-peer.service`（或位于 `$XDG_CONFIG_HOME` 下）。在 macOS 上，LaunchAgent 是 `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist`，安装只能在图形登录会话里执行；没有图形会话的主机改用系统级服务。

单元或 plist 以该账户身份运行 `agent-peer broker`。它在账户登录时启动，Broker 退出后重新启动。

## 安装系统级服务

系统级安装以 root 运行，让 Broker 在开机时启动。二进制必须放在 root 拥有、且组和其他人不可写的目录里，例如 `/usr/local/bin`。服务以 root 身份运行这个二进制，能改动它或其上级目录的普通账户可能借此接管服务：

```sh
VERSION=0.1.0
sudo install -o root -g 0 -m 0755 agent-peer-${VERSION}/agent-peer /usr/local/bin/agent-peer
sudo /usr/local/bin/agent-peer service install --system
```

Intel Mac 上用 Homebrew 时，`/usr/local/bin` 属于普通用户，安装会被拒绝；改选一个 root 拥有、组和其他人不可写的目录。

安装会检查二进制以及它上面的每一层目录，然后立即启动 Broker。

在 Linux 上，命令写入 `/etc/systemd/system/agent-peer.service`，启用并启动它；在 macOS 上写入并加载 `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`。系统级安装还会在不存在时创建 `/etc/agent-peer/config.yaml`，属主为 root，权限 0600。

## 卸载服务

用同一个账户运行 `agent-peer service uninstall`，可以卸载用户级服务。卸载系统级服务用 `sudo /usr/local/bin/agent-peer service uninstall --system`。命令停止 Broker 并删除服务定义；配置、身份与审计日志都保留。没有安装过服务时，命令照常成功，并报告未安装，所以可以放心重复执行。

## 检查服务

在终端运行 `agent-peer status`。输出包含一张 `Agents` 表和一张 `Peers` 表，没有内容时分别写 `Agents: none` 和 `Peers: none`。服务管理器里也能看到 Broker：`systemctl --user status agent-peer.service`、`systemctl status agent-peer.service`，或者用 `launchctl print` 查看标签 `io.github.wangzw.agent-peer`。

每个账户只保留一个 Broker。用同一个账户再启动一个 Broker 会失败，错误说明已有实例在运行，无论它要绑定哪个地址。
