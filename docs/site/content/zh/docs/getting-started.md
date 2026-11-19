---
title: 快速开始
linkTitle: 快速开始
weight: 2
---

agent-peer 让运行在不同主机上的 Claude Code 会话互相连接。会话通过 MCP 工具登记到本机的一个 Broker；两个 Broker 凭配对密钥完成配对后，处在同一个 topic 里的会话就能互发消息。

## 开始之前

agent-peer 运行在 macOS 与 Linux 上，支持 amd64 和 arm64；不支持原生 Windows。需要 Claude Code v2.1.224 或更高版本。本站提到的 Claude Code 行为都注明了观测所用的版本，对其他版本不承诺兼容。见[设计取舍与注意事项]({{< relref "limitations.md" >}})与[简介]({{< relref "/docs" >}})。

## 安装

目前尚未发布正式版本。需要用 Go 1.27 从源码构建：

```sh
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -o agent-peer ./cmd/agent-peer
```

把生成的二进制放进 `PATH` 上的某个目录，或者记下它的绝对路径，下一步会用到。

## 注册 MCP 服务

为账户注册一次 MCP 服务：

```sh
claude mcp add --scope user peer -- agent-peer mcp
```

服务名必须是 `peer`。Claude Code 把工具显示为 `mcp__peer__<tool>`，权限规则也按这个名字写。如果 `agent-peer` 不在 `PATH` 上，就换成它的绝对路径。

检查注册结果：

```sh
claude mcp get peer
```

输出里有 `Scope: User config`。此后启动的会话在 `/mcp` 里能看到 `peer`。

## 启动 Broker

会话只连接 Broker，不会启动它。主机上已经有你能使用的系统级 Broker 时，跳过这一步，会话会连接到它。

否则，安装用户级服务，安装完成就会启动，此后随登录启动：

```sh
agent-peer service install
```

用户级安装不能用 root 运行；在 macOS 上还需要图形登录会话，没有图形会话的主机改用系统级服务：以 root 或请管理员执行 `agent-peer service install --system`，做法见[运行系统级 Broker]({{< relref "guides/system-broker.md" >}})。另一种做法是在 Claude Code 之外的终端里运行 Broker，并保持运行：

```sh
agent-peer broker
```

在会话里启动的 Broker 会以退出码 6 退出。没有 Broker 在运行时，工具调用返回错误，说明该怎么做，具体提示分三种情形。第一次查找时用户级与系统级 Broker 都找不到，或者用户级 Broker 的 socket 消失、拒绝连接（例如被强杀之后），报的是同一条：先提示本机如有系统级 Broker 请管理员启动它，再给出两种用户级启动方法。原先连在系统级 Broker 上的会话断开后，只提示联系管理员。用户级 socket 还在应答，但握手或调用失败时，报告 Broker 不可用，并在后台重试，不给出启动方法。启动 Broker 之后，会话不必重新连接 MCP 服务。系统级与用户级两种服务形态以及卸载方法见[把 Broker 安装为服务]({{< relref "guides/run-as-service.md" >}})。

## 在同一台主机上互发消息

在同一个项目目录里打开两个会话，都启用 agent-peer 工具。

两个会话在同一个带有 `origin` 地址的 git 仓库里，或者在同一个项目配置文件 `.agent-peer.yaml` 下时，无需额外设置就会加入同一个默认 topic。topic 取自工作目录或上层目录里的项目配置文件 `.agent-peer.yaml`；没有这个文件时，取自工作区 git 仓库的 `origin` 地址。origin 会规范化为 `github.com/org/repo` 这样的名字，因此同一个仓库的每个克隆得到同一个 topic。

两者都不满足时，两个会话不加入任何 topic，在对方的 `list_agents` 结果里也看不到彼此。这时让两个会话都用 `join_topic` 加入同一个 topic。

在一个会话里让模型调用 `list_agents`。结果会给出你自己的地址，以及对方会话的地址，形式为 `name@alias`，例如 `frontend@workstation-17443`。再让它用这个地址调用 `send_message`，消息只含纯文本。

消息写入接收方会话的 inbox socket 后，发送方会话报告成功。在 Claude Code v2.1.327 上观测到，空闲的接收会话无需用户输入就会开始处理；不承诺兼容其他版本。消息带有发送方的 Agent 地址，会话可以用 `send_message` 向这个地址发送消息。

## 在两台主机之间互发消息

两台主机中至少有一台能连到另一台上的 17443 端口。配对之前，确认两台主机上的 Broker 都在运行，且都用默认配置。

1. 在能被对方连到的那台主机上，让模型调用 `create_pairing_key`，并在权限确认中批准。
2. 结果给出一个一次性配对密钥、它的失效时间，以及本机 Broker 的地址。密钥和地址分开显示，地址不属于密钥。把两者经 agent-peer 之外的途径交给另一台主机上的用户。
3. 在另一台主机上，让模型用第 2 步的地址和密钥调用 `connect_broker`，并在权限确认中批准。
4. 结果显示接受方 Broker 的别名与指纹。两台主机上处在同一个 topic 里的会话此后就能互相看见、互发消息。

`create_pairing_key`、`connect_broker` 等管理工具要求对 Broker 的配置文件有写权限。你自己的用户级 Broker 的配置文件属于你的账户，所以自然满足。系统级 Broker 的情形见[以配置文件权限管理 Broker]({{< relref "guides/management-permissions.md" >}})。

调用 `connect_broker` 的一方是拨号方，网络中断后由它重新建立链路；接受方不主动拨号。密钥过期、别名冲突、地址连不上等情形见[两台主机配对]({{< relref "guides/pair-hosts.md" >}})。
