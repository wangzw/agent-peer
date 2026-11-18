# config

`config` 保存 [Broker](../../glossary.md) 的配置和审计日志，并给出两种部署形态下各类文件的位置。配置是一组有名字、有类型、有默认值的键，可以由用户编辑文件设定，也可以经本机接口读取和修改；审计日志是只追加的事件记录。其他包从这里读取自己需要的值和路径，并把需要留痕的事件交给它写入。它不引用本项目的任何其他包。

## 职责

- 按账户与部署形态给出运行时目录、配置文件、数据目录、审计日志和账户锁的路径（[ADR-broker-deployment-modes](../../adr/adr-broker-deployment-modes.md)）。
- 启动时加载配置文件，对缺失的键取默认值，键的类型或取值不合法时拒绝启动并指出该键；监听地址与对外地址配置的组合非法时拒绝启动并同时指出这两项，对外地址列表超过 8 项、或对外地址的配置写出本版本不认识的地址种类时拒绝启动并指出该项，运行期间的这类修改不提交（`REQ-broker-config-032`、`REQ-broker-config-036`）。
- 把启动参数给出的监听地址作为本次运行的取值，不把它写入配置文件，配置文件中的 `broker.listen` 在本次运行中不起作用；其间经本机接口修改 `broker.listen` 的调用失败（`REQ-broker-config-025`、`REQ-broker-config-029`）。
- 在没有配置别名时按监听地址得出默认别名（`REQ-broker-pairing-002`）。
- 由监听地址、`broker.advertise` 与 `broker.advertise_extra` 得出本次运行的对外地址列表，监听全部地址时取默认路由所在接口的地址，供 [link](design-link.md)、[pairing](design-pairing.md) 与 [directory](design-directory.md) 使用（[ADR-advertised-address](../../adr/adr-advertised-address.md)，`REQ-broker-config-030`、`REQ-broker-config-031`、`REQ-broker-config-033`、`REQ-broker-config-035`）。
- 每隔 `broker.address_check_interval` 重新枚举本机地址，对外地址列表变化时通知订阅者（`REQ-broker-discovery-016`）。
- 给出系统主机名，供 `pairing` 在生成配对密钥的结果中展示本机地址（`REQ-broker-pairing-028`）。
- 保存密钥类配置项，即只经配置文件设置、取值不经本机接口返回的配置项，读取时不返回取值，审计与日志不记录取值，并校验预置静态密钥的长度（[ADR-static-pairing-key](../../adr/adr-static-pairing-key.md)，`REQ-broker-pairing-029` 至 `REQ-broker-pairing-031`）。
- 提供按键读取，以及经校验的修改：校验通过后持久化、更新内存、写一条审计记录、通知订阅该键的调用方（`REQ-broker-config-001`、`REQ-broker-config-002`、`REQ-broker-config-006`）。
- 监视配置文件，文件由其他程序改动后逐项采用合法的新值，不合法的项保留原值，并为每个采用了新值的键写一条审计记录（`REQ-broker-config-017`）。
- 追加审计记录，合并同一来源的重复拒绝事件，并提供按时间顺序读取（`REQ-broker-config-007` 至 `REQ-broker-config-009`）。

### 非职责

- 不决定工具的读写划分与只读标记，由 [mcpserver](design-mcpserver.md) 负责。
- 不要求用户确认。修改是否需要确认由 Claude Code 的权限设置决定（[ADR-config-through-tools](../../adr/adr-config-through-tools.md)）。
- 不判定调用方有没有权限读写配置。配置文件的文件权限就是管理权限的依据（[ADR-management-by-config-file-permission](../../adr/adr-management-by-config-file-permission.md)），判定由 [broker](design-broker.md) 在调用本包之前完成；本包只给出配置文件的路径。
- 不绑定监听地址，由 [link](design-link.md) 负责；绑定失败时的处理由 `broker` 负责。
- 不保存对端表与身份密钥，由 [pairing](design-pairing.md) 负责；不保存名录，由 [directory](design-directory.md) 负责。
- 不读取项目配置文件 `.agent-peer.yaml`，由 `mcpserver` 负责。
- 不了解某个键的取值在别的包看来是否可行。这类检查由 [broker](design-broker.md) 以校验函数的形式注册进来。
- 不输出运行日志。运行日志由各包经标准日志接口写出，本包只提供其级别设置。

