# inbox

`inbox` 是系统里唯一接触 Claude Code 内部格式的包。它在[会话注册表](../../glossary.md)中查找会话的记录，得到会话的名字、状态和 inbox socket 路径，监听注册表目录的变化；它向 inbox socket 写入一帧带署名的消息；它还提供名字规范化与署名清洗两项纯计算。注册表与帧的格式都没有公开的兼容承诺，把对它们的依赖收在一个包里，Claude Code 版本变化时只需改动这里。本包总是以所在进程的账户行事：在用户级 Broker 里它运行在 Broker 进程内；在系统级 Broker 里，查找、监听、重读与写入都运行在目标账户的辅助进程内，见 [broker](design-broker.md)。

## 职责

- 按调用方给出的 inbox socket 路径或进程号，在指定配置目录下的会话注册表中查找会话记录，返回会话的当前信息（`REQ-agent-messaging-001`、`REQ-agent-messaging-036`）。
- 监听会话注册表目录的文件变化，把变化了的记录告知调用方；按调用方的要求重读一条记录（`REQ-agent-messaging-005`、`REQ-agent-messaging-010`）。
- 向指定会话的 inbox socket 写入一条消息，以调用方给出的署名包装，并把结果区分为已写入、未写入和不确定（`REQ-agent-messaging-022`、`REQ-agent-messaging-023`）。
- 写入时不发送认证行（[ADR-inbox-delivery-without-auth-token](../../adr/adr-inbox-delivery-without-auth-token.md)，`REQ-agent-messaging-024`），不带会话标识（`REQ-agent-messaging-033`）。
- 提供不涉及 I/O 的计算：一条消息编码成帧后的字节数，会话名的规范化结果，一个署名经接收方清洗后的结果（`REQ-agent-messaging-003`、`REQ-agent-messaging-029`、`REQ-agent-messaging-037`）。
- 判断本进程是否运行在某个 Claude Code 会话的进程树内，供 `broker` 在启动时确认自己不是任何会话的后代。

### 非职责

- 不决定消息该不该投递，不做可见性检查和去重，由 [topic](design-topic.md) 与 [broker](design-broker.md) 负责。
- 不重试。写入的结果原样返回，由 `broker` 决定是否重试。
- 不接收会话发回的投递状态，不为远端 Agent 绑定任何 socket；系统不采用投递状态（[ADR-inbox-write-is-success](../../adr/adr-inbox-write-is-success.md)）。
- 不登记会话。由哪些会话参与由 MCP 服务进程的登记决定，见 [mcpserver](design-mcpserver.md)；会话名能否使用、Agent 是否成立，由 `broker` 判定。
- 不核对 inbox socket 的属主，由 `broker` 负责。
- 不切换账户，不派生辅助进程。系统级形态下以哪个账户的身份运行本包，由 [broker](design-broker.md) 决定并安排。
- 不读取同目录下各会话的密钥文件，不读取其他账户的会话注册表。
- 不决定署名的内容。署名由 `broker` 拼出后传入。

## 外部接口契约

接口只供 `broker` 调用，随 Claude Code 内部格式的变化而调整实现，签名保持稳定。

```go
// Lookup 在 configDir 下的会话注册表中查找会话记录，configDir 为空表示本账户的默认位置。
// 取 messagingSocketPath 与 socketPath 相同的记录；同一路径出现在多条记录中时，取修改时间最新的一条。
// 找不到记录时返回 ErrNoRecord。
func Lookup(configDir, socketPath string) (Session, error)

// Reread 重读 s 所在的那条记录。记录已删除时返回 ErrNoRecord；
// 内容无法解析时返回 ErrPartial，调用方保留上一次的值。
func Reread(s Session) (Session, error)

// Watch 监听 configDir 下的注册表目录，目录中的记录新增、改写或删除时，
// 以变化的记录所属的进程号回调；无法得知是哪条记录时以 0 回调。
func Watch(configDir string, onChange func(pid int)) (stop func(), err error)

// Deliver 以署名 sig 向会话写入一条消息。
// 返回 nil 表示已写入；ErrNotWritten 表示确定没有写入；ErrUncertain 表示帧可能已被接收；
// ErrBadSignature 表示 sig 不能原样被接收方识别，没有写入。
func Deliver(ctx context.Context, s Session, sig, text, msgID string, timeout time.Duration) error

// FrameSize 返回这条消息编码成帧后的字节数。
func FrameSize(sig, text string) int

// NormalizeName 返回会话名按 Claude Code 匹配会话名的规则规范化后的结果。
func NormalizeName(name string) string

// CleanSignature 返回 sig 经接收方清洗后的结果。
func CleanSignature(sig string) string

// InSession 报告本进程是否运行在某个 Claude Code 会话的进程树内。
func InSession() bool

type Session struct {
    ConfigDir  string // 记录所在的配置目录
    PID        int    // 会话进程号，即记录的文件名
    Name       string // 会话名，原样取自记录
    Status     string // 原样取自记录，可以为空
    SocketPath string // 取自记录，不自行拼接；绑定 inbox 之前为空
}
```

