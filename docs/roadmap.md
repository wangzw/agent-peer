# 路线图

本文件记录需求的优先级、里程碑的划分和实现过程中要验证的事项。需求本身见 [需求文档](prd/README.md)，设计见 [设计文档](design/README.md)。需求的实现状态以需求文档中每条需求的状态行所写为准，本文件不重复记录。

## 优先级

| 级别 | 含义 |
|---|---|
| P0 | 首期必须交付，缺少它系统不能完成跨主机的一次收发 |
| P1 | 首期交付，但不阻塞 P0 的验收 |
| P2 | 首期之后 |

## 里程碑

每个里程碑以所列的用户旅程或验收条件通过为完成标志。

| 里程碑 | 优先级 | 内容 | 涉及的模块 | 完成标志 |
|---|---|---|---|---|
| M0 可行性验证 | P0 | 用手工写入 inbox socket 的方式确认方案的前提成立 | 无 | 已完成，结论见下文 |
| M1 同机闭环 | P0 | 用户级 Broker 的启动、单实例与监听，MCP 服务进程对 Broker 的选定与登记，Broker 本机接口，inbox 写入，本机 topic，同一主机上经本系统工具列出 Agent 并收发消息 | `config`、`inbox`、`broker`、`mcpserver`、`topic` | 同一主机上处于同一默认 topic 的两个会话经 `send_message` 互发消息并回复 |
| M2 Broker 互联 | P0 | 身份与配对、链路的建立与重建、成员名单交换、跨主机发送与重试、解除配对、列出对端及其状态 | `pairing`、`link`、`topic`、`broker`、`mcpserver`、`config`、`directory` | 用户旅程“两台主机首次配对”和“在同一个 topic 里跨主机协作”通过，验收时其中一台主机的对外地址不能被对方连上 |
| M3 发现与配置 | P1 | 名片与公开 topic 的发布和对账、列出非对端的 Broker、配置读写及其权限判定、审计日志、终端命令 | `directory`、`config`、`mcpserver`、`broker`、`topic`、`pairing` | 用户旅程“发现并连接公开 Broker”通过 |
| M4 运行与收尾 | P1 | 系统级 Broker（服务多个账户、向其他账户的会话投递、限定可使用的账户）、用户级 Broker 安装为随登录启动的服务、版本不兼容的处理、完成尚无结论的待验证事项 | `broker`、`mcpserver`、`inbox`、`config` | 待验证事项全部有结论，需求文档中每条需求的状态为已实现 |

各里程碑覆盖的需求：

| 里程碑 | 需求 |
|---|---|
| M1 | `REQ-agent-messaging-001` 至 `007`、`009` 至 `011`、`013` 至 `016`、`018`、`021` 至 `028`、`030` 至 `038`；`REQ-topics-001` 至 `004`、`006`、`008`、`015` 至 `022`；`REQ-broker-config-011`、`013`、`014`、`019`、`023` 至 `025`、`028`、`038` |
| M2 | `REQ-broker-pairing-001` 至 `022`、`024` 至 `037`；`REQ-agent-messaging-008`、`012`、`017`、`019`、`020`、`029`；`REQ-topics-005`、`007`、`009`、`014`；`REQ-broker-discovery-012` 至 `014`；`REQ-broker-config-030` 至 `033`、`035`、`036` |
| M3 | `REQ-broker-discovery-001` 至 `011`、`015` 至 `017`；`REQ-broker-pairing-023`；`REQ-topics-010` 至 `013`、`023` 至 `025`；`REQ-broker-config-001` 至 `010`、`017`、`021`、`022`、`029`、`034` |
| M4 | `REQ-broker-config-015`、`016`、`018`、`020`、`026`、`027`、`037` |

自动化测试不启动 Claude Code，inbox socket 与会话注册表一律用模拟实现。用真实会话做的验证按待验证事项表所列的阶段手工进行。

## M0 的结论

验证于 2026-11-18 在 macOS、Claude Code v2.1.327 上完成，目标会话以 auto 权限模式运行。

