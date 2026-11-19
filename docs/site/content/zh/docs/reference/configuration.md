---
title: 配置键
weight: 3
---

两种部署形态各读一个 YAML 文件：用户级 Broker 读 `~/.agent-peer/config.yaml`，系统级 Broker 读 `/etc/agent-peer/config.yaml`。每个键都可选，未出现的键取默认值。Broker 会监视该文件，所以无论是手工修改、经 `set_config` 修改，还是经 `agent-peer config set` 修改，合法的改动都会在运行期间生效。两个保活键是例外：它们在建立监听或链路时读取，改动只对之后建立的监听或链路生效。

## 配置键

| 键 | 类型 | 默认值 | 含义 | `set_config`、`agent-peer config set` |
|---|---|---|---|---|
| `broker.alias` | 字符串 | 推导得出 | 本 Broker 自报的别名。未设置时由系统主机名和实际监听地址推导。取值为 1 至 24 个字符，限字母、数字、`.`、`-` 和 `_`。 | 可以 |
| `broker.public` | 布尔 | `false` | 是否发布名片，使本 Broker 为整个连通网络所知。 | 可以 |
| `broker.listen` | 字符串 | `:17443` | 绑定的地址与端口，写作 `host:port`、`:port` 或 `[v6]:port`。主机部分为空表示本机全部地址；整个值为空表示不监听。 | 可以，`--listen` 运行时除外 |
| `broker.advertise` | 字符串 | 空 | 取代自动探测到的对外地址。合法写法为空、`host`、`host:port` 和 `:port`。`host` 使用监听端口；`:port` 取默认路由所在接口的地址，并要求监听本机全部地址。非空值要求有监听地址。 | 可以 |
| `broker.advertise_extra` | 字符串列表 | 空 | 追加的对外地址。每项为 `host` 或 `host:port`；不带端口的项在 `broker.advertise` 写出了端口时取它，否则取监听端口。要求有监听地址。 | 可以 |
| `broker.address_check_interval` | 时长 | `1m` | Broker 重新读取本机地址的间隔。 | 可以 |
| `broker.local_socket_group` | 字符串 | 空 | 系统级 Broker 本机 socket 的属组。空值沿用运行时目录的属组。组必须存在于账户数据库，取值不能含空白、`:` 或 `/`。用户级 Broker 忽略此项。 | 可以 |
| `broker.local_socket_mode` | 字符串 | `0666` | 系统级 Broker 本机 socket 的文件权限，写成 3 或 4 位八进制数。用户级 Broker 固定为 `0600`。 | 可以 |
| `pairing.static_key` | 字符串（密钥） | 空 | 预置静态密钥，16 至 128 个字符。有值即生效。 | 不可以，只能改文件 |
| `pairing.max_failures` | 整数 | `5` | 一次性配对密钥作废前允许的验证失败次数。 | 可以 |
| `pairing.max_attempts_per_minute` | 整数 | `10` | 每分钟允许的、没有匹配上任何配对密钥的配对请求数。匹配上预置静态密钥的请求不计入。 | 可以 |
| `topics.auto_join` | 布尔 | `true` | 会话是否自动加入默认 topic。 | 可以 |
| `directory.reconcile_interval` | 时长 | `10m` | 对端之间对账名录的间隔。 | 可以 |
| `directory.max_records` | 整数 | `1000` | 名录记录总数的上限。 | 可以 |
| `directory.max_records_per_link` | 整数 | `250` | 经一条链路接受的记录数上限。 | 可以 |
| `directory.max_topics_per_publisher` | 整数 | `16` | 同一发布者的公开 topic 声明与未知种类记录合计上限。名片与撤销不计。 | 可以 |
| `send.link_retries` | 整数 | `5` | 链路未建立时的重试次数。 | 可以 |
| `send.link_backoff` | 时长 | `500ms` | 首次重试前的等待；之后每次翻倍，单次不超过 8 秒。 | 可以 |
| `send.request_timeout` | 时长 | `20s` | 一次发送的总时限，包含重试与等待。 | 可以 |
| `send.inbox_retries` | 整数 | `2` | inbox 确定未写入时的重试次数。 | 可以 |
| `send.inbox_backoff` | 时长 | `200ms` | inbox 写入重试之间的等待。 | 可以 |
| `send.inbox_timeout` | 时长 | `5s` | 单次 inbox 写入的期限。 | 可以 |
| `send.dedupe_window` | 时长 | `5m` | 去重表条目的保留时间。 | 可以 |
| `link.dial_timeout` | 时长 | `5s` | 拨号时对每个地址的连接期限。 | 可以 |
| `link.reconnect_min` | 时长 | `1s` | 重建链路的初始退避间隔。 | 可以 |
| `link.reconnect_max` | 时长 | `1m` | 重建链路的最大退避间隔。 | 可以 |
| `link.keepalive_interval` | 时长 | `15s` | 保活探测的间隔。gRPC 会把小于 10 秒的间隔提高到 10 秒。 | 可以 |
| `link.keepalive_timeout` | 时长 | `10s` | 保活探测的应答期限。 | 可以 |
| `log.level` | 字符串 | `info` | 运行日志级别：`debug`、`info`、`warn` 或 `error`。 | 可以 |

## 类型与约束

- 时长写成 Go 时长语法，例如 `10m`、`500ms`、`1s` 或 `1h30m`。时长必须大于零。
- 整数必须大于零，`send.link_retries` 和 `send.inbox_retries` 例外，可以取 `0`。
- `link.reconnect_max` 不得小于 `link.reconnect_min`；`link.keepalive_timeout` 必须小于 `link.keepalive_interval`。修改其中一对里的一个键时，会与另一个一起校验。
- `broker.listen` 写作 `host:port`、`:port` 或 `[v6]:port`，端口为 1 至 65535。修改为另一个值时，系统会先探测该地址，无法绑定的地址会被拒绝。
- `broker.advertise` 与 `broker.advertise_extra` 要求有监听地址；只有端口的 `broker.advertise` 还要求监听本机全部地址。
- 配置的对外地址，连同监听特定地址时隐含的那一项，最多 8 项。默认路由所在接口的地址会使列表超过 8 项时，从这部分末尾舍去；配置的项全部保留。
- `broker.local_socket_group` 必须指向存在的组，不存在时启动失败。
- `broker.local_socket_mode` 接受 3 或 4 位八进制数，最大 `0777`。
- `pairing.static_key` 接受 16 至 128 个字符；空值表示不设静态密钥。

未通过检查的值会被拒绝，配置保持原样。

## 修改配置

`set_config` 以文本接收新值，返回旧值与新值。`agent-peer config set` 在终端做同样的事，规则相同。两者都不能修改 `pairing.static_key`，在 Broker 以 `--listen` 运行时也都不能修改 `broker.listen`。手工编辑文件时，Broker 采纳其中合法的改动；某个键的值不合法时，该键保留旧值，Broker 向日志写入一条错误。

文件位置与权限见[文件与路径]({{< relref "docs/reference/files" >}})。会话默认加入哪些 topic 见[项目配置文件]({{< relref "docs/reference/project-config" >}})。