## 外部接口契约

供本项目其他各包调用，签名稳定。配置键的名字与含义是对用户的稳定承诺，新增键不改变既有键的含义。

```go
// Load 按形态确定各路径并加载配置；listenOverride 非空时取代 broker.listen，只对本次运行有效，
// 其间 Set 拒绝修改 broker.listen，配置文件中这一项的变化不采用。
func Load(mode Mode, listenOverride string) error
func Paths() PathSet                     // 运行时目录、配置文件、数据目录、审计日志
func AccountLock() string                // 账户锁的路径，不依赖 Load

func Get(key string) (Value, error)
func All() []Entry
func Set(actor Actor, key string, v Value) (old Value, err error)

// Secret 返回密钥类配置项的取值，未设置时为空；只供 pairing 在握手查找密钥时调用。
func Secret(key string) string

// AdvertisedAddr 是对外地址列表中的一项。Kind 是地址种类，决定其他 Broker 怎样经这一项连到本 Broker；
// Addr 是该种类下的地址写法。本版本产生的 Kind 只有 "direct"，此时 Addr 为 "主机:端口"，IPv6 主机带方括号。
type AdvertisedAddr struct {
    Kind string
    Addr string
}

// Advertised 返回本次运行的对外地址列表，至多 8 项；不监听时为空。列表中的每一项都是直连的地址项。
func Advertised() []AdvertisedAddr

// Hostname 返回系统主机名，其中不属于别名字符集的字符替换为 "-"。
func Hostname() string

// SubscribeAdvertised 注册对外地址列表的变更通知；各次通知按发生的顺序逐个送达。
func SubscribeAdvertised(onChange func(old, new []AdvertisedAddr))

// Actor 是一次修改或一个事件的发起者：系统账户，以及经 Agent 发起时的 Agent 地址。
type Actor struct {
    Account string
    Agent   string
}

// Validate 注册某个键的附加校验；Set 与文件重载在提交之前调用它，返回错误则不提交。
func Validate(key string, check func(old, new Value) error)

// Subscribe 注册某个键的变更通知；各次通知按提交的顺序逐个送达。
func Subscribe(key string, onChange func(old, new Value))

func Audit(e Event)
func ReadAudit(since time.Time) ([]Event, error)
```

下表是配置键的完整集合：

