# mcpserver

`mcpserver` 是每个会话一个的[MCP 服务进程](../../glossary.md)，由 Claude Code 经标准输入输出启动。它向模型声明本系统的全部工具（[REF-mcp](../../references.md)），在后台选定本机的一个 Broker 并把所在会话登记为 Agent，把每次调用转成对它的一次请求。它不保存权威的业务状态，可以把它看作模型与 Broker 之间只做转交的一层，外加一条维持登记的连接。

## 职责

- 声明工具的名字、参数、只读属性和说明文字，并提供整个MCP 服务的使用说明（[ADR-config-through-tools](../../adr/adr-config-through-tools.md)，`REQ-broker-config-003` 至 `REQ-broker-config-005`）。
- 启动后先完成与 Claude Code 的握手，握手不依赖 Broker（`REQ-agent-messaging-038`）。
- 在后台按固定的顺序选定要连接的 Broker；没有可用的 Broker 时拉起本账户的用户级 Broker，并使它脱离会话的进程树（`REQ-broker-config-012`、`REQ-broker-config-026`）。
- 向选定的 Broker 登记，报出环境中的 inbox socket 路径与环境中的配置目录，并保持登记连接（[ADR-register-through-mcp-server-process](../../adr/adr-register-through-mcp-server-process.md)，`REQ-agent-messaging-001`、`REQ-agent-messaging-036`）。
- Broker 不可用、登记未完成或登记失败期间不退出，工具调用返回说明原因的错误，后台持续重试连接（`REQ-agent-messaging-038`）。
- 收到 SIGINT 或 SIGTERM 时主动关闭登记连接后退出，使停用MCP 服务的会话随即注销（`REQ-agent-messaging-034`）。
- 登记连接断开后立即连回原来那个 Broker 并重新登记，恢复断开前的 topic 成员关系（`REQ-broker-config-037`）。
- 确定默认 topic 并在登记后加入（[ADR-default-topic-from-repository](../../adr/adr-default-topic-from-repository.md)，`REQ-topics-015` 至 `REQ-topics-021`）。
- 把工具调用转给 Broker，把结果和错误转成模型可读的文字；Broker 拉起失败时把它给出的原因原样转给模型。

### 非职责

- 不与对端 Broker 通信，由 [link](design-link.md) 负责；不写 inbox socket，不读会话注册表，由 [inbox](design-inbox.md) 负责。Claude Code 的内部信息中，本进程只读取环境变量 `CLAUDE_CODE_MESSAGING_SOCKET` 与 `CLAUDE_CONFIG_DIR`。
- 不表明自己的账户。调用方的账户由 [broker](design-broker.md) 从本机 socket 另一端进程的凭据（进程凭据）得出；登记时报出的三项只用于查找会话记录。
- 不区分调用来自主会话还是子代理。子代理与主会话共用本进程和同一条连接，以主会话的 Agent 身份行事（`REQ-agent-messaging-035`）。
- 不判定权限。每个调用能否执行由 `broker` 按调用方账户判定，`mcpserver` 只转述结果。
- 不拉起系统级 Broker。系统级 Broker 只由管理员安装和启动。
- 不判定可见性，不保存 topic 成员关系的权威副本，由 [topic](design-topic.md) 负责；`mcpserver` 只记着本会话已加入的 topic 名及加入时的公开声明与简介，用于重新登记后恢复。
- 不读取 Broker 的配置文件，配置由 [config](design-config.md) 保存，一律经 Broker 的本机接口读写。

## 外部接口契约

MCP 服务的名字是 `peer`，模型看到的工具全名是 `mcp__peer__<工具名>`。下表是工具的完整集合，工具名与参数名是对模型的稳定承诺。只读一栏为“是”的工具没有副作用：不改变 Broker 状态，也不投递消息。权限一栏是调用方账户对所连 Broker 的配置文件必须具备的权限，由 `broker` 判定（[ADR-management-by-config-file-permission](../../adr/adr-management-by-config-file-permission.md)）；连接用户级 Broker 时不检查这些前提。

