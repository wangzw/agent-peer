---
title: 以配置文件权限管理 Broker
linkTitle: 管理权限
weight: 6
---

工具和终端命令能改动的范围，不超出调用方账户直接修改 Broker 配置文件所能改动的范围。Broker 从操作系统为本机 socket 另一端进程提供的凭据里取得账户，不采信调用方的陈述。

## 规则

| 操作 | 对配置文件所需的权限 |
|---|---|
| `list_agents`、`list_topics`、`join_topic`、`leave_topic`、`send_message` | 无 |
| `list_brokers`、`agent-peer peer list` | 无 |
| `get_config`、`agent-peer config get`、完整的 `agent-peer status` 输出、`agent-peer audit` | 读 |
| `set_config`、`agent-peer config set` | 写 |
| `create_pairing_key`、`agent-peer pairing-key create` | 写 |
| `connect_broker`、`agent-peer peer connect` | 写 |
| `disconnect_broker`、`agent-peer peer disconnect` | 写 |

用户级 Broker 的配置文件属于运行它的账户，所以这些检查对该账户自然通过。系统级 Broker 的文件是 `/etc/agent-peer/config.yaml`，创建时属主为 root、权限 0600。读权限还允许账户查看完整状态和审计日志；写权限允许它修改设置、管理配对。两级权限都由普通的文件权限决定，不另设管理员名单。

管理员用 `sudo` 执行终端命令时，按 root 的权限判定，所以在默认 0600 的文件上，root 无需额外授权就能管理 Broker。普通账户在系统级 Broker 上默认没有写权限，在会话里生成不了配对密钥；这时由管理员执行 `sudo agent-peer pairing-key create`。

检查识别的是操作系统账户，不是人，也不是模型。同一账户下的任何进程，无论经工具还是从终端，都能发起同样的调用。

## 在系统级 Broker 上授权

创建一个组，把账户加进去，再把配置文件的权限授予这个组：

```sh
sudo groupadd agent-peer-admins
sudo usermod -aG agent-peer-admins "$USER"
sudo chgrp agent-peer-admins /etc/agent-peer/config.yaml
sudo chmod 0640 /etc/agent-peer/config.yaml
```

读权限用 `0640`，写权限用 `0660`。Broker 每次检查都从账户数据库读取账户的组，所以新的成员关系从下一次调用起生效，不需要重启会话。

能使用 Broker 和能管理 Broker 是两件事。[运行系统级 Broker]({{< relref "system-broker.md" >}})里的设置决定哪些账户能连接本机 socket，配置文件的权限决定其中哪些账户能读取或修改 Broker。

## 其他前提

- Claude Code 自己的权限设置决定一次工具调用是否需要你批准，这只管工具；终端命令不经过 Claude Code 的权限确认。文件权限检查与此独立，工具调用要文件权限检查和会话里的批准都允许才会执行。
- 工具和终端命令都不能设置 `pairing.static_key`，也不能读取它的取值。`get_config` 与 `agent-peer config get` 只报告它是否已设置，`set_config` 与 `agent-peer config set` 对它总是失败。要设置、更换或删除它，直接编辑配置文件；Broker 无需重启就会采用改动。
- 权限不足的错误不会改变任何状态：配置、对端和有效的配对密钥都保持原样。终端命令权限不足时，Broker 给出的英文错误会写到标准错误，其中包含配置文件的路径，命令以退出码 1 结束。