- 向空闲会话的 inbox socket 写入一帧不带认证行、不带回复地址、只带署名的消息，会话被唤醒并开始处理。
- 会话中的模型读到的发送方是署名的原文，例如 `spike@hostB`；界面的预览行把它显示为 `@spike-hostB`。
- 连续写入 15 条不同的消息，全部按顺序送达，没有触发限速。会话忙碌时到达的消息被攒到下一个回合一并交付。
- 两条正文相同、消息标识不同的消息都被送达，接收方没有去重。
- 上面两项实验每次写入各用一个独立的进程。接收方在帧里没有回复地址时按写入进程识别发送方，所以这两项实验没有检验到同一个发送方的限速与去重，Broker 以同一个进程写入时的行为见“Claude Code 行为的观测记录”。
- 会话注册表中每个会话有一份记录，含会话名、名字来源、inbox socket 路径、忙闲状态和协议版本号，协议版本号为 1。
- 没有启用本系统工具的会话中，模型打算用 Claude Code 自带的发送工具向带 `@` 的发送方地址回复。

## Claude Code 行为的观测记录

除“方式”一栏另注版本的以外，下列行为在 Claude Code v2.1.327 上观测到，没有兼容承诺。“方式”一栏写明结论得自实验还是对 Claude Code 程序的分析。

