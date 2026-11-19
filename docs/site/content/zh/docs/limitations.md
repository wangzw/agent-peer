---
title: 设计取舍与注意事项
weight: 7
---

agent-peer 能做到本站其他页面介绍的事。本页说明这些做法背后的设计取舍，以及由此产生的注意事项。

## 设计取舍

- 消息只传纯文本。文件、会话历史与上下文不在协议之内，所以每条投递的消息都是一段接收方可以单独读懂和判断的文字。其他主机发来的文本怎么对待，见[安全模型]({{< relref "security.md" >}})。
- 消息不缓存、不持久化、不重投。一次发送要么写进目标会话的 inbox socket 并报告成功，要么在有限的等待后报告未发送，要么因为请求已经发出却没有收到明确答复而报告送达情况未知。没有需要回放、过期清理或对账的消息存储；发送失败或送达情况未知时，要不要重发由发送方 Agent 决定。见[链路]({{< relref "concepts/links.md" >}})。
- Broker 之间直接连接。没有中转，也没有集中式注册中心，所以没有第三方持有主机名单或看到流量；由此带来的要求是两台主机里至少有一台能连到另一台。见[链路]({{< relref "concepts/links.md" >}})与[配对]({{< relref "concepts/pairing.md" >}})。
- agent-peer 不与外部 A2A agent 互通。Broker 之间的消息体沿用 A2A 的 Message 结构作为消息格式，但 A2A 的任务、流式传输与回调不在设计之内：另一个 agent 发来消息，就是又一条消息。
- agent-peer 运行在 macOS 与 Linux 上，支持 amd64 与 arm64。不支持原生 Windows，因为那里的 inbox 连接强制要求认证行，而 agent-peer 不写认证行。见[安全模型]({{< relref "security.md" >}})。
- 一个账户至多运行一个 Broker。换一个监听地址再启动会失败，所以一个账户的 Agent、topic 与配置只有一个归属，也只有一个地方可查。账户与组取自系统的账户数据库：Linux 上 Broker 经 `getent` 按 NSS 配置查询，`getent` 不存在、执行失败或输出不合要求时，退回读取本地文件，并记一条警告。见[Broker]({{< relref "concepts/broker.md" >}})。
- agent-peer 依赖 Claude Code 内部的两个接口，Claude Code 没有公开承诺保持兼容：inbox socket 的消息格式与会话注册表。Claude Code 升级可能改变登记或投递的方式。
- agent-peer 不限制发送频率，不携带跳数，也不检测消息循环。接收端的 Claude Code 负责限速、去重和待处理消息的排队：它识别发送方，为发送方维护令牌桶，丢弃重复的消息，并对待处理消息排队。此外，接收端的 Broker 也按消息标识丢弃同一次发送中重试产生的副本。两层规则的完整说明见[安全模型]({{< relref "security.md" >}})，Claude Code 可以改动它那一层的规则。
- macOS 二进制没有签名，也没有经过公证。用浏览器下载的归档，运行前要清除隔离属性：

  ```sh
  VERSION=0.1.0
  xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer  # only if a browser downloaded the archive
  ```

  [快速开始]({{< relref "getting-started.md" >}})的安装一节介绍怎样从发布归档下载并安装二进制。
- 操作系统账户是隔离边界。同一账户下运行的任何进程，都能把该账户的任意会话登记为 Agent 并冒用它的身份；账户之间，用户级 Broker 拒绝其他账户的调用，系统级 Broker 只接受调用方账户所拥有的 inbox socket 的登记。见[安全模型]({{< relref "security.md" >}})。

## 注意事项

- 以无界面方式运行且跳过权限确认的会话，收不到 agent-peer 的消息。接收方的 Claude Code 先把消息暂扣，随后因发送方没有声明权限类别而丢弃，而发送方那边已经返回成功。没有声明权限类别意味着什么，见[安全模型]({{< relref "security.md" >}})。
- 有些会话不能成为 Agent：不绑定 inbox socket 的会话，Claude Code 没有为其保留会话注册表记录的会话（包括从另一个会话的命令行启动的交互式会话），以及名字不可用的会话。见[Agent]({{< relref "concepts/agents.md" >}})。
- 接收方处在跳过权限确认的模式且没有设置入站策略时，agent-peer 的消息要逐条批准后才放进会话。见[安全模型]({{< relref "security.md" >}})。
- 接收方会话正在处理一个回合时收到的消息，可能不会单独触发一个回合。