| 键 | 类型 | 默认值 | 含义 |
|---|---|---|---|
| `broker.alias` | 字符串 | 见“默认别名” | 本机自报的别名，1 至 24 个字符，取自字母、数字、`.`、`-`、`_`，超过 24 个字符或含其他字符的配置非法（`REQ-broker-pairing-002`、`REQ-broker-pairing-003`、`REQ-broker-pairing-037`） |
| `broker.public` | 布尔 | `false` | 是否公开（`REQ-broker-discovery-001`） |
| `broker.listen` | 字符串 | `:17443` | 实际绑定的地址与端口；主机部分为空表示本机的全部地址，明确配置为空表示不监听（[ADR-listen-by-default](../../adr/adr-listen-by-default.md)，`REQ-broker-config-028`） |
| `broker.advertise` | 字符串 | 空 | 对外地址的配置，合法写法只有空、`主机`、`主机:端口`、`:端口`，见“对外地址”（[ADR-advertised-address](../../adr/adr-advertised-address.md)，`REQ-broker-config-030` 至 `REQ-broker-config-032`） |
| `broker.advertise_extra` | 字符串列表 | 空 | 追加的对外地址，每项是 `主机` 或 `主机:端口`（`REQ-broker-config-033`） |
| `broker.address_check_interval` | 时长 | `1m` | 重新枚举本机地址的间隔（`REQ-broker-discovery-016`） |
| `broker.local_socket_group` | 字符串 | 空 | 系统级 Broker 本机 socket 的属组；空表示沿用运行时目录的属组，用户级忽略（`REQ-broker-config-027`） |
| `broker.local_socket_mode` | 字符串 | `0666` | 系统级 Broker 本机 socket 的文件权限；用户级的固定为 `0600`（`REQ-broker-config-027`） |
| `pairing.static_key` | 字符串，密钥类 | 空 | 预置静态密钥，长度 16 至 128 个字符，有值即生效（[ADR-static-pairing-key](../../adr/adr-static-pairing-key.md)，`REQ-broker-pairing-008`、`REQ-broker-pairing-009`、`REQ-broker-pairing-029`） |
| `pairing.max_failures` | 整数 | `5` | 一次性配对密钥作废前允许的验证失败次数 |
| `pairing.max_attempts_per_minute` | 整数 | `10` | 每分钟允许的、没有匹配上任何配对密钥的配对请求数；匹配上预置静态密钥的成功请求不计入 |
| `topics.auto_join` | 布尔 | `true` | 是否自动加入默认 topic（`REQ-topics-020`） |
| `directory.reconcile_interval` | 时长 | `10m` | 定期对账的间隔 |
| `directory.max_records` | 整数 | `1000` | 名录记录总数的上限（`REQ-broker-discovery-011`） |
| `directory.max_records_per_link` | 整数 | `250` | 经同一条链路接受的记录数上限（`REQ-broker-discovery-015`） |
| `directory.max_topics_per_publisher` | 整数 | `16` | 同一发布者的公开 topic 声明数上限 |
| `send.link_retries` | 整数 | `5` | 链路未建立时的重试次数（`REQ-agent-messaging-017`） |
| `send.link_backoff` | 时长 | `500ms` | 首次重试前的等待，之后每次翻倍，单次不超过 8 秒 |
| `send.request_timeout` | 时长 | `20s` | 一次发送的预算，即发送调用的总时限（`REQ-agent-messaging-017`、`REQ-agent-messaging-018`） |
| `send.inbox_retries` | 整数 | `2` | inbox 确定未写入时的重试次数（`REQ-agent-messaging-018`） |
| `send.inbox_backoff` | 时长 | `200ms` | inbox 写入重试之间的等待 |
| `send.inbox_timeout` | 时长 | `5s` | 单次 inbox 写入的期限 |
| `send.dedupe_window` | 时长 | `5m` | 去重表条目的保留时间 |
| `link.dial_timeout` | 时长 | `5s` | 拨号时对每个地址的连接期限 |
| `link.reconnect_min` | 时长 | `1s` | 重建链路的初始退避间隔 |
| `link.reconnect_max` | 时长 | `60s` | 重建链路的最大退避间隔 |
| `link.keepalive_interval` | 时长 | `15s` | 保活探测的间隔 |
| `link.keepalive_timeout` | 时长 | `10s` | 保活探测的应答期限 |
| `log.level` | 字符串 | `info` | 运行日志级别 |

`broker.listen` 的默认值只是默认配置中的一个取值，代码不对这个端口做任何特殊处理。一次性配对密钥的有效期、名录记录的有效期和续签间隔由需求固定（`REQ-broker-pairing-006`、`REQ-broker-discovery-007`、`REQ-broker-discovery-008`），不是配置键；消息编码成 inbox 帧后的字节数上限 1 000 000 同样固定（`REQ-agent-messaging-026`），Agent 曾用名的保留时间 10 分钟是常量，两者见 [broker](design-broker.md)。表中其余默认值是实现选定的初值。

### SetConfig 的取值

`SetConfig` 请求中的 `value` 是一个字符串，原样携带模型给出的文字，不按键的类型预先编码。Broker 按该键在配置键表中的类型解析它：字符串、布尔、整数与时长键的取值是一个 YAML 标量，例如 `10m`、`true`；字符串列表键的取值是一个 YAML 流式序列（flow sequence），例如 `[a, b]`。解析结果转成 `Value` 后交给 `Set`；字符串不能按该键的类型解析时，调用以 `invalid_argument` 失败，错误信息指出该键与原因，不改动任何状态。

`GetConfig` 的应答、`SetConfig` 的应答与审计记录中的取值仍用 `ConfigValue` 表示。`Set` 接收解析后的 `Value`，不接收字符串。

## 依赖

`config` 不引用本项目的其他包，符合模块依赖图的方向。

## 核心数据结构

各类文件的位置由部署形态决定：