| 行为 | 方式 |
|---|---|
| Claude Code 在会话启动时启动配置的 stdio MCP 服务进程，它是会话进程的直接子进程 | 实验 |
| MCP 服务进程握手超过 30 秒未完成或进程启动失败时，交互式会话把该服务标为失败，要用户在 `/mcp` 界面手动重连；stdio 服务崩溃后交互式会话不自动重启它 | 程序分析 |
| MCP 服务进程的环境中有 `CLAUDE_CODE_MESSAGING_SOCKET`，值是所属会话的 inbox socket 路径 | 实验 |
| 会话启动时先清除 `CLAUDE_CODE_MESSAGING_SOCKET`，绑定 inbox 成功后才设置：不绑定 inbox 的会话里没有它，也不沿用外层会话的值；inbox 的绑定晚于 MCP 服务进程启动时，该进程的环境里没有它 | 程序分析；实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 环境中的 `CLAUDE_CODE_SESSION_ID` 是进程启动时的会话标识，会话执行 `/clear` 后不更新 | 实验，并经程序分析印证 |
| 子代理与主会话共用同一个 MCP 服务进程和同一条连接，工具调用不带调用者的信息；子代理定义中内联声明的服务在子代理运行期间另起一个进程 | 共用进程与连接、调用不带调用者信息：实验；内联声明另起进程：实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 会话退出时，Claude Code 向 MCP 服务进程发送 SIGINT | 实验 |
| 用户在 `/mcp` 界面停用一个服务时，Claude Code 向该进程依次发送 SIGINT、100 毫秒后 SIGTERM、再 400 毫秒后 SIGKILL；重新启用时启动新进程 | 停用时发送 SIGINT、重新启用时启动新进程且环境重新构造：实验；后续信号及间隔：实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 会话改名、`/clear`、`/compact` 时，MCP 服务进程不重启，也收不到任何通知 | 改名与 `/clear`：实验；`/compact`：程序分析 |
| 会话注册表位于配置目录下的 `sessions/<pid>.json`，配置目录取 `CLAUDE_CONFIG_DIR`，缺省为 `~/.claude`；每个会话进程一个文件，会话启动时写入，名字、状态、会话标识变化时就地改写而不是原子替换，进程退出时删除；同目录下另有各会话的密钥文件 | 程序分析 |
| 会话注册表记录中的字段包括 `pid`、`name`、`status`、`messagingSocketPath`，`status` 取 `idle`、`busy`、`waiting`、`shell`；`messagingSocketPath` 在绑定 inbox 之后才有 | 记录的字段与 `idle`、`busy`、`waiting` 三个取值：实验，见“M0 的结论”；其余：程序分析 |
| 无界面的 `claude -p` 会话与交互式会话同样登记并绑定 inbox | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 以无界面方式运行且跳过权限确认的会话对未声明权限类别的发送方暂扣消息，没有批准界面，暂扣的消息因发送方没有声明权限类别而被丢弃 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311，headless 探查） |
| `/mcp` 列表用 `✔ peer   N tools` 表示服务可用，不打印 `connected` | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 会话记录中以 `origin.name` 字段记下识别出的发送方名字；界面预览行对名字另做一次显示用清洗 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 从另一个会话的命令行里启动的交互式会话不写会话注册表 | 程序分析 |
| `claude agents --json` 读取会话注册表，输出中没有 inbox socket 路径 | 输出中没有 inbox socket 路径：实验；数据来自会话注册表：程序分析 |
| `claude agents --json` 每次调用都执行完整的启动准备，并删除已退出会话遗留的记录与密钥文件 | 程序分析 |
| 会话名由用户指定或由 Claude Code 自动生成；改名后，自带的发送工具用旧名字寻址即失败并提示相近的名字，自带的会话列表注明该会话不久前的名字；改名后与另一个存活会话重名时，其中一个自动改用带后缀的名字 | 用旧名字寻址失败并提示相近的名字、列表注明不久前的名字：实验；重名时自动加后缀：程序分析 |
| 自带的发送工具匹配名字前先规范化：去掉控制字符与格式字符，去掉首尾空白，转为小写，空白换成 `-`；自带的会话列表不显示名字中含 `@` 或 `*` 的会话 | 程序分析 |
| 自带会话列表中方括号里的标识，是对 `session:` 接 socket 路径求 SHA-256 后取十六进制前 12 位，再取能在全部候选之间区分的最短前缀，至少 6 位；它不随改名和 `/clear` 变化；列表工具名为 `ListAgents` | 算法：程序分析，并以本机会话的 socket 路径复算，与列表中显示的标识相符；不随改名和 `/clear` 变化：实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| `/clear` 不改变 socket 路径、会话名和上述标识，只更换会话标识并清空对话；此前已到达而尚未处理的会话间消息保留，进入清空之后的对话 | 不改变 socket 路径、会话名与标识：实验（GitHub 托管的 Linux x64 runner，v2.1.311），并经程序分析印证；此前已到达而尚未处理的消息保留：实验（v2.1.327），v2.1.311 上无法验证 |
| 不带会话标识的帧被会话接收；帧里带有会话标识而与接收方当前的不一致时，帧被静默丢弃，不通知发送方；自带的发送工具发出的帧不带会话标识 | 不带会话标识的帧被接收：实验，见“M0 的结论”；其余：程序分析 |
| 署名包装中的发送方名字在识别前被清洗：去掉 `"`、`<`、`>`，去掉 `Cc`、`Cf`、`Cs`、`Zl`、`Zp` 字符与首尾空白，超过 64 个码点时截断；清洗结果与原文不完全相同时，整个署名包装不被识别，消息照常交付，但接收方看不到发送方。v2.1.311 保留中间的 U+00A0、私用区码点、未分配的 U+0378 与可打印的 U+3164 | 程序分析（v2.1.327），并经实验确认（GitHub 托管的 Linux x64 runner，v2.1.311）；首尾空白一项的实验只测了首部 |
| 依赖库以 Go 的 `%q` 写署名，接收方读到的是转义后的字面文本；本系统的 `Deliver` 在交给库之前调用 `CleanSignature` 并拒绝会被改变的署名（`internal/inbox/deliver.go` 的 `Deliver`） | 程序分析，并经实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 限速与去重按发送方进行，帧里有回复地址时以回复地址识别发送方，没有时以写入进程的进程号识别；每个发送方一个令牌桶，容量 30，每秒补充 0.5 个；同一发送方 30 秒内发来与上一条内容完全相同的消息时，后一条被丢弃；未处理的会话间消息至多排队 50 条；这些数值可由 Claude Code 远程调整 | 程序分析，并经实验（GitHub 托管的 Linux x64 runner，v2.1.311）；排队上限按队列峰值深度观察 |
| 会话回合进行中入队的会话间消息在回合结束时以 remove 结束，不成为用户记录；remove 表示丢弃还是并入当前回合的上下文，无法区分 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| `crossSessionInbound` 取 `accept` 时消息直接送达，取 `hold` 时暂扣并显示提示，取 `refuse` 时丢弃 | 程序分析，并经实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 以跳过权限确认的模式运行的会话，把未声明权限类别的发送方发来的消息留待逐条批准；default 模式下同样的消息直接送达 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| Linux 上接收方以 `SO_PEERCRED` 取写入方进程号，沿 `/proc/<pid>/stat` 的父进程号向上判定是否为自己的子进程，层数上限为 12；11 层以内直接送达，13 层受入站控制 | 程序分析，并经实验（GitHub 托管的 Linux x64 runner，v2.1.311）；实测 11 层被接受、13 层被暂扣 |
| 入站策略为 `hold` 时，发送方立即返回成功；接收方只显示一条暂扣提示，没有逐条批准界面，按 Enter 不投递，`/clear` 后暂扣的消息丢失 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 脚本在工具执行期间键入 `/clear` 并按 C-x C-s 之后，工具被中断，入队的 peer 消息先作为回合处理，然后 `/clear` 才生效 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311） |
| 启用本系统的工具后，模型对会话间消息的回复用 `send_message` 原样以署名中的 Agent 地址为目标，不使用 Claude Code 自带的发送工具 | 实验（GitHub 托管的 Linux x64 runner，v2.1.311；claude-sonnet-5-5、claude-haiku-5-5） |
| 自带消息的循环检测依赖署名包装里记录的经过路径 | 程序分析 |

