# 术语表

| 术语 | 英文 | 定义 |
|---|---|---|
| Broker | broker | 运行在主机上的守护进程，登记它所服务的本机 Agent，维护与对端的链路，并把收到的消息写入本机会话的 inbox socket。每个实例有自己的指纹、对端和配置；一台主机上可以有多个实例，按部署形态分为系统级 Broker 和用户级 Broker。 |
| 系统级 Broker | system broker | 以特权身份作为系统服务运行的 Broker，一台主机至多一个，服务本机上获准使用它的账户下的 Agent。 |
| 用户级 Broker | user broker | 以普通账户运行的 Broker，只服务同一账户下的 Agent。一个账户至多运行一个实例。 |
| Agent | agent | 启用了本系统工具并已向本机的一个 Broker 登记的 Claude Code 会话，以该会话的 inbox socket 区分。会话名规范化后非空且不含 `@` 与 `*`，是登记的前提。未启用工具的会话不是 Agent。 |
| 远端 Agent | remote agent | 登记在另一个 Broker 上的 Agent。 |
| Agent 地址 | agent address | 定位一个 Agent 的字符串，由 Agent 的名字、需要时的消歧标识、`@` 和别名依次组成，名字放不下时截短。别名是看到该地址的一方的 Broker 对该 Agent 所在 Broker 使用的别名，同一个 Agent 在不同 Broker 上的地址可以不同。 |
| 消歧标识 | disambiguator | 区分同一个 Broker 上各个 Agent 的短标识，由会话的 inbox socket 路径算出，算法与 Claude Code 在会话列表中显示该标识所用的算法相同。它在一个 Broker 内唯一，会话改名、清空对话之后不变。 |
| MCP 服务进程 | MCP server process | Claude Code 为启用了本系统工具的会话启动的子进程，随会话启动，向会话中的模型提供工具，并把所在会话登记到本机 Broker。 |
| 会话注册表 | session registry | Claude Code 在本机为每个会话进程保存的记录，写有进程号、会话名、状态和 inbox socket 的位置，会话进程退出时删除。 |
| inbox socket | inbox socket | Claude Code 为每个会话绑定的本机 socket，其他进程经它向该会话投递消息。 |
| 入站控制 | inbound controls | Claude Code 对进入会话的会话间消息施加的机制，决定一条消息是交给模型、留待用户批准还是丢弃。它的设置项称为入站策略。 |
| topic | topic | 以名字标识的可见性与通信许可范围。两个 Agent 至少同在一个 topic 时才能互相看见、互发消息。 |
| 默认 topic | default topic | Agent 登记时自动加入的 topic，取自项目配置文件的声明，没有声明时取工作区 git 仓库 `origin` 地址的规范化结果；两者都没有时不自动加入任何 topic。 |
| 公开 topic | public topic | 名字和简介向连通网络内所有 Broker 传播的 topic。未声明公开的 topic，只有双方都有成员的两个 Broker 才能确认它存在。 |
| 项目配置文件 | project configuration file | 工作区内随仓库提交的配置文件，声明该项目的 topic 以及是否自动加入。 |
| 成员名单 | roster | 一个 Broker 上属于某个 topic 的全部 Agent 的列表。 |
| 配对 | pairing | 两个 Broker 凭配对密钥相互认证并保存对方身份的过程。配对完成后双方互为对端。 |
| 对端 | peer | 与本 Broker 完成过配对且未解除配对的另一个 Broker。 |
| 配对密钥 | pairing key | 配对时用来证明身份、由配对双方共有的秘密值，分为一次性配对密钥和预置静态密钥。 |
| 一次性配对密钥 | one-time pairing key | 由 Broker 生成、限时有效、使用一次即失效的配对密钥。 |
| 预置静态密钥 | pre-provisioned static key | 可以在系统部署之前确定、由用户或管理员写在 Broker 配置文件里的配对密钥，可重复使用且不过期；系统不生成它。 |
| 指纹 | fingerprint | Broker 公钥的摘要，是 Broker 的身份标识，生成后不变。 |
| 别名 | alias | Broker 的显示名，出现在 Agent 地址的 `@` 之后。Broker 自报的别名未经配置时，系统级取系统主机名的第一段（第一个 `.` 之前的部分），用户级由主机名的第一段、`.` 和所属账户名依次组成；别名与监听地址、监听端口和对外地址无关，超过 24 个字符时先截短主机名部分，仍超长再截短账户名。 |
| 本地别名 | local alias | 本 Broker 为某个对端另起的别名，只在本 Broker 上有效。设有本地别名的对端，本 Broker 以本地别名取代它自报的别名。 |
| 监听地址 | listen address | Broker 实际绑定并接受其他 Broker 连接的地址，由配置或启动参数给出。主机部分为空时指本机的全部地址。监听地址为空的 Broker 不监听，只能主动连接其他 Broker。 |
| 降级运行 | degraded run | Broker 在启动的某一步失败后不退出、保留本机 socket 并报告故障的运行状态。只有监听地址无法绑定时，本机功能照常，称为仅本机；数据目录或审计日志不可用时只提供诊断，称为仅诊断。失败的步骤重试成功后不重启即恢复正常。 |
| 仅本机 | local only | 降级运行的一档：只有监听地址无法绑定，本机 Agent 的登记、列表、topic 与消息照常，生成配对密钥失败，对端连不进来。 |
| 仅诊断 | diagnostics only | 降级运行的一档：数据目录或审计日志不可用，本机 socket 只回答握手与状态，登记收到拒绝事件，其他调用返回 `broker_degraded` 及原因。 |
| 下线 | offline | Broker 的一种保存在配置中的状态：拒绝对端发来的消息，链路、成员关系与名录照常，本机 Agent 之间的消息与发往对端的消息不受影响。与之相对的状态称为上线。 |
| 对外地址 | advertised address | Broker 公布的、供其他 Broker 连接它的地址，每项带有种类，种类决定怎样经这一项连到该 Broker；现有的种类只有直连，由其地址的主机和端口组成。全部对外地址按顺序构成对外地址列表。名片里写的、接受方告知拨号方的，都是整个对外地址列表。对外地址用于连接、展示和区分同一主机上的多个 Broker，不参与身份的认定。不监听的 Broker 的对外地址列表为空。 |
| 链路 | link | 两个互为对端的 Broker 之间的长连接。每对对端之间只有一条，由拨号方建立并在断开后重新建立，建立后承载双向通信。 |
| 拨号方 | dialer | 每对对端中发起配对的一方，负责建立两者之间的链路，并在链路断开后重新建立。 |
| 接受方 | acceptor | 每对对端中接受配对的一方，不主动连接对方，只等待拨号方连入，并把自己的对外地址列表告诉拨号方。 |
| 连通网络 | connected network | 由链路直接或间接连在一起的全部 Broker。 |
| 公开 Broker | public broker | 发布名片、允许自己的信息在连通网络内传播的 Broker。 |
| 名片 | broker card | 公开 Broker 签名后发布的记录，写明它的指纹、别名和对外地址列表，列表可以为空。 |
| 名录 | directory | 一个 Broker 所知道的全部名片与公开 topic 声明的集合。 |
| 对账 | reconciliation | 链路两端互相比对各自名录中的记录，补上对方缺少的记录，更新较旧的记录的过程。 |