`Deliver` 返回 nil 的确切含义是：帧已写入 socket，接收方已关闭连接。之后该会话是立即处理、排队、留待用户批准还是丢弃，`inbox` 得不到这方面的信息，也不去查询；这与 `REQ-agent-messaging-015` 对成功的定义一致。入站控制放行的消息到达空闲会话时，会话自行开始处理（`REQ-agent-messaging-021`），这是 Claude Code 接收端的行为（[REF-claude-code-cross-session-messaging](../../references.md)）。

`Status` 的取值由 Claude Code 决定，已知的取值有 `idle`、`busy`、`waiting`、`shell`，其余取值与缺失由 `broker` 记为未知。

## 依赖

帧格式、署名包装与地址编码使用 claude-code-socket-transport（[REF-claude-code-socket-transport](../../references.md)，[ADR-reuse-socket-transport-library](../../adr/adr-reuse-socket-transport-library.md)），源码按引用文件登记的提交纳入本仓库的 `third_party` 目录，不随上游自动更新。该库实现的是依据 Claude Code v2.1.233 得出的协议版本 1。会话注册表不经该库读取，由本包自行读取。注册表目录的变化经操作系统的文件变化通知得知。`inbox` 不引用本项目的其他包；写入期限由调用方传入。

调用该库时固定下列选项：

| 选项 | 取值 | 作用 |
|---|---|---|
| `Client.NoAuth` | `true` | 不发送认证行，不读取目标会话的令牌文件（[ADR-inbox-delivery-without-auth-token](../../adr/adr-inbox-delivery-without-auth-token.md)） |
| `Client.Timeout` | 调用方给出的写入期限 | 单次写入的时间上限；实际期限取它与调用方上下文期限中较早的一个 |
| 发送接口 | 按 socket 地址发送 | 库不经注册表查找目标，也就不自动填入会话标识 |
| `Message.FromName` | 调用方给出的署名 | 接收方据此显示发送方 |
| `Message.From` | 空 | 不提供回复地址，不接收投递状态（[ADR-inbox-write-is-success](../../adr/adr-inbox-write-is-success.md)） |
| `Message.SessionID` | 空 | 不带会话标识：带了而与接收方当前的会话标识不一致的帧会被接收方静默丢弃，不通知发送方，而会话执行 `/clear` 后会话标识即变化；Claude Code 自带的发送工具也不带 |
| `Message.Priority` | `next` | 在下一个可处理的时机交给模型 |
| `Message.MsgID` | 调用方给出的消息标识 | 与链路上的消息标识一致，便于对照日志 |

## 核心算法与流程

### 查找会话记录

会话注册表位于配置目录下的 `sessions` 目录，每个会话进程一个文件，文件名是进程号加 `.json`。配置目录取调用方给出的值，为空时取本账户家目录下的 `.claude`。读取目录中的全部记录，取 `messagingSocketPath` 与所给 socket 路径相同的一条；同一路径出现在多条记录中时，取修改时间最新的一条。找不到时返回 `ErrNoRecord`。配置目录与 socket 路径都由 MCP 服务进程报出，本包不核实；它们只决定本账户读取自己的哪条记录，本包以本账户的身份读取，读不到其他账户不允许它读的内容。会话存续期间记录中的 `messagingSocketPath` 不变，绑定之前为空。

### 宽松解析

记录只依赖四个字段：`pid`、`name`、`status`、`messagingSocketPath`，其余字段一律忽略，字段缺失不算错误。Claude Code 改写记录时就地写入，不是原子替换，读取时可能遇到写了一半的内容：JSON 无法解析时返回 `ErrPartial`，调用方保留上一次的值，等待下一次变化通知。文件已不存在时返回 `ErrNoRecord`，调用方视为会话结束。

### 监听与重读