| 内容 | 用户级 | 系统级 |
|---|---|---|
| 配置文件 | `~/.agent-peer/config.yaml` | `/etc/agent-peer/config.yaml` |
| 数据目录：身份、对端表、名录 | `~/.agent-peer/` | `/var/lib/agent-peer/` |
| 审计日志 | `~/.agent-peer/audit.log` | `/var/log/agent-peer/audit.log` |

运行时目录内有本机 socket `broker.sock` 和账户锁 `broker.lock`。运行时目录只取决于进程的账户，与形态、环境变量、配置内容和监听地址无关：root 的是 Linux 上的 `/run/agent-peer/`、macOS 上的 `/var/run/agent-peer/`，系统级 Broker 与 root 的用户级 Broker 因此争用同一把锁；其他账户的是 `/tmp/agent-peer-<uid>/`，`<uid>` 是进程的账户。同一账户的两个进程因此总是争用同一把锁，也总在同一个位置找本机 socket（`REQ-broker-config-013`）。root 的运行时目录权限 `0755`；系统级 Broker 的本机 socket 在绑定后按 `broker.local_socket_group` 与 `broker.local_socket_mode` 设置属组与权限，哪些账户能连接由它决定；用户级的 socket 固定为 `0600`；其他账户的运行时目录权限 `0700`。运行时目录的属主必须是本账户，不符时拒绝启动。

用户级的数据目录权限 `0700`，其中的文件权限 `0600`。系统级的配置文件默认只有 root 可写；管理员把它的读或写权限授予某个组，即把读取配置或管理操作委托给该组的账户。

配置文件的结构与键名一一对应，例如 `broker.alias` 对应顶层 `broker` 下的 `alias`。文件中没有出现的键取默认值，文件不存在等同于空文件。`Set` 只改动文件中它所修改的那个键，文件里的其他内容原样保留；文件不存在时，以运行 Broker 的账户创建它，属主与权限按形态设置。

默认别名：没有配置 `broker.alias` 时，本次运行有监听地址的，取“系统主机名、`-`、监听端口”，例如 `buildbox-17443`；没有监听地址的，取系统主机名。规则对所有端口相同，默认配置下的别名因此带有端口；别名取监听端口，与对外地址中的端口无关。系统主机名中不属于别名字符集的字符替换为 `-`。结果超过 24 个字符时从末尾截短主机名部分，保留 `-` 与端口，使别名恰为 24 个字符；不监听时取主机名的前 24 个字符（`REQ-broker-pairing-037`）。监听端口变化使默认别名随之变化，按 `broker.alias` 的变更通知订阅者。

监听地址：`broker.listen` 的主机部分为空时，在本机的全部地址上监听，启用了 IPv6 时包括 IPv6 地址；写出了主机部分的，按字面绑定该地址，其中 `0.0.0.0` 与 `::` 在计算对外地址时按全部地址对待。端口不得为 0。

对外地址：本次运行的对外地址列表由生效的监听地址与 `broker.advertise` 按下表得出，`broker.advertise_extra` 的各项追加在其后（[ADR-advertised-address](../../adr/adr-advertised-address.md)，`REQ-broker-config-030`、`REQ-broker-config-031`、`REQ-broker-config-033`）。

| 监听地址 | `broker.advertise` | 列表中由此得出的部分 |
|---|---|---|
| 不为空 | `主机:端口` | 仅此一项 |
| 不为空 | `主机` | 该主机配上监听端口 |
| 特定地址 | 空 | 监听地址配上监听端口（`REQ-broker-config-035`） |
| 全部地址 | 空 | 默认路由所在接口的地址，每个地址都配上监听端口 |
| 全部地址 | `:端口` | 默认路由所在接口的地址，每个地址都配上该端口 |
| 空，不监听 | 空 | 空 |

表外的组合非法，例如监听特定地址而 `broker.advertise` 只有端口，或不监听而 `broker.advertise` 非空；不监听时 `broker.advertise_extra` 也必须为空（`REQ-broker-config-032`）。组合按本次运行生效的监听地址判定，监听地址来自启动参数时以它为准。`broker.advertise_extra` 中不带端口的项取对外地址所用的端口：`broker.advertise` 写出了端口时取它，否则取监听端口。主机是 IP 地址或域名，IPv6 地址写成带方括号的形式。列表中重复的项只保留首次出现的。

