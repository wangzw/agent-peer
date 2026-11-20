---
title: MCP 工具
weight: 1
---

Claude Code 会话经名为 `peer` 的 MCP 服务使用 agent-peer。用 `claude mcp add --scope user peer -- agent-peer mcp` 为账户注册一次。模型看到的工具名是 `mcp__peer__<tool>`，权限规则也按这个名字写，例如 `mcp__peer__send_message`。

## 工具

| 工具 | 作用 | 参数 | 配置文件权限 |
|---|---|---|---|
| `list_agents` | 列出本会话能看到的 Agent。 | `topic`（字符串，可选）：只列与本会话同在这个 topic 的 Agent。`broker`（字符串，可选）：只列别名为此值的 Broker 上的 Agent。`name`（字符串，可选）：只列名字为此值的 Agent。给出的筛选条件须全部匹配。 | 无 |
| `send_message` | 向一个 Agent 地址发送纯文本。 | `to`（字符串，必填）：地址，照抄消息发送方的地址。`message`（字符串，必填）：纯文本。 | 无 |
| `list_topics` | 列出本会话已加入的 topic，以及本 Broker 已知的公开 topic。 | 无。 | 无 |
| `join_topic` | 加入一个 topic。 | `name`（字符串，必填）。`public`（布尔，可选）：声明该 topic 公开，这要求 Broker 本身也是公开的。`description`（字符串，可选）：公开 topic 显示的简短说明。 | 无 |
| `leave_topic` | 退出本会话已加入的一个 topic。 | `name`（字符串，必填）。 | 无 |
| `list_brokers` | 列出本 Broker 已知的 Broker，包括对端状态、地址与指纹。 | 无。 | 无 |
| `connect_broker` | 与另一个 Broker 配对。 | `address`（字符串，必填）：写作 `host:port` 或 `hostname(host:port)`。`key`（字符串，必填）：对端的配对密钥。`local_alias`（字符串，可选）：对端自报别名被占用时改用的别名。 | 写 |
| `disconnect_broker` | 解除与一个对端 Broker 的配对。 | `broker`（字符串，必填）：对端的别名或 64 位十六进制指纹。 | 写 |
| `create_pairing_key` | 生成一次性配对密钥，并给出本 Broker 的地址。 | 无。 | 写 |
| `get_config` | 读取本 Broker 的配置。 | `key`（字符串，可选）：一个键，例如 `broker.alias`；省略则返回全部键。 | 读 |
| `set_config` | 修改一个配置键。 | `key`（字符串，必填）。`value`（字符串，必填）：新值的文本形式，例如 `10m`、`true` 或 `[a, b]`。 | 写 |

## 返回结果

### Agent

`list_agents` 返回 `self_address` 和 `agents`。每个 Agent 有 `address`、与本会话共有的 `shared_topics`、`status`，改名后十分钟内还有 `previous_name`。`status` 为 `idle`、`busy`、`waiting` 或 `shell`。

### 消息

`send_message` 返回 `result` 和 `to`。`result` 为 `delivered to the recipient's session (not necessarily read)`：成功只表示 Broker 把消息写进了接收方会话的 inbox socket，不表示对方 Agent 读过。确定没有送达时，调用返回以 `Not sent` 开头的错误。请求已经发出、但没有收到明确答复时，返回以 `Delivery unknown` 开头的错误，并提示重发可能让对方收到两份。

### topic

`list_topics` 返回 `joined_topics` 和 `public_topics`，后者每项有 `name` 和可选的 `description`。`join_topic` 与 `leave_topic` 返回更新后的列表 `topics`。

### Broker

`list_brokers` 返回 `brokers`。每项有 `alias`、小写十六进制的 `fingerprint`、`advertised_addresses`（每项含 `kind` 与 `addr`，只有 `direct` 一种）、`is_peer`、`status`（`connected`、`connectable` 或 `not_directly_reachable`）以及 `status_detail`。对端还有 `role`，取 `dialer` 或 `acceptor`，表示本 Broker 的角色；还有 `link_state`：`dialing`、`waiting`、`handshaking`、`established`、`backoff` 或 `rejected`。

`connect_broker` 返回对端的 `alias` 和 `fingerprint`，以及 `shown_host` 和可能的 `warnings`。`disconnect_broker` 返回 `result`。`create_pairing_key` 返回 `key`、RFC 3339 UTC 格式的 `expires_at`、`shown_addresses` 和可选的 `note`。地址只是给用户转交对端的，不属于密钥本身。

### 配置

`get_config` 返回 `entries`，每项有 `key` 和 `value`。密钥类键的值只显示 `(set)` 或 `(not set)`。`set_config` 返回该键的 `old_value` 和 `new_value`。

### 更新提示

每个 MCP 服务进程在第一次工具调用时向 Broker 查询更新状态，此后最多每 10 分钟查询一次。已知有更新的正式发布版本时，该次工具结果的末尾多出一段文本：

```text
agent-peer <v> is available (this host runs <c>). Update it with `agent-peer update` in a terminal, using sudo for a system-level installation.
```

`<v>` 是可用版本，`<c>` 是当前版本。同一个版本在一个会话中只提示一次；会话开着期间出了新版本，之后的工具调用会再提示一次。以工具错误结束的调用同样附带。查询失败或超时、Broker 是旧版而不支持该查询，或 `update.check` 关闭时，不附提示，工具结果保持原样。会话里没有安装更新的工具，更新要在终端运行 `agent-peer update`。

## 配置文件权限

表中的“无”“读”“写”指调用方账户对 Broker 配置文件的访问级别。账户取自操作系统为本机 socket 另一端进程提供的凭据，不采信调用方的陈述。

- 用户级 Broker 的配置文件属于本账户，所以每次调用都通过。
- 系统级 Broker 以该文件判定权限：读权限允许 `get_config`；写权限允许 `connect_broker`、`disconnect_broker`、`create_pairing_key` 和 `set_config`。管理员给某个组授予文件的相应权限，就把这些操作委托给了这个组。
- 消息与 topic 工具以及 `list_brokers` 不要求配置文件权限。

`list_agents`、`send_message`、`list_topics`、`join_topic` 和 `leave_topic` 代表会话行事，需要会话已经登记。发现、配对与配置类工具在登记完成之前也能使用。

## set_config 的限制

- `pairing.static_key` 是密钥类键，只能在配置文件里设置，`set_config` 会拒绝。
- Broker 以 `--listen` 运行时，`broker.listen` 不能修改，因为本次运行的监听地址由命令行固定。
- 值的类型必须与键相符。被拒绝的值不会改动配置。[配置键]({{< relref "docs/reference/configuration" >}})列出每个键及其约束。

这些工具背后的概念见[概念]({{< relref "docs/concepts" >}})；逐步操作见[使用指南]({{< relref "docs/guides" >}})。
