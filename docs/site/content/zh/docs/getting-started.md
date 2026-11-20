---
title: 快速开始
linkTitle: 快速开始
weight: 2
---

agent-peer 让运行在不同主机上的 Claude Code 会话互相连接。会话通过 MCP 工具登记到本机的一个 Broker；两个 Broker 凭配对密钥完成配对后，处在同一个 topic 里的会话就能互发消息。

## 开始之前

agent-peer 运行在 macOS 与 Linux 上，支持 amd64 和 arm64；不支持原生 Windows。需要 Claude Code v2.1.224 或更高版本。

## 安装

agent-peer 以 `tar.gz` 归档发布，放在 [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest) 上。下载对应平台和架构的归档，以及同一发布里的 `checksums.txt`：

```sh
VERSION=0.1.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

把 `<os>_<arch>` 换成 `darwin_amd64`、`darwin_arm64`、`linux_amd64` 或 `linux_arm64`。下面的校验与解压示例用 arm64 归档；在 amd64 主机上，把 `arm64` 换成 `amd64`。

使用前先用 `checksums.txt` 校验归档。Linux 上：

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

macOS 上用 `shasum`：

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

新建一个目录，把归档解压进去：归档没有外层目录，不新建目录时，`LICENSE`、`README.md`、`third_party/` 等文件会和二进制一起直接落在当前目录。macOS 二进制没有签名，也没有经过公证，所以在 macOS 上，如果用浏览器下载了归档，运行前要清除解压出的二进制的隔离属性：

```sh
VERSION=0.1.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer  # only if a browser downloaded the archive
```

Linux 上直接解压，不需要 `xattr` 这一步：

```sh
VERSION=0.1.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_linux_arm64.tar.gz -C agent-peer-${VERSION}
```

如果只用用户级 Broker，放进 `~/.local/bin` 这类在 `PATH` 上的用户目录就够了。如果要安装系统级服务，用[把 Broker 安装为服务]({{< relref "guides/run-as-service.md" >}})里的 `sudo install` 代替下面的 `mv`：

```sh
VERSION=0.1.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

最后一条命令打印的版本与下载的版本一致。

从源码构建需要 Go 1.27。克隆仓库并构建：

```sh
VERSION=0.1.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

不带 `-ldflags` 时，`agent-peer --version` 输出 `agent-peer dev`。不支持用 `go install …@version` 安装，因为 `go.mod` 里的 `replace` 指令指向一份随仓库附带的依赖。

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

在一个会话里让模型调用 `list_agents`。结果会给出你自己的地址，以及对方会话的地址，形式为 `name@alias`，例如 `frontend@workstation.alice`；系统级 Broker 上的地址则是 `frontend@workstation`。再让它用这个地址调用 `send_message`，消息只含纯文本。

消息写入接收方会话的 inbox socket 后，发送方会话报告成功。空闲的接收会话无需用户输入就会开始处理。消息带有发送方的 Agent 地址，会话可以用 `send_message` 向这个地址发送消息。

## 在两台主机之间互发消息

两台主机中至少有一台能连到另一台上的 17443 端口。配对之前，确认两台主机上的 Broker 都在运行，且都用默认配置。

1. 在能被对方连到的那台主机上，让模型调用 `create_pairing_key` 并在权限确认中批准，或者在终端运行 `agent-peer pairing-key create`。
2. 结果给出一个一次性配对密钥、它的失效时间，以及本机 Broker 的地址。密钥和地址分开显示，地址不属于密钥。把两者经 agent-peer 之外的途径交给另一台主机上的用户。
3. 在另一台主机上，让模型用第 2 步的地址和密钥调用 `connect_broker` 并在权限确认中批准，或者运行 `agent-peer peer connect <address> --key <key>`。
4. 结果显示接受方 Broker 的别名与指纹。两台主机上处在同一个 topic 里的会话此后就能互相看见、互发消息。

`create_pairing_key`、`connect_broker` 等管理工具以及对应的终端命令，要求对 Broker 的配置文件有写权限。你自己的用户级 Broker 的配置文件属于你的账户，所以自然满足。系统级 Broker 上普通账户默认没有写权限。配对双方中任一侧是系统级 Broker 时，生成密钥和连接都需要该侧的写权限，请管理员执行 `sudo agent-peer pairing-key create` 与 `sudo agent-peer peer connect <address> --key <key>`；管理员也可以按管理权限页的做法授予写权限。权限规则见[以配置文件权限管理 Broker]({{< relref "guides/management-permissions.md" >}})。

调用 `connect_broker` 或运行 `agent-peer peer connect` 的一方是拨号方，网络中断后由它重新建立链路；接受方不主动拨号。密钥过期、别名冲突、地址连不上等情形见[两台主机配对]({{< relref "guides/pair-hosts.md" >}})。
