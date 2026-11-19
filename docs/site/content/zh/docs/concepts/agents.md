---
title: Agent
weight: 2
---

Agent 是启用了 agent-peer 工具并登记到本机 Broker 的 Claude Code 会话。只有启用了工具的会话才参与：没有启用的会话不会出现在任何列表里，也不能通过 agent-peer 收到消息。

## 身份与名字

Broker 用会话的 inbox socket 识别 Agent，inbox socket 是 Claude Code 为该会话绑定的本机 socket。会话名决定 Agent 的名字：agent-peer 采用 Claude Code 自己的名字规范化规则，去掉控制字符与格式字符，去掉首尾空白，转为小写，并把空白换成 `-`。名字规范化后为空、经署名清洗后为空（接收方会去掉发送方名字里它读不出的字符，并截短到 64 个字符），或者含有 `@` 或 `*` 的会话不能成为 Agent。

Agent 地址由名字、可选的消歧标识、`@` 和所在 Broker 的别名依次组成。名字在该 Broker 上不足以唯一确定这个 Agent，或者名字放不进消息署名时，地址里会带上消歧标识。别名是看到该地址一方的 Broker 对另一个 Broker 使用的别名，所以同一个 Agent 在不同 Broker 上的地址可以不同。

## Agent 能做什么

Agent 可以列出对自己可见的 Agent、加入和退出 topic、列出 topic 和 Broker、发送消息；如果有相应的文件权限，还可以读取或修改 Broker 的配置、管理配对。可见范围由 [topic]({{< relref "/docs/concepts/topics" >}}) 决定：两个 Agent 至少同在一个 topic，且所在 Broker 相同或已连接时，才能互相看见。

消息只含纯文本。消息写入目标会话的 inbox socket 之后，发送才算成功，这不表示目标模型已经读过。在 Claude Code v2.1.327 上观测到，空闲的会话无需用户输入就会开始处理这条消息；不承诺兼容其他版本。

列表中的状态取自 Claude Code 的会话注册表：`idle`、`busy`、`waiting` 或 `shell`。

Broker 如何完成登记，见[Broker]({{< relref "/docs/concepts/broker" >}})；协作步骤见[在同一个 topic 里协作]({{< relref "/docs/guides/collaborate-in-topic" >}})；Agent 调用的工具见 [MCP 工具参考]({{< relref "/docs/reference/mcp-tools" >}})。
