---
title: 配对
weight: 4
---

配对是两个 Broker 建立信任的过程。接受方生成一次性配对密钥，发起方用它连接对方；握手成功后，两个 Broker 各自保存对方的身份，之后凭公钥指纹确认对方。整个过程不涉及证书颁发机构，信任是一对一对建立的。

## 配对密钥

接受方用 `create_pairing_key` 或 `agent-peer pairing-key create` 生成一次性配对密钥。密钥在 10 分钟内有效，成功配对一次后失效。用户把密钥和 Broker 的地址经 agent-peer 之外的途径交给对方，地址不是秘密，可以另走一条途径。

Broker 也可以使用用户或管理员写在配置文件里的预置静态密钥。这种密钥可以用于任意多次配对，不会过期，agent-peer 从不生成它。

发起方调用 `connect_broker` 或运行 `agent-peer peer connect <address> --key <key>`，传入对方的地址和密钥。地址写成 `host:port`，或者按 `create_pairing_key` 的结果或 `agent-peer pairing-key create` 的输出，写成 `hostname(host:port)` 形式。密钥错误、已过期或已被使用时，配对失败，双方都不保存对方的身份。

## 配对之后

两个 Broker 互为对端。新对端的别名如果与本 Broker 自己的别名相同，或已被另一个对端使用，发起方要先指定一个本地别名，配对才能完成：工具调用里用 `local_alias`，命令行上加 `--alias`。

配对关系在任一 Broker 重启后仍然保留。网络中断后重新建立链路，不需要配对密钥。

别名与指纹见 [Broker]({{< relref "/docs/concepts/broker" >}})；配对之后建立连接见[链路]({{< relref "/docs/concepts/links" >}})；配对的操作步骤见[两台主机配对]({{< relref "/docs/guides/pair-hosts" >}})；`connect_broker` 与静态密钥见 [MCP 工具]({{< relref "/docs/reference/mcp-tools" >}})和[配置键]({{< relref "/docs/reference/configuration" >}})。
