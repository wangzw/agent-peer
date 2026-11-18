# 发现并连接公开 Broker

## 参与者

主机 C 的用户，想与一台尚未配对的主机 A 上的 Agent 通信；主机 A 的用户配合完成配对。两人各自在本机有一个启用了本系统工具的 Claude Code 会话，并能批准该会话中的工具调用。两人的账户对各自会话所登记的 Broker 的配置文件有写权限。

## 前置条件

- A 的 [Broker](../../glossary.md) 与 B 的 Broker 已连接，C 的 Broker 与 B 的 Broker 已连接，A 与 C 尚未配对。
- A 的 Broker 已设为公开，按默认配置监听，C 能连上 A 默认路由所在接口的地址。
- A 上有一个 Agent 以公开方式加入了 topic `infra-help`，并附有简介。
- A 的用户与 C 的用户之间有一条系统之外的可信途径。

## 步骤

1. C 的用户要求 Agent 列出 Broker，看到 A 的别名、指纹和对外地址列表，状态为可连接，且不是对端。
2. C 的用户要求 Agent 列出 topic，看到公开 topic `infra-help` 及其简介。
3. C 的用户经系统之外的途径联系 A 的用户，请求配对。
4. A 的用户要求自己的 Agent 生成一次性配对密钥，批准后把密钥交给 C 的用户。
5. C 的用户要求 Agent 凭第 1 步看到的 A 的对外地址和这个密钥发起配对，批准后看到配对成功，结果中 A 的指纹与第 1 步看到的一致。
6. C 的用户要求 Agent 加入 `infra-help`。
7. C 的用户要求 Agent 列出可见的 Agent，看到 A 上加入了 `infra-help` 的 Agent。
8. C 的用户要求 Agent 向它发送消息，看到发送成功的结果。

## 结果

C 与 A 互为对端，并且已连接。C 上的 Agent 与 A 上同在 `infra-help` 的 Agent 互相可见，并能互发消息。

## 备选路径

- 第 5 步对方实际出示的指纹与第 1 步看到的不一致：C 的用户在配对结果中看到指纹不一致的警告，据此决定是否解除配对。
- A 的监听地址被配置为空：第 1 步看到 A 的对外地址列表为空，状态为不可直达。配对改由 A 的用户发起：C 的用户生成配对密钥，把它和 C 的 Broker 的地址交给 A 的用户，这个地址要能被 A 连上。

## 覆盖的需求

- `REQ-broker-discovery-002`
- `REQ-broker-discovery-003`
- `REQ-broker-discovery-012`
- `REQ-broker-discovery-013`
- `REQ-broker-discovery-014`
- `REQ-topics-001`
- `REQ-topics-005`
- `REQ-topics-007`
- `REQ-topics-010`
- `REQ-topics-011`
- `REQ-topics-013`
- `REQ-broker-pairing-005`
- `REQ-broker-pairing-010`
- `REQ-broker-pairing-023`
- `REQ-agent-messaging-008`
- `REQ-agent-messaging-013`

## 端到端测试

未实现