对外地址列表中的每一项有一个种类，种类决定其他 Broker 怎样经这一项连到本 Broker。Go 接口以 `AdvertisedAddr` 表示一项，`Kind` 是种类，`Addr` 是该种类下的地址写法。本版本产生的种类只有直连，取值为 `direct`，其地址写作 `主机:端口`，表示可以直接向该地址建立连接，IPv6 主机带方括号；本包得出的列表只含直连的项。`broker.advertise` 与 `broker.advertise_extra` 中写出本版本不认识的种类的项属于非法配置，与组合非法同样处理。线路上种类的具体表示法属于实现。其他 Broker 告知的列表与名片所载的列表中出现不认识的种类时怎样处理，见 [link](design-link.md)、[pairing](design-pairing.md) 与 [directory](design-directory.md)。

默认路由所在接口的地址：度量值最小的默认 IPv4 路由所在接口上的 IPv4 地址，不含 `169.254.0.0/16` 中的链路本地地址；存在默认 IPv6 路由时，另加度量值最小的那条所在接口上的全局单播 IPv6 地址，不含链路本地地址与临时地址。没有默认路由时这部分为空。主机接入的其他网络上的地址不列入。

列表至多 8 项。`broker.advertise` 含主机时的一项加上 `broker.advertise_extra` 的项数超过 8 的配置非法（`REQ-broker-config-036`）；默认路由所在接口的地址使列表超过 8 项时，舍去这部分中排在后面的地址，`broker.advertise_extra` 的各项全部保留。

本包每隔 `broker.address_check_interval` 重新枚举本机地址，并在 `broker.listen`、`broker.advertise`、`broker.advertise_extra` 变化后重新计算列表；列表与此前不同时通知 `SubscribeAdvertised` 的订阅者（`REQ-broker-discovery-016`）。对端在连接上看到的本方地址不参与计算。

名片中的列表与生成配对密钥时的展示分别见 [directory](design-directory.md) 与 [pairing](design-pairing.md)。

密钥类配置项：`pairing.static_key` 属于这一类，这一类配置项适用下列规则（[ADR-static-pairing-key](../../adr/adr-static-pairing-key.md)）：

- 只能通过编辑配置文件设置。`Set` 对它的修改一律以 `invalid_argument` 拒绝，说明该项只能在配置文件中设置（`REQ-broker-pairing-031`）。
- `Get` 与 `All` 只给出它是否已设置，不返回取值；取值只经 `Secret` 交给 `pairing`（`REQ-broker-pairing-030`）。
- 配置文件被修改后的重载对它照常生效，设置、更换、删除都不需要重启（`REQ-broker-config-017`、`REQ-broker-pairing-032`、`REQ-broker-pairing-033`）。它的 `config_file_changed` 审计记录只记这一项发生了变化，不带 `old` 与 `new`；运行日志、审计日志和错误信息都不出现它的取值（`REQ-broker-config-008`、`REQ-broker-pairing-030`）。
- 预置静态密钥的长度必须在 16 至 128 个字符之间：启动时不合法即拒绝启动并指出该键，重载时不合法即这一项保留原值（`REQ-broker-pairing-029`）。取值原样保存，不做大小写或格式的规范化。
- 取值必须写成 YAML 字符串，写成数字等其他类型的视为不合法；含 ` #` 等会被 YAML 另作解释的字符时要加引号。

审计日志每行一个 JSON 对象：

| 字段 | 含义 |
|---|---|
| `time` | 事件时间，RFC 3339 格式 |
| `event` | 事件类型，取值见下表 |
| `actor` | 发起的 Agent 地址；不是经 Agent 发起，或由对端、文件改动、系统触发时为空 |
| `account` | 发起的系统账户；由对端、文件改动或系统触发时为空（`REQ-broker-config-007`） |
| `peer` | 相关 Broker 的指纹与别名；与 Broker 无关时为空 |
| `key`、`old`、`new` | 配置变更的键与前后的值；密钥类配置项只有 `key` |
| `count` | 合并的事件次数；没有合并时不出现 |
| `detail` | 失败原因等补充信息 |

