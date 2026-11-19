---
title: 以配置文件权限管理 Broker
linkTitle: 管理权限
weight: 6
---

工具能改动的范围，不超出调用方账户直接修改 Broker 配置文件所能改动的范围。Broker 从操作系统为本机 socket 另一端进程提供的凭据里取得账户，不采信调用方的陈述。

## 规则

| 操作 | 对配置文件所需的权限 |
|---|---|
| `list_agents`、`list_topics`、`list_brokers`、`join_topic`、`leave_topic`、`send_message` | 无 |
| `get_config` | 读 |
| `set_config`、`create_pairing_key`、`connect_broker`、`disconnect_broker` | 写 |
| 完整的 `agent-peer status` 输出、`agent-peer audit` | 读 |

用户级 Broker 的配置文件属于运行它的账户，所以这些检查对该账户自然通过。系统级 Broker 的文件是 `/etc/agent-peer/config.yaml`，创建时属主为 root、权限 0600。读权限还允许账户查看完整状态和审计日志；写权限允许它修改设置、管理配对。两级权限都由普通的文件权限决定，不另设管理员名单。

检查识别的是操作系统账户，不是人，也不是模型。同一账户下的任何进程都能发起同样的调用。

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

- Claude Code 自己的权限设置决定一次工具调用是否需要你批准。文件权限检查和会话里的批准彼此独立，两者都允许，调用才会执行。
- 工具不能设置 `pairing.static_key`，也不能读取它的取值。`get_config` 只报告它是否已设置，`set_config` 对它总是失败。要设置、更换或删除它，直接编辑配置文件；Broker 无需重启就会采用改动。
- 权限不足的错误不会改变任何状态：配置、对端和有效的配对密钥都保持原样。
