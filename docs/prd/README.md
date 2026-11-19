# agent-peer 需求文档

本文档集描述 agent-peer 从外部看必须具备的行为：不同主机上的 Claude Code 会话如何互相发现、互发消息，以及承载这些通信的 Broker 如何配对、发现和配置。

## 索引

- [系统概览](overview.md)：系统是什么，对外提供哪些能力，整体范围边界
- [术语表](../glossary.md)：项目约定的术语，各类文档共用
- [引用](../references.md)：文档引用的公开外部来源

### 用户旅程

| 旅程 | 文档 |
|---|---|
| 两台主机首次配对 | [两台主机首次配对](journeys/journey-pair-brokers.md) |
| 在同一个 topic 里跨主机协作 | [在同一个 topic 里跨主机协作](journeys/journey-collaborate-in-topic.md) |
| 发现并连接公开 Broker | [发现并连接公开 Broker](journeys/journey-connect-public-broker.md) |

### 能力

| 能力 | 文档 |
|---|---|
| Agent 消息 | [Agent 消息](features/prd-agent-messaging.md) |
| topic | [topic](features/prd-topics.md) |
| Broker 配对与连接 | [Broker 配对与连接](features/prd-broker-pairing.md) |
| Broker 发现 | [Broker 发现](features/prd-broker-discovery.md) |
| Broker 配置、审计与运行 | [Broker 配置、审计与运行](features/prd-broker-config.md) |
| 自动更新 | [自动更新](features/prd-update.md) |