| 工具 | 参数 | 只读 | 权限 | 返回 | 需求 |
|---|---|---|---|---|---|
| `list_agents` | `topic?`、`broker?`、`name?` 作为过滤条件 | 是 | 无 | 调用方自己的地址；每个可见 Agent 的地址、共同 topic、状态，改名后 10 分钟内的另注明曾用名 | `REQ-agent-messaging-008` 至 `REQ-agent-messaging-011`、`REQ-agent-messaging-032` |
| `send_message` | `to`、`message` | 否 | 无 | 成功；以 `Not sent` 开头的错误；以 `Delivery unknown` 开头的结果未知 | `REQ-agent-messaging-013`、`REQ-agent-messaging-027` |
| `list_topics` | 无 | 是 | 无 | 已加入的 topic；已知的公开 topic 及简介 | `REQ-topics-013` |
| `join_topic` | `name`、`public?`、`description?` | 否 | 无 | 加入后的 topic 列表 | `REQ-topics-001`、`REQ-topics-010`、`REQ-topics-023` |
| `leave_topic` | `name` | 否 | 无 | 退出后的 topic 列表 | `REQ-topics-003` |
| `list_brokers` | 无 | 是 | 无 | 每个已知 Broker 的别名、指纹、对外地址列表、是否对端、状态；对端另标明本方是它的拨号方还是接受方 | `REQ-broker-discovery-012` 至 `REQ-broker-discovery-014` |
| `connect_broker` | `address`，对方的连接地址，写作 `地址:端口` 或 `主机名(地址:端口)`；`key`，配对密钥；`local_alias?` | 否 | 写 | 对方的别名与指纹，`address` 带主机名时一并给出，可能附带警告；或以 `Not connected` 开头的错误 | `REQ-broker-pairing-010`、`REQ-broker-config-021` |
| `disconnect_broker` | `broker` | 否 | 写 | 解除配对的结果 | `REQ-broker-pairing-021`、`REQ-broker-config-021` |
| `create_pairing_key` | 无 | 否 | 写 | 一次性配对密钥及其失效时间；另行展示本机 Broker 的地址，写作 `主机名(地址:端口)`，对外地址列表有多项时一并列出，为空时说明没有可展示的地址、需要用户自行告知对方连接地址；本 Broker 不监听时返回错误，提示由对方生成密钥、本方发起配对 | `REQ-broker-pairing-005`、`REQ-broker-pairing-025`、`REQ-broker-pairing-028`、`REQ-broker-pairing-036`、`REQ-broker-config-021` |
| `get_config` | `key?` | 是 | 读 | 配置项及其当前值；密钥类配置项只给出是否已设置 | `REQ-broker-config-001`、`REQ-broker-config-022`、`REQ-broker-pairing-030` |
| `set_config` | `key`、`value` | 否 | 写 | 修改前后的值；修改密钥类配置项一律失败 | `REQ-broker-config-002`、`REQ-broker-config-021`、`REQ-broker-config-029`、`REQ-broker-pairing-031` |

权限一栏为“无”的工具不以对配置文件的任何权限为前提（`REQ-broker-config-023`）。权限不足的调用得到 `permission_denied`，转给模型的文字说明所缺的是读权限还是写权限。这一判定与 Claude Code 对工具调用的权限确认各自独立，一次调用要两者都通过才执行。

MCP 服务的使用说明随工具一起进入会话上下文。下列要点是必须包含的内容，说明可以有其他文字：

- 带 `@` 的发送方名字是 Agent 地址。回复时用 `send_message`，把来信署名中的地址原样填入 `to`；Claude Code 自带的会话间发送工具到不了这类地址（`REQ-agent-messaging-025`）。
- 地址带 `@` 的 Agent 用本服务的工具联系，不论它在本机还是在别的主机上；没有登记到本系统的本机会话用 Claude Code 自带的工具联系。
- `send_message` 成功只表示消息已到达对方会话，不表示对方已读或同意；对方的会话可能把消息留待用户批准，对方也可能不回复。
- 结果为 `Delivery unknown` 时消息可能已经送达，重发可能使对方收到两份。
- 向多个 Agent 发送时逐个调用。不为致谢或确认收到而回复。
- 不要在短时间内向同一个 Agent 重复发送内容完全相同的消息：接收方会丢弃重复的那一条，而发送照样返回成功（[ADR-no-broker-rate-limiting](../../adr/adr-no-broker-rate-limiting.md)）。
- 本会话中遭到拒绝或会遭到拦截的操作，不得请其他 Agent 代做。

环境变量 `AGENT_PEER_SOCKET` 指定要连接的本机 socket，用于会话的工具配置明确选定某个 Broker 的情形。

项目配置文件是工作区内的 `.agent-peer.yaml`，从会话工作目录向上逐级查找，以先找到的为准：

```yaml
topics: [team-backend, github.com/org/repo]   # 声明后不再使用 origin
auto_join: true                               # 设为 false 时不自动加入任何 topic
```

## 依赖

`mcpserver` 只引用 `broker` 包中本机接口的客户端与消息定义，符合模块依赖图的方向。它用系统的 `git` 命令读取 `origin` 地址。两种形态下运行时目录的位置规则与 [config](design-config.md) 所列的一致，由 `broker` 包中的客户端一并提供。

## 核心算法与流程

