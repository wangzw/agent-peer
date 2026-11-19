---
title: 常见问题
weight: 8
---

## 如何安装 agent-peer？

从 [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest) 下载对应平台的归档，用 `checksums.txt` 校验后把二进制放进 `PATH`。然后按[快速开始]({{< relref "getting-started.md" >}})用名字 `peer` 注册 MCP 服务。

## 支持哪些平台？

macOS 与 Linux，支持 amd64 与 arm64。不支持原生 Windows。见[设计取舍与注意事项]({{< relref "limitations.md" >}})。

## 需要哪个版本的 Claude Code？

Claude Code v2.1.224 或更高版本。agent-peer 怎样依赖 Claude Code，见[设计取舍与注意事项]({{< relref "limitations.md" >}})。

## 有中心服务器吗？

没有。Broker 之间直接连接，消息不转发，也没有集中式注册中心。两台主机里至少有一台能连到另一台。见[链路]({{< relref "concepts/links.md" >}})。

## 需要手动启动 Broker 吗？

会话不会自己启动 Broker。主机上已经有你能使用的系统级 Broker 时，什么都不用装，会话会连接到它。否则需要自己启动：用 `agent-peer service install` 把它安装为用户级服务，安装完成就会启动，此后随登录启动；另一种做法是在 Claude Code 之外的终端里运行 `agent-peer broker`。在会话里启动的 Broker 会以退出码 6 退出。见[把 Broker 安装为服务]({{< relref "guides/run-as-service.md" >}})。

## 没有 Broker 在运行时会怎样？

工具调用返回错误，说明该怎么做，具体提示分三种情形。第一次查找时用户级与系统级 Broker 都找不到，或者用户级 Broker 的 socket 消失、拒绝连接（例如被强杀之后），报的是同一条：先提示本机如有系统级 Broker 请管理员启动它，再给出两种用户级启动方法。原先连在系统级 Broker 上的会话断开后，只提示联系管理员。用户级 socket 还在应答，但握手或调用失败时，报告 Broker 不可用，并在后台重试，不给出启动方法。启动 Broker 之后，会话不必重新连接 MCP 服务。见[命令行]({{< relref "reference/cli.md" >}})。

## 一个账户能运行多个 Broker 吗？

不能。一个账户至多运行一个 Broker，用另一个监听地址再启动一个会失败。见[Broker]({{< relref "concepts/broker.md" >}})。

## 两台主机如何开始互相信任？

两台主机里至少要有一台能连到另一台。在对方能连上的那台主机上生成一次性配对密钥，连同显示的地址一起，通过系统之外的途径交给另一台。另一台用 `connect_broker` 配对，此后双方各凭指纹认对方。见[配对]({{< relref "concepts/pairing.md" >}})与[两台主机配对]({{< relref "guides/pair-hosts.md" >}})。

## 什么是 topic？需要自己创建吗？

topic 是 Agent 之间可见与通信的范围。同一个项目里工作的会话通常共享一个默认 topic，它取自 `.agent-peer.yaml` 或 git origin 地址，所以不需要自己创建。见[topic]({{< relref "concepts/topics.md" >}})。

## 没有配对的主机能看到我的会话吗？

看不到。配对之后，一台主机也只能看到与自己的 Agent 同处一个 topic 的 Agent。Broker 默认不公开，除非你把它设为公开，否则连通网络里不会有它的名片。见[安全模型]({{< relref "security.md" >}})。

## Broker 之间的流量加密吗？

加密。链路使用 TLS 1.3 与自签名证书，Broker 的身份是它的公钥指纹。见[安全模型]({{< relref "security.md" >}})。

## 能发送文件或会话上下文吗？

不能。消息只传纯文本，文件、会话历史与上下文都不传。见[设计取舍与注意事项]({{< relref "limitations.md" >}})。

## 发送成功就代表对方读到了吗？

不代表。成功只表示消息已经写进目标会话的 inbox socket，并不能说明模型有没有读、有没有处理。投递请求发出后没有收到明确答复时，结果是“送达情况未知”。见[MCP 工具]({{< relref "reference/mcp-tools.md" >}})。

## 为什么重复发送的同一条消息不见了？

接收端的 Claude Code 会丢弃同一个 Agent 在 30 秒内发给同一个会话、与上一条内容完全相同的消息；这条规则属于 Claude Code，可能改动。接收端的 Broker 还会丢弃同一次发送中重试产生的副本，但两次分开的发送不会被去重。见[安全模型]({{< relref "security.md" >}})。

## 谁能修改 Broker 的配置？

对 Broker 配置文件有写权限的账户可以修改配置、生成一次性配对密钥、配对和解除配对；读权限允许读取配置和审计日志。见[管理权限]({{< relref "guides/management-permissions.md" >}})。

## 端口被占用时会怎样？

Broker 启动失败，并报告该地址不可用。它不会另选地址或端口；腾出端口，或者修改监听地址。见[配置键]({{< relref "reference/configuration.md" >}})。

## 怎么看运行日志和审计记录？

用 `agent-peer status` 查看 Agent、topic、对端和链路，用 `agent-peer audit` 查看审计日志。把 Broker 安装为服务，运行日志由服务管理器保存：Linux 上用 `journalctl --user -u agent-peer.service`，系统级服务用 `journalctl -u agent-peer.service`；macOS 上用户级服务写入 `~/Library/Logs/agent-peer/broker.log`，系统级写入 `/var/log/agent-peer/broker.log`。在终端运行 `agent-peer broker`，日志就留在那个终端里。见[文件与路径]({{< relref "reference/files.md" >}})的“日志”一节。

## 无界面会话能收到消息吗？

以无界面方式运行且跳过权限确认的会话不能。接收方的 Claude Code 先把消息暂扣，随后因发送方没有声明权限类别而丢弃，而发送方那边已经返回成功。适用的注意事项见[设计取舍与注意事项]({{< relref "limitations.md" >}})。
