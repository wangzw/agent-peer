---
title: 常见问题
weight: 8
---

## 如何安装 agent-peer？

这条命令安装最新版本，并为你的账户注册 MCP 服务；能询问时会问安装级别：

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

发布包解压后也有一个 `install.sh`，在解压后的目录里运行 `sh ./install.sh`，同样会询问级别；也可以手动安装二进制。[安装 agent-peer]({{< relref "guides/install.md" >}})介绍级别询问与各种安装方式，包括系统级安装。

主机上已经有你能使用的系统级 Broker 时，只需注册 MCP 服务，不要安装用户级 Broker；命令见[运行系统级 Broker]({{< relref "guides/system-broker.md" >}})。

## 如何卸载 agent-peer？

停止并移除服务，移除 MCP 注册，再删除二进制：

```sh
agent-peer service uninstall
claude mcp remove -s user peer
rm ~/.local/bin/agent-peer
```

用 `--prefix` 安装的，改为删除该目录下的二进制。

系统级安装用 `sudo agent-peer service uninstall --system` 移除服务，用同样的命令移除注册，再从系统级目录删除二进制，例如 `sudo rm /usr/local/bin/agent-peer`。移除服务时，配置、身份与审计日志都会保留；不再需要时另行删除。其他账户如果自己注册过 MCP 服务，也要用 `claude mcp remove -s user peer` 移除注册。

## 配置文件在哪里？

用户级 Broker 读 `~/.agent-peer/config.yaml`，系统级 Broker 读 `/etc/agent-peer/config.yaml`。`agent-peer service install` 在该级别的文件不存在时生成它。没有文件时，每个键都取默认值，Broker 照常运行。生成的文件与修改方法见[配置键]({{< relref "reference/configuration.md" >}})。

## 支持哪些平台？

macOS 与 Linux，支持 amd64 与 arm64。不支持原生 Windows。见[已知限制]({{< relref "limitations.md" >}})。

## 需要哪个版本的 Claude Code？

Claude Code v2.1.224 或更高版本。agent-peer 怎样依赖 Claude Code，见[已知限制]({{< relref "limitations.md" >}})。

## 有中心服务器吗？

没有。Broker 之间直接连接，消息不转发，也没有集中式注册中心。两台主机里至少有一台能连到另一台。见[链路]({{< relref "concepts/links.md" >}})。

## 需要手动启动 Broker 吗？

会话不会自己启动 Broker。主机上已经有你能使用的系统级 Broker 时，只需注册 MCP 服务，会话会连接到它。否则需要自己启动：用 `agent-peer service install` 把它安装为用户级服务，安装完成就会启动，此后随登录启动；另一种做法是在 Claude Code 之外的终端里运行 `agent-peer broker`。安装脚本已经默认安装并启动用户级服务，用它装好之后无需再做这一步。在会话里启动的 Broker 会以退出码 6 退出。见[把 Broker 安装为服务]({{< relref "guides/run-as-service.md" >}})。

## 没有 Broker 在运行时会怎样？

工具调用返回错误，说明该怎么做，具体提示分三种情形。第一次查找时用户级与系统级 Broker 都找不到，或者用户级 Broker 的 socket 消失、拒绝连接（例如被强杀之后），报的是同一条：先提示本机如有系统级 Broker 请管理员启动它，再给出两种用户级启动方法。原先连在系统级 Broker 上的会话断开后，只提示联系管理员。用户级 socket 还在应答，但握手或调用失败时，报告 Broker 不可用，并在后台重试，不给出启动方法。启动 Broker 之后，会话不必重新连接 MCP 服务。需要 Broker 的终端命令，例如 `agent-peer peer list`，做法相同：只查找正在运行的 Broker，不会启动它。见[命令行]({{< relref "reference/cli.md" >}})。

## 一个账户能运行多个 Broker 吗？

不能。一个账户至多运行一个 Broker，用另一个监听地址再启动一个会失败。见[Broker]({{< relref "concepts/broker.md" >}})。

## 升级后 Agent 地址为什么变了？

从 0.2.0 起，默认别名不再包含监听端口：主机名为 `workstation` 的系统级 Broker 报出 `workstation`，不再是旧形式 `workstation-17443`；账户 alice 的用户级 Broker 报出 `workstation.alice`。因此，从 0.1.0 升级、又没有设置 `broker.alias` 的 Broker 别名通常会变，对端看到的 Agent 地址也随之改变，除非对端已经为它设了本地别名；保存了旧地址的会话要改用新地址。对端遇到与已有别名冲突时会自行处理：为改了别名的 Broker 生成一个本地别名，原有地址不会因此指向另一个 Broker。想沿用旧别名，就在升级前把它读出来：用 `get_config` 工具读 `broker.alias`，或者从 `agent-peer status` 输出的 Agent 地址里取 `@` 后面的部分；升级后把配置文件里的 `broker.alias` 设回该值，例如 `workstation-17443`。默认端口下，旧别名是完整主机名加上 `-17443`，截短到 24 个字符。无论用哪种方式升级，包括在线安装、发布包里的 `install.sh`、`agent-peer update` 或手工替换二进制，情形都一样。

## 怎样更新 agent-peer？

