---
title: 把 Broker 安装为服务
linkTitle: 把 Broker 安装为服务
weight: 4
---

由会话启动的 Broker 在后台运行，但没有运行日志，也没有服务管理器负责启动或重新拉起它。把它安装为服务后，它会在登录或开机时启动、持续运行，并在机器重启后恢复。

## 先停掉正在运行的 Broker

安装服务不会停掉会话已经拉起的 Broker。它持有账户锁，服务启动时会以“已有实例在运行”的错误退出，服务管理器随后每 5 秒重试一次。还有会话开着 peer 工具时，光停掉 Broker 也不够：MCP 进程发现 socket 不在，约 250 毫秒后就会重新拉起一个 Broker。

按下面的顺序操作：

1. 退出本账户所有启用 peer 工具的会话，或在 `/mcp` 里停用 peer。
2. 停掉 Broker，并确认它已经停止。
3. 启动服务。
4. 重新打开会话。

会话拉起的 Broker 不由 systemd 或 launchd 管理，收到 `SIGTERM` 后退出。它的进程名与 `agent-peer mcp` 的进程相同，所以要按命令行查找：

```sh
pgrep -u "$USER" -f 'agent-peer broker'
```

对列出的进程发送 `SIGTERM`：

```sh
kill -TERM <pid>
```

Broker 以状态 0 退出，并删除本机 socket。`pgrep` 不再有输出、`agent-peer status` 报告连不上时，就表示它已经停止。账户锁文件仍然保留，但锁随进程退出释放，服务此后可以启动。安装系统级服务时，与系统级服务争锁的是 root 名下的 Broker，而不是运行 `sudo` 的账户名下的 Broker：以 root 身份运行同样的命令，也就是 `sudo pgrep -u root -f 'agent-peer broker'` 和 `sudo kill -TERM <pid>`。

## 安装用户级服务

用拥有会话的账户运行安装命令：

```sh
agent-peer service install
```

命令会写入单元文件或 plist，在 Linux 上还会启用它，但不会启动 Broker。输出的最后一行是立即启动它的命令。用户级安装不能用 root 运行，因为单元属于一个账户。位于临时目录里的二进制会被拒绝安装，用户级与系统级都如此。

在装有 systemd 的 Linux 上，单元文件是 `~/.config/systemd/user/agent-peer.service`（或位于 `$XDG_CONFIG_HOME` 下），启动命令是：

```sh
systemctl --user start agent-peer.service
```

在 macOS 上，LaunchAgent 是 `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist`，输出的启动命令是 `launchctl bootstrap gui/<uid> <plist 的绝对路径>`，其中 `<uid>` 换成你的数字用户 ID。

单元或 plist 以该账户身份运行 `agent-peer broker`。它在账户登录时启动，Broker 退出后重新拉起。在 macOS 上，随登录启动用户级 Broker 并恢复链路，尚待手工验证；见[已知限制]({{< relref "/docs/limitations" >}})。

## 安装系统级服务

系统级安装以 root 运行，让 Broker 在开机时启动。二进制必须放在 root 拥有、且组和其他人不可写的目录里：

```sh
sudo install -o root -g root -m 0755 agent-peer /usr/local/bin/agent-peer
sudo /usr/local/bin/agent-peer service install --system
```

安装会检查二进制以及它上面的每一层目录。

在 Linux 上，命令写入 `/etc/systemd/system/agent-peer.service` 并启用它；在 macOS 上写入 `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`。系统级安装还会在不存在时创建 `/etc/agent-peer/config.yaml`，属主为 root，权限 0600。

Linux 上的启动命令是：

```sh
sudo systemctl start agent-peer.service
```

macOS 上输出的命令是 `launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`。

系统级安装只在 Linux 上做过端到端验证。在 macOS 上需要安装后自行检查。

## 检查服务

在终端运行 `agent-peer status`。输出包含一张 `Agents` 表和一张 `Peers` 表，没有内容时分别写 `Agents: none` 和 `Peers: none`。服务管理器里也能看到 Broker：`systemctl --user status agent-peer.service`、`systemctl status agent-peer.service`，或者用 `launchctl print` 查看标签 `io.github.wangzw.agent-peer`。

每个账户只保留一个 Broker。用同一个账户再启动一个 Broker 会失败，错误说明已有实例在运行，无论它要绑定哪个地址。