### 选定 Broker

按下列顺序取第一个成立的（`REQ-broker-config-026`）：

| 顺序 | 条件 | 连接 |
|---|---|---|
| 一 | 环境变量 `AGENT_PEER_SOCKET` 非空 | 它指定的本机 socket；连接不上即失败，不再往下找 |
| 二 | 本账户的用户级 Broker 的本机 socket 上有进程应答 | 该 socket |
| 三 | 系统级 Broker 的本机 socket 存在，且本账户有权连接 | 该 socket |
| 四 | 以上都不成立 | 拉起本账户的用户级 Broker，然后连接它 |

管理员用系统级 Broker 本机 socket 的文件权限限定哪些账户可以使用它（`REQ-broker-config-027`），无权连接的账户落到拉起用户级 Broker 这一项。本账户已有用户级 Broker 在运行时，即使主机上另有系统级 Broker，也连接用户级的那个。

### 拉起用户级 Broker

启动 `agent-peer broker`，不带 `--system`，作为新的会话组的首进程，并使它的父进程不再是本进程；它的环境中去掉 `CLAUDE_CODE_MESSAGING_SOCKET`，使 Broker 的进程树检查不把它当作会话的后代（见 [inbox](design-inbox.md)）。然后在有限时间内反复重连。多个 MCP 服务进程同时这样做时，只有取得账户锁的那个 Broker 留下，其余的自行退出。

Broker 在启动阶段失败时，例如配置的监听地址已被同一主机上另一个账户的 Broker 占用，它在标准错误输出说明原因后退出。MCP 服务进程收集这段说明，此后的每次工具调用都返回它，直到一次重新选定成功；后台按退避重跑完整的“选定 Broker”，包括重新拉起用户级 Broker。说明中写明要由用户在配置或启动参数中改用其他监听地址，MCP 服务进程不替用户更换（[ADR-listen-by-default](../../adr/adr-listen-by-default.md)，`REQ-broker-config-024`）。

### 启动与登记

1. 启动后立即响应 Claude Code 的 MCP 握手，声明全部工具。握手不等待 Broker：Claude Code 在握手超过 30 秒未完成时把 MCP 服务标为失败，要用户手动重新连接（`REQ-agent-messaging-038`）。
2. 在后台按“选定 Broker”连接 Broker，必要时拉起用户级 Broker，调用 `Hello` 比较本机协议版本。不兼容时不再登记，之后的每次工具调用都返回提示重启 Broker 的 `version_mismatch` 错误（`REQ-broker-config-016`）。
3. 打开 `Register` 流，上行首帧带两项：环境变量 `CLAUDE_CODE_MESSAGING_SOCKET` 的值，可以为空；环境变量 `CLAUDE_CONFIG_DIR` 的值，可以为空。本进程不读会话注册表，记录的查找、等待与核对由 Broker 完成，见 [broker](design-broker.md)。
4. 收到下行流发出的登记完成后，计算默认 topic 并逐个调用加入。下行流发出 `not_registered` 时记下原因，流保持打开，等待 Broker 此后发出登记完成。

Claude Code 在会话启动时就启动本进程，本进程是会话进程的直接子进程。会话的 inbox 的绑定晚于本进程启动时，环境中没有 socket 路径，登记停在等待，Broker 在下行流上说明原因，直到本进程重新启动。无界面运行与在 IDE 等宿主中运行的会话，登记与交互式会话相同（`REQ-agent-messaging-036`）。

Broker 不可用、登记未完成或登记失败期间，本进程不退出，工具调用立即返回说明原因的错误，不排队也不等待；与 Broker 的连接在后台按退避持续重试。任何 Broker 一侧的故障都不使本进程退出，会话因此不需要用户重新连接 MCP 服务（`REQ-agent-messaging-038`）。

收到 SIGINT 或 SIGTERM 时，本进程主动关闭 `Register` 流后退出。用户在 `/mcp` 界面停用 MCP 服务或会话退出时，Claude Code 向本进程依次发送 SIGINT、SIGTERM 与 SIGKILL，登记连接因此随即断开（`REQ-agent-messaging-034`）。

### 重新登记

`Register` 流断开时，`mcpserver` 立即连回原来那个 Broker 并重新登记，不等下一次工具调用，也不改连其他 Broker（`REQ-broker-config-037`）。原来是用户级 Broker 的，连不上时按“拉起用户级 Broker”重新拉起它；原来是系统级 Broker 的，一直退避重连它的本机 socket，等待系统的服务管理机制把它重新启动，不拉起用户级 Broker，也不从“选定 Broker”重新开始。重新登记以同一个 socket 路径进行，得到的 Agent 标识不变；消歧标识按当时本 Broker 上的 Agent 集合重新计算，集合未变时其他 Agent 手里的地址继续有效。登记完成后按原来的公开声明与简介重新加入本进程记着的全部 topic；Broker 撤销登记后再次发出登记完成时同样如此。Broker 已改为不公开的，公开声明失败，该 topic 仍以不公开方式加入。本进程退出后，它记着的 topic 随之丢失：MCP 服务被停用后重新启用，新的进程只重新加入默认 topic。