在终端运行 `agent-peer update`，或在会话里调用 `update_broker`。系统级安装由管理组成员更新，不用 `sudo`；不在组内的账户用 `sudo agent-peer update`，或先加入管理组。同一账户还运行着用户级 Broker 时，用 `sudo agent-peer update` 到达系统级 Broker。更新会校验下载内容、替换二进制；作为服务运行的 Broker 会用新版本重启，没有在运行的 Broker，或者不在服务管理器下运行的用户级 Broker，下次启动时才用上新版本。已经打开的会话要用 `/mcp` 重连或重开。见[更新 agent-peer]({{< relref "guides/update.md" >}})。

## 能关闭更新检查吗？

能。运行 `agent-peer config set update.check false`，`Update` 一行和会话里的提示会立刻消失，之后不再检查。检查与更新的工作方式见[更新 agent-peer]({{< relref "guides/update.md" >}})，检查向 GitHub 发送什么见[安全模型]({{< relref "security.md" >}})。

## 两台主机如何开始互相信任？

两台主机里至少要有一台能连到另一台。在对方能连上的那台主机上用 `create_pairing_key` 或 `agent-peer pairing-key create` 生成一次性配对密钥，连同显示的地址一起，通过系统之外的途径交给另一台。另一台用 `connect_broker` 或 `agent-peer peer connect <address> --key <key>` 配对，使用该一次性密钥或接受方配置的预置静态密钥，此后双方各凭指纹认对方。见[配对]({{< relref "concepts/pairing.md" >}})与[两台主机配对]({{< relref "guides/pair-hosts.md" >}})。

## 什么是 topic？需要自己创建吗？

topic 是 Agent 之间可见与通信的范围。同一个项目里工作的会话通常共享一个默认 topic，它取自 `.agent-peer.yaml` 或 git origin 地址，所以不需要自己创建。见[topic]({{< relref "concepts/topics.md" >}})。

## 没有配对的主机能看到我的会话吗？

看不到。配对之后，一台主机也只能看到与自己的 Agent 同处一个 topic 的 Agent。Broker 默认不公开，除非你把它设为公开，否则连通网络里不会有它的名片。见[安全模型]({{< relref "security.md" >}})。

## Broker 之间的流量加密吗？

加密。链路使用 TLS 1.3 与自签名证书，Broker 的身份是它的公钥指纹。见[安全模型]({{< relref "security.md" >}})。

## 能发送文件或会话上下文吗？

不能。消息只传纯文本，文件、会话历史与上下文都不传。见[已知限制]({{< relref "limitations.md" >}})。

## 发送成功就代表对方读到了吗？

不代表。成功只表示消息已经写进目标会话的 inbox socket，并不能说明模型有没有读、有没有处理。投递请求发出后没有收到明确答复时，结果是“送达情况未知”。见[MCP 工具]({{< relref "reference/mcp-tools.md" >}})。

## 为什么重复发送的同一条消息不见了？

接收端的 Claude Code 会丢弃同一个 Agent 在 30 秒内发给同一个会话、与上一条内容完全相同的消息；这条规则属于 Claude Code，可能改动。接收端的 Broker 还会丢弃同一次发送中重试产生的副本，但两次分开的发送不会被去重。见[安全模型]({{< relref "security.md" >}})。

## 谁能修改 Broker 的配置？

对 Broker 配置文件有写权限的账户可以用 `set_config` 或 `agent-peer config set` 修改配置，用 `create_pairing_key` 或 `agent-peer pairing-key create` 生成一次性配对密钥，用 `connect_broker` 与 `disconnect_broker`，或 `agent-peer peer connect` 与 `agent-peer peer disconnect` 配对和解除配对；读权限允许读取配置和审计日志。终端命令按执行命令的账户判定权限。管理组成员在本账户没有运行自己的用户级 Broker 时，不用 `sudo` 就能管理系统级 Broker；管理员用 `sudo` 执行命令时，系统级 Broker 按 root 的权限判定。见[管理权限]({{< relref "guides/management-permissions.md" >}})，管理组的说明见[管理组]({{< relref "guides/management-permissions.md" >}}#管理组)一节。

## 端口被占用时会怎样？

Broker 启动失败，并报告该地址不可用。它不会另选地址或端口；腾出端口，或者修改监听地址。见[配置键]({{< relref "reference/configuration.md" >}})。

## 怎么看运行日志和审计记录？

用 `agent-peer status` 查看 Agent、topic、对端和链路，用 `agent-peer audit` 查看审计日志。把 Broker 安装为服务，运行日志由服务管理器保存：Linux 上用 `journalctl --user -u agent-peer.service`，系统级服务用 `journalctl -u agent-peer.service`；macOS 上用户级服务写入 `~/Library/Logs/agent-peer/broker.log`，系统级写入 `/var/log/agent-peer/broker.log`。在终端运行 `agent-peer broker`，日志就留在那个终端里。见[文件与路径]({{< relref "reference/files.md" >}})的“日志”一节。

## 无界面会话能收到消息吗？

以无界面方式运行且跳过权限确认的会话不能。接收方的 Claude Code 先把消息暂扣，随后因发送方没有声明权限类别而丢弃，而发送方那边已经返回成功。见[已知限制]({{< relref "limitations.md" >}})。