`Watch` 对每个配置目录只建立一个监听，回调变化了的记录所属的进程号。系统没有周期性的检查，记录的变化只经通知得知；调用方另在使用记录之前调用 `Reread` 按需重读，见 [broker](design-broker.md)。

### 名字规范化

`NormalizeName` 与 Claude Code 自带的发送工具匹配会话名之前的规范化相同：去掉控制字符与格式字符，去掉首尾空白，转为小写，空白换成 `-`。结果用作 Agent 的名字，结果是否可用由 `broker` 判定。

### 署名清洗与校验

接收方在识别署名包装之前清洗发送方的名字：去掉 `"`、`<`、`>` 与不可见字符，去掉首尾空白，超过 64 个码点时截断。清洗后的结果与原文不完全相同时，接收方不识别整个署名包装，消息照常交付，但看不到发送方是谁。`CleanSignature` 按接收方的这一规则计算，规则以在 Claude Code v2.1.327 上观测到的为准。依赖的库在写入前按自己的规则再清洗一次，超过 200 字节时截断。

`Deliver` 在写入之前校验署名：`CleanSignature(sig)` 与 `sig` 完全相同，且 `sig` 不超过 200 字节，才写入；否则返回 `ErrBadSignature`，不写入。通过校验的署名，库的清洗与接收方的清洗都不改变它。

### 进程树的判断

Claude Code 把所属会话的 inbox socket 路径放在它启动的命令与 MCP 服务进程的环境变量 `CLAUDE_CODE_MESSAGING_SOCKET` 中。`InSession` 在这个变量非空时报告本进程在某个会话的进程树内。MCP 服务进程拉起 Broker 时从 Broker 的环境中去掉这个变量，见 [mcpserver](design-mcpserver.md)。

### 写入

连接 socket，写入一行 JSON，半关闭，等待接收方关闭连接。接收方看到的正文形如：

```
<cross-session-message from-name="api-worker@hostA">
正文
</cross-session-message>
```

正文中出现的结束标签被转义。

写入结果的判定：连接失败，或在写出任何字节之前出错，是未写入；整行写出且接收方关闭了连接，是已写入；其余情形，包括写出之后等待关闭超时，是不确定。

### 帧大小

接收方在一行超过 1 MiB 仍未见换行时断开连接（REF-claude-code-socket-transport）。`FrameSize` 按实际的编码方式计算：署名包装后的正文作为 JSON 字符串编码，其中 `<`、`>`、`&`、引号、反斜杠和控制字符按转义后的长度计，其余字符按 UTF-8 的字节数计，再加上帧中其他字段的长度。`broker` 以它的结果与 1 000 000 字节的上限比较。

## 错误与失败语义

| 情形 | 返回 | 说明 |
|---|---|---|
| 注册表中没有符合条件的记录 | `ErrNoRecord` | 会话没有绑定 inbox，会话没有注册表记录，配置目录不对，或会话已结束 |
| 记录写了一半，无法解析 | `ErrPartial` | 调用方保留上一次的值 |
| 署名经接收方清洗后会改变，或超过 200 字节 | `ErrBadSignature` | 没有写入；这样的署名写入后，接收方照常交付消息但看不到发送方 |
| socket 无法连接，或写出之前出错 | `ErrNotWritten` | 可以重试 |
| 写出之后未能确认接收方关闭连接 | `ErrUncertain` | 帧可能已被接收，重试可能使会话收到两份 |
| 接收方读走了帧但随后丢弃 | nil | `inbox` 无从得知，包括入站策略拒收、超出限速额度、与同一发送方的上一条内容完全相同而被去重、排队已满；本系统写入的消息在接收方那里以写入进程识别发送方，规则与数值见 [ADR-no-broker-rate-limiting](../../adr/adr-no-broker-rate-limiting.md) |

写入不是幂等的：同一条消息写两次，会话收到两份，接收方的去重只在同一发送方 30 秒内连续发来相同内容时生效。系统的去重由 `broker` 在写入之前完成。

Claude Code 版本更新使帧格式或注册表格式不兼容时，表现为 `Lookup` 找不到记录，或 `Deliver` 返回 nil 而会话收不到消息。会话收不到消息的情形 `inbox` 无法自行发现，要靠用真实会话做的验证。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 会话记录的查找与宽松解析 | 未实现 | — |
| 注册表目录的监听与重读 | 未实现 | — |
| 消息写入与结果判定 | 未实现 | — |
| 帧大小、名字规范化与署名清洗的计算 | 未实现 | — |
| 进程树的判断 | 未实现 | — |
