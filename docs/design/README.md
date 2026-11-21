# peers 设计文档

peers 让不同主机上的 Claude Code 会话互相发现并发送文本消息。

## 索引

- [架构](architecture.md)：系统整体结构、跨切面设计与架构不变量
- [架构决策记录](../adr/README.md)：现行的架构决策及各自考虑过的选项与代价
- [术语表](../glossary.md)：项目约定的术语，各类文档共用
- [引用](../references.md)：文档引用的公开外部来源

### 模块

| 模块 | 文档 |
|---|---|
| `broker` | [broker](modules/design-broker.md) |
| `mcpserver` | [mcpserver](modules/design-mcpserver.md) |
| `inbox` | [inbox](modules/design-inbox.md) |
| `topic` | [topic](modules/design-topic.md) |
| `link` | [link](modules/design-link.md) |
| `pairing` | [pairing](modules/design-pairing.md) |
| `directory` | [directory](modules/design-directory.md) |
| `config` | [config](modules/design-config.md) |