## 待验证事项

下列事项不影响方案是否成立，在实现过程中用真实会话确认，结论回写到相关的设计文档。

| 事项 | 关系到 | 计划阶段 | 结论 |
|---|---|---|---|
| 以跳过权限确认的模式运行的会话是否把消息留待用户批准 | `REQ-agent-messaging-024` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：以跳过权限确认的模式运行且未设置入站策略的会话，把 Broker 写入的消息留待逐条批准，批准后才开始回合。 |
| Broker 脱离会话的进程树之后，它写入的消息是否不再被任何会话当作自己的子进程所发，入站控制是否照常生效 | `REQ-agent-messaging-024` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：Broker 的父链中没有 Claude Code 进程，环境中没有 socket 路径；它写入的消息按非子进程处理，入站控制照常生效。 |
| Linux 上接收方如何判定写入方是自己的子进程 | `REQ-agent-messaging-024` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：接收方以 `SO_PEERCRED` 取写入方进程号，沿 `/proc/<pid>/stat` 的父进程号向上至多 12 层判定；11 层写入方被接受，13 层写入方被暂扣。 |
| 安装本系统的 MCP 服务并给出使用说明之后，模型是否改用 `send_message` 回复 Agent 地址；不够时在正文前加一行提示 | `REQ-agent-messaging-025` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：claude-sonnet-5-5 与 claude-haiku-5-5 对每条消息都用 `send_message` 原样以署名地址回复，不需要在正文前加提示。 |
| inbox 的绑定晚于 MCP 服务进程启动、使其环境中没有 socket 路径的情形出现的频率 | `REQ-agent-messaging-001` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：30 次启动（20 次交互式，其中 10 次带负载；10 次无界面）中，会话注册表出现 socket 路径后，在等待注册记录出现的上限之内，MCP 服务进程的环境里都有同名路径，没有出现缺路径的窗口。 |
| 子会话是否沿用父会话的 `CLAUDE_CODE_MESSAGING_SOCKET`；沿用会使子会话的登记归入父会话的 Agent | `REQ-agent-messaging-001`、`REQ-agent-messaging-035` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：子会话使用自己的 socket，注册记录与 MCP 服务进程环境中的路径都是子会话的，不沿用父会话的值。 |
| 会话注册表目录的文件变化通知在 macOS 与 Linux 上是否可靠，会话改名后本系统列出的名字是否随之更新 | `REQ-agent-messaging-005`、`REQ-agent-messaging-010` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：10 次改名与 10 次退出都在 1 秒内反映到本系统的列表中，改名后以新名字列出并带旧名，watch 的 misses 为 0；Broker 持有 2 个 inotify 实例，限额为 1280。macOS 无法验证：探查只在 GitHub 托管的 Linux x64 runner 上运行，没有可用的 macOS 主机。 |
| 以实验确认 `/clear` 之后 inbox socket 路径、会话名与会话列表中的标识都不变，此前已到达而尚未处理的消息进入清空之后的对话 | `REQ-agent-messaging-006`、`REQ-agent-messaging-033` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：`/clear` 之后 inbox socket 路径、会话名与会话列表中的标识都不变，其他 Agent 看到的地址与共同 topic 也不变；此前已到达而尚未处理的消息没有丢失：在工具执行期间键入 `/clear` 并按 C-x C-s 之后，入队的消息先作为回合被处理，`/clear` 随后才生效。 |
| 以实验确认会话列表中的标识与本系统按同一算法算出的消歧标识相同 | `REQ-agent-messaging-006` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：会话列表中的标识与本系统按同一算法算出的消歧标识相同，`a<b` 一项两处都是 `dd91b0`；列表工具为 `ListAgents`。 |
| 以实验确认署名清洗的规则：含 `"`、`<`、`>` 或超过 64 个码点的署名不被识别；本系统生成的、带消歧标识并截短了名字的署名原样到达会话，模型按它回复时能够解析 | `REQ-agent-messaging-022`、`REQ-agent-messaging-025`、`REQ-agent-messaging-037` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：含 `"`、`<`、`>` 或超过 64 个码点的署名不被接收方识别；系统生成的、带消歧标识并截短了名字的署名原样到达会话，模型按它回复时地址能够解析。 |
| 以实验确认停用 MCP 服务时 MCP 服务进程依次收到 SIGINT、SIGTERM，重新启用时启动新进程 | `REQ-agent-messaging-034` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：停用 MCP 服务时进程依次收到 SIGINT、约 100 毫秒后 SIGTERM、约 401 毫秒后被 SIGKILL；重新启用后启动新进程，注销与重新登记以相同身份完成。 |
| 以实验确认接收方的限速与去重：经同一个 Broker 写入的消息共用一个令牌桶，容量 30，每秒补充 0.5 个；同一写入进程 30 秒内发来与上一条内容完全相同的消息时后一条被丢弃；未处理的会话间消息至多排队 50 条 | Agent 消息的假设与依赖 | M2 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：令牌桶容量 30、每秒补充约 0.5 个，同一写入进程 30 秒内相同内容的后一条被丢弃；未处理消息的排队上限按队列峰值深度计为 50，回合进行中入队的消息被 Claude Code remove，未成为用户记录。 |
| 以实验确认 `CleanSignature` 去掉的不可见字符集与真实接收方一致 | `REQ-agent-messaging-022`、`REQ-agent-messaging-029`、`REQ-agent-messaging-037` | 真实会话验证 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19）：接收方去掉的字符集是本系统 `CleanSignature` 去掉的字符集的子集，本系统多删的方向安全；v2.1.311 保留而本系统删除的有中间的 U+00A0、U+E000、U+0378、U+3164 与反斜杠。`CleanSignature` 的规则依据是 v2.1.327 的程序分析。 |
| Broker 不在时 MCP 服务是否保持连接，以及手工启动 Broker 后是否无需重连即恢复 | `REQ-agent-messaging-038` | M1 | 已确认（GitHub 托管的 Linux x64 runner，Claude Code v2.1.311，2026-11-19，probe/broker-down 运行 44510569953）：占用 `127.0.0.1:27443` 后，终端启动 Broker 以退出码 4 失败并指出该地址不可用；会话启动后主机上没有 Broker 进程，`/mcp` 显示 peer 可用；工具调用返回没有可用 Broker 的错误并给出管理员与用户级两种启动方法；释放地址并手工启动 Broker 后，同一个 MCP 服务进程恢复，`/mcp` 仍显示可用。 |
| macOS 上 LaunchAgent 启动用户级 Broker | `REQ-broker-config-015` | M4 | 已确认（GitHub 托管的 macOS runner，2026-11-19）：`make e2e-darwin` 的用户级用例验证安装后 Broker 立即运行、`agent-peer status` 可连，卸载后进程消失、plist 删除；`TestUserServiceRestoresLinkAfterRelogin` 以 bootout/bootstrap 模拟登录，断言出现新 pid 且对端链路恢复为已连接。 |

Claude Code 每次升级后，核对 inbox 帧格式与会话注册表的字段是否仍与观测记录一致。

## 首期之后

- 与外部 A2A Agent 互通，限于消息的收发：外部 Agent 的回复是另一条消息，不包括 A2A 的任务、流式与回推。首期只在 Broker 之间的消息体上沿用 A2A 的 Message 结构，设计中把消息送达不在本机的 Agent 的方式与对端成员名单的来源预留为扩展点（[broker](design/modules/design-broker.md)、[topic](design/modules/design-topic.md)）。
- 原生 Windows。
- 两台主机都不可达时的互通。注册服务由一台长期在线的公开 Broker 承担，沿用名片的发布与传播，不需要新的机制。中转在连接层进行，被中转的两个 Broker 之间仍是端到端认证的链路，不在消息层转发；设计中把链路所依托的连接怎样建立预留为扩展点（[link](design/modules/design-link.md)）。
- 由团队证书颁发机构签发证书的信任方式，作为配对密钥之外的另一种信任来源。
- 文件与会话上下文的传递。
- 分布式追踪与指标导出。
