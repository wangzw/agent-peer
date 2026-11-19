# 架构决策记录

本目录记录 agent-peer 中现行的架构决策。一个决定在难以逆转，或影响模块边界、对外接口、部署方式、外部依赖或安全性时，才在此留一条记录；每条记录写明促成决定的约束、考虑过的选项、采纳的选项和为此接受的代价。

## 拓扑与参与方

| 决策 |
|---|
| [Broker 之间只做直连](adr-direct-links-only.md) |
| [Broker 分系统级与用户级两种部署形态](adr-broker-deployment-modes.md) |
| [Broker 默认监听，地址冲突时启动失败](adr-listen-by-default.md) |
| [会话经 MCP 服务进程登记为 Agent](adr-register-through-mcp-server-process.md) |
| [Agent 以 inbox socket 为身份，名字取自会话名](adr-agent-identity-by-inbox-socket.md) |

## 消息投递

| 决策 |
|---|
| [发送同步完成，不缓存不补发](adr-synchronous-delivery.md) |
| [写入 inbox socket 即算发送成功](adr-inbox-write-is-success.md) |
| [写入 inbox socket 时不出示认证令牌](adr-inbox-delivery-without-auth-token.md) |
| [Broker 不限速，不检测消息循环](adr-no-broker-rate-limiting.md) |
| [链路上的消息体沿用 A2A 的 Message 结构](adr-a2a-message-body.md) |

## 信任与链路

| 决策 |
|---|
| [以配对密钥在自签证书之间建立信任](adr-pairing-key-trust.md) |
| [预置静态密钥取自配置文件](adr-static-pairing-key.md) |
| [链路采用 gRPC 双向流](adr-grpc-stream-links.md) |
| [对外地址默认取默认路由所在接口的地址](adr-advertised-address.md) |
| [发起配对的一方负责重新建立链路](adr-initiator-redials.md) |

## 可见性

| 决策 |
|---|
| [以 topic 限定 Agent 的可见性](adr-topic-scoped-visibility.md) |
| [默认 topic 取自项目声明或仓库地址](adr-default-topic-from-repository.md) |
| [以加盐摘要比对共同的 topic](adr-salted-topic-digest.md) |

## 发现

| 决策 |
|---|
| [Broker 默认不公开](adr-brokers-private-by-default.md) |
| [公开信息以签名记录逐跳对账传播](adr-signed-directory-reconciliation.md) |

## 配置

| 决策 |
|---|
| [Broker 配置经工具读写，确认交给 Claude Code](adr-config-through-tools.md) |
| [管理操作以调用方对配置文件的权限为准](adr-management-by-config-file-permission.md) |

## 外部依赖

| 决策 |
|---|
| [账户与组取自系统的账户数据库](adr-account-database-source.md) |
| [复用 claude-code-socket-transport 写入 inbox socket](adr-reuse-socket-transport-library.md) |