事件类型是封闭集合：

| 事件 | 触发 |
|---|---|
| `config_set` | 经本机接口完成的一次配置修改 |
| `config_file_changed` | 配置文件由其他程序改动后，某个键采用了新值 |
| `pairing_key_created` | 生成一次性配对密钥 |
| `pairing_succeeded` | 配对成功，发起方与接受方各记一条 |
| `pairing_failed` | 配对失败，记录原因 |
| `link_up` | 链路建立 |
| `link_down` | 链路断开 |
| `unpaired` | 解除配对 |
| `connection_rejected` | 拒绝了一个未知指纹的连接 |
| `rejected_by_peer` | 本方的连接遭对方以“不是对端”拒绝 |
| `local_alias_assigned` | 系统为某个对端生成了本地别名 |

审计日志不记录配对密钥的值、消息正文和消息的收发（`REQ-broker-config-008`）。密钥类配置项的变化只记键名，见“密钥类配置项”。

## 核心算法与流程

### 修改一个键

`Set` 依次做下面的事，前一件失败则不做后面的：

- 检查键存在且不是密钥类配置项，值的类型与取值范围合法，与其他键的组合合法，再调用为该键注册的校验函数。修改 `broker.listen`、`broker.advertise` 或 `broker.advertise_extra` 时，以新值与另外两项的当前值核对“对外地址”中的组合规则、项数上限与地址种类。
- 读取配置文件的当前内容，只替换这个键，在原文件上就地改写：截断后写入完整内容并同步到磁盘。文件不存在时以本账户的身份创建它；文件存在时就地改写，原文件的属主、组、权限位与访问控制列表因此保持不变。
- 更新内存中的值。
- 追加 `config_set` 审计记录。
- 把这次变更放入通知队列。

通知队列由一个协程按提交的顺序逐个调用订阅者，对同一个键的两次修改因此按修改的先后生效。校验函数用于需要别的包判断的情形，由 `broker` 注册：`broker.listen` 的新值先尝试绑定，绑定失败则修改不提交，原有的监听不受影响（`REQ-broker-config-034`）；`broker.alias` 的新值与某个对端的生效别名相同时修改不提交。本次运行的监听地址来自启动参数时，`Set` 对 `broker.listen` 的修改在校验之前即以 `invalid_argument` 拒绝，错误说明本次运行的监听地址由启动参数给定，配置文件与内存中的值都不变（`REQ-broker-config-029`）。

订阅者使修改立即生效，订阅在 `broker` 装配完成后交给各包：`broker.alias` 的变更使各链路收到 `PeerInfo` 帧，并使 `directory` 重新签发名片；`broker.listen` 的变更使 `link` 改在新地址上监听，没有配置别名时默认别名随端口变化，按别名变更处理；对外地址列表的变化，不论来自 `broker.listen`、`broker.advertise`、`broker.advertise_extra` 的修改还是定期的重新枚举，都经 `SubscribeAdvertised` 送达，使本方是[接受方](../../glossary.md)的各条链路收到 `PeerInfo` 帧，并使 `directory` 重新签发名片；`broker.public` 的变更使 `directory` 发布或撤回本机的记录；改为公开时，`broker` 另行按各 Agent 的公开 topic 集合重新声明。`pairing.static_key` 没有订阅者，`pairing` 在下一次握手查找密钥时读取新值；`Secret` 返回取值之前核对配置文件的修改时间与大小，与上次读取不同即重新读取，文件变化通知丢失时删除或更换仍能生效。各项间隔、次数与上限在下一次使用时取新值。

### 配置文件的监视

本包监视配置文件。文件内容变化时重新解析，与内存中的值逐键比较：

- 解析失败：不采用任何改动，内存中的值不变，运行日志以错误级别记录原因。
- 解析成功：逐键判定。取值合法并通过校验函数的键采用新值；不合法或未通过校验函数的键保留原值，运行日志以错误级别记录。`broker.listen`、`broker.advertise` 与 `broker.advertise_extra` 作为一组判定：按改动后的整体检查组合规则、项数上限与地址种类，合法则一起采用，否则三项都保留原值。
- 对每个采用了新值的键更新内存、追加一条 `config_file_changed` 审计记录、放入通知队列。