### 默认 topic 的确定

按下列顺序判断，前面的条件成立即得出结果：

1. Broker 配置 `topics.auto_join` 为假：不加入。
2. 找到项目配置文件且 `auto_join` 为假：不加入。
3. 找到项目配置文件且 `topics` 非空：加入其中每一个。
4. 工作目录所在的 git 仓库有 `origin`，且其地址能规范化：加入以规范化结果命名的 topic。
5. 其余情况不加入。

自动加入一律不带公开声明（`REQ-topics-019`）。`topics.auto_join` 的值由 Broker 在登记的应答中给出，MCP 服务进程不需要读配置的权限。

### 仓库地址的规范化

规范化的结果是“小写的主机名、`/`、仓库路径”。地址按写法分别处理：

| 写法 | 判别 | 主机名与路径的取法 |
|---|---|---|
| 带协议的 URL，协议为 `https`、`http`、`ssh` 或 `git` | 含 `://` | 按 URL 解析：主机名取主机部分，丢弃用户信息与端口；路径取 URL 的路径 |
| 类 scp 写法，如 `git@github.com:org/repo.git` | 不含 `://`，第一个 `/` 之前有 `:` | 第一个 `:` 之前是主机部分，其中最后一个 `@` 之前的内容丢弃；`:` 之后全部是路径 |
| 本地路径或 `file` 协议 | 以上都不符 | 不规范化，不产生默认 topic |

得到主机名与路径后，去掉路径首尾的 `/` 和结尾的 `.git`，主机名转为小写，路径保持原样。类 scp 写法中 `:` 之后的内容一律当作路径，即使它以数字开头。用户信息只在主机部分内识别并丢弃，密码中的字符不会进入结果。`git@github.com:org/repo.git`、`ssh://git@github.com/org/repo.git` 与 `https://github.com/org/repo` 的结果都是 `github.com/org/repo`（`REQ-topics-017`）。

## 错误与失败语义

Broker 返回的错误码转成一句以结果开头的文字，随后附上错误码和可采取的做法，例如名字有歧义时列出候选地址，附上消歧标识，目标是曾用名时给出现在的地址，权限不足时指出所缺的权限。Broker 不可达或登记未完成时，工具调用立即返回说明原因的错误，不在 MCP 服务进程内排队。

| 情形 | 工具调用得到 |
|---|---|
| `AGENT_PEER_SOCKET` 指定的 socket 连接不上 | 错误，说明该 socket 不可用；不改连其他 Broker |
| 拉起的用户级 Broker 在启动阶段退出 | Broker 给出的原因原样返回，例如监听地址不可用、已有实例在运行 |
| 本机协议版本不兼容 | `version_mismatch`，提示重启 Broker |
| Broker 连接不上，或正在重新连接 | 错误，说明 Broker 不可用、正在重试 |
| 登记未完成或失败：找不到会话记录，会话名不可用，或 socket 不属于本账户 | 需要 Agent 身份的工具返回 `not_registered` 并说明原因；`list_brokers`、`get_config`、`set_config`、`connect_broker`、`disconnect_broker`、`create_pairing_key` 不需要 Agent 身份，仍按权限执行 |
| 调用方账户缺少所需的权限 | `permission_denied` |
| Broker 以启动参数给定监听地址运行时，`set_config` 修改 `broker.listen` | `invalid_argument`，说明本次运行的监听地址由启动参数给定，配置文件未改动 |
| `set_config` 修改 `pairing.static_key` | `invalid_argument`，说明该项只能在配置文件中设置 |
| `set_config` 使监听地址与对外地址配置的组合非法 | `invalid_argument`，说明与哪一项冲突（`REQ-broker-config-032`） |

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 工具声明与使用说明 | 未实现 | — |
| Broker 的选定 | 未实现 | — |
| 拉起用户级 Broker 与失败原因的传递 | 未实现 | — |
| 握手、版本握手与会话登记 | 未实现 | — |
| 信号处理 | 未实现 | — |
| 重新登记与成员关系恢复 | 未实现 | — |
| 默认 topic 的确定 | 未实现 | — |
| 仓库地址的规范化 | 未实现 | — |
| 工具调用转发与错误转换 | 未实现 | — |
