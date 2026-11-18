# 在同一个 topic 里跨主机协作

## 参与者

主机 A 的用户与主机 B 的用户，各自在本机的同一个项目仓库里工作，并能批准各自会话中的工具调用。

## 前置条件

- A 的 [Broker](../../glossary.md) 与 B 的 Broker 已配对，并且已连接。两个 Broker 都按默认配置运行，没有配置别名，也没有设本地别名；两台主机的系统主机名分别是 `hostA` 与 `hostB`，因此 A 以别名 `hostB-17443` 称呼 B 的 Broker，B 以别名 `hostA-17443` 称呼 A 的 Broker。
- 两台主机上各有该项目仓库的工作区，两个工作区的 `origin` 远端指向同一个仓库，地址写法可以不同。
- 项目配置文件没有声明 topic，也没有关闭自动加入。

## 步骤

1. A 的用户在工作区中启动一个启用了本系统工具的会话，会话名为 `api-worker`。
2. B 的用户在工作区中启动一个启用了本系统工具的会话，会话名为 `frontend`。
3. A 的用户要求 Agent 列出可见的 Agent，看到 `frontend@hostB-17443` 以及共同所在的 topic，该 topic 以仓库地址命名。
4. A 的用户要求 Agent 向 `frontend@hostB-17443` 发送一条消息，说明某个接口已经变更。
5. A 的用户看到发送成功的结果。
6. B 的用户看到自己空闲的会话自行开始处理一条署名为 `api-worker@hostA-17443` 的消息。
7. B 的会话处理完后向 `api-worker@hostA-17443` 发送回复。
8. A 的用户看到自己的会话收到一条署名为 `frontend@hostB-17443` 的消息。

## 结果

两台主机上的会话未经任何 topic 操作即互相可见，一条消息及其回复分别进入了对方的会话，每条消息都带有发送方的 Agent 地址。

## 备选路径

- B 的会话在第 6 步正忙：消息在该会话当前工作的间隙或结束后被读到，A 在第 5 步看到的结果不变。
- B 的会话的入站策略是留待用户批准：A 在第 5 步仍看到成功，B 的用户看到一条待批准的消息；B 的用户不批准时，A 不会收到任何通知，也不会收到回复。
- 两个工作区的 `origin` 指向不同的仓库：第 3 步的列表不含对方。任一方的用户经系统之外得知对方 topic 的名字，要求 Agent 加入该 topic 后，从第 3 步继续。

## 覆盖的需求

- `REQ-agent-messaging-001`
- `REQ-agent-messaging-003`
- `REQ-agent-messaging-004`
- `REQ-agent-messaging-008`
- `REQ-agent-messaging-009`
- `REQ-agent-messaging-013`
- `REQ-agent-messaging-015`
- `REQ-agent-messaging-016`
- `REQ-agent-messaging-021`
- `REQ-agent-messaging-022`
- `REQ-agent-messaging-024`
- `REQ-agent-messaging-025`
- `REQ-topics-001`
- `REQ-topics-005`
- `REQ-topics-007`
- `REQ-topics-016`
- `REQ-topics-017`
- `REQ-broker-pairing-002`

## 端到端测试

`test/e2e/journey_collaborate_test.go` 的 `TestJourneyCollaborateInTopic`

以上备选路径中，“B 的会话的入站策略是留待用户批准”不在本旅程的端到端测试中；它依赖真实 Claude Code 会话，相关验证留待用户批准。