本包自己写入文件期间不处理文件变化；写入完成后文件内容与内存中的值相同，不产生记录。

本次运行的监听地址来自启动参数时，`broker.listen` 不参与比较与校验：文件中这一项的值变化时不采用，不写审计记录，运行日志记录一次；其他键照常重载（`REQ-broker-config-017`）。

### 审计记录的合并

`connection_rejected` 与由尝试过于频繁引起的 `pairing_failed`，在 10 分钟内来自同一指纹或同一来源地址的，只保留一条记录并累加其 `count`，使持续重连的一方不能无限追加审计日志。其余事件逐条记录。

## 错误与失败语义

| 情形 | 行为 |
|---|---|
| 启动时配置文件无法解析或含不合法的值 | 拒绝启动；无法解析时错误信息只给出行号与列号，不引用文件内容；含不合法的值时指出键与原因，不含密钥类配置项的取值 |
| 启动时监听地址与对外地址配置的组合非法，对外地址的配置超过 8 项，或写出本版本不认识的地址种类 | 拒绝启动；组合非法时错误信息同时指出 `broker.listen` 与 `broker.advertise` 或 `broker.advertise_extra`，超过 8 项时指出 `broker.advertise_extra`，种类不认识时指出写出该项的键 |
| 运行时目录或账户锁所在目录的属主、权限不符 | 拒绝启动，错误信息指出目录 |
| `SetConfig` 请求中的 `value` 无法按该键的类型解析 | 返回 `invalid_argument`，指出该键与原因，不改动任何状态 |
| `Set` 的键不存在，值不合法，或校验函数拒绝 | 返回 `invalid_argument`，不改动任何状态 |
| `Set` 修改密钥类配置项 | 返回 `invalid_argument`，说明该项只能在配置文件中设置，不改动任何状态 |
| `Set` 使监听地址与对外地址配置的组合非法，使对外地址的配置超过 8 项，或写出本版本不认识的地址种类 | 返回 `invalid_argument`，说明与哪一项冲突或哪一项的种类不认识，不改动任何状态 |
| 本次运行的监听地址来自启动参数时 `Set` 修改 `broker.listen` | 返回 `invalid_argument`，说明本次运行的监听地址由启动参数给定，不改动任何状态 |
| 配置文件写入失败 | 返回 `internal`，内存中的值不变，不写审计记录；就地改写中途失败或进程崩溃时文件可能不完整，下次加载时按无法解析处理 |
| 运行期间配置文件改成了不合法的内容，包括组合非法 | 不合法的项保留原值，其余合法的项照常采用，运行日志记录 |
| 本次运行的监听地址来自启动参数时，配置文件中的 `broker.listen` 改变 | 不采用，其他键照常重载，运行日志记录 |
| 审计日志写入失败 | 触发它的操作照常完成，运行日志以错误级别记录 |

能够写配置文件的账户不经本机接口也能直接改写它，这正是管理权限以该文件的写权限为准的含义；用户级形态下审计日志与配置文件属于同一账户，该账户下的其他进程能够改写它们，本包不防范这一途径。系统级形态下审计日志只有 root 可写。

## 并发与事务边界

`Set` 与文件重载的各项动作由一把互斥锁串行化，订阅者在锁外由通知协程调用。`Get` 读取内存中的值，不访问磁盘。审计日志的追加由独立的锁保护，每条记录以一次写调用写入完整的一行。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 两种形态的路径与账户锁路径 | 未实现 | — |
| 配置的加载、默认值与校验 | 未实现 | — |
| 启动参数对监听地址的取代与默认别名 | 未实现 | — |
| 对外地址列表的计算与组合校验 | 未实现 | — |
| 密钥类配置项的读取、修改限制与长度校验 | 未实现 | — |
| 本机地址的定期重新枚举与变更通知 | 未实现 | — |
| 配置的读取与修改 | 未实现 | — |
| 附加校验与变更通知 | 未实现 | — |
| 配置文件的监视 | 未实现 | — |
| 审计日志的追加、合并与读取 | 未实现 | — |
