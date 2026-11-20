---
title: 快速开始
linkTitle: 快速开始
weight: 2
---

agent-peer 让运行在不同主机上的 Claude Code 会话互相连接。会话通过 MCP 工具登记到本机的一个 Broker；两个 Broker 凭配对密钥完成配对后，处在同一个 topic 里的会话就能互发消息。

## 开始之前

agent-peer 运行在 macOS 与 Linux 上，支持 amd64 和 arm64；不支持原生 Windows。需要 Claude Code v2.1.224 或更高版本。

## 安装

agent-peer 有四种安装方式，按方便程度排列：用一条命令从 GitHub Releases 安装；下载发布包，执行包里的 `install.sh`；从同一个发布包手动安装；从源码构建。从发布包安装的 agent-peer 要保持更新，运行 `agent-peer update`，详见《更新 agent-peer》指南。从源码构建的二进制版本是 `dev`，不能用这种方式更新。

无论用哪种方式安装，都要为账户注册 MCP 服务；已经打开的 Claude Code 会话要重开才能加载新的 MCP 服务，并用 `claude mcp get peer` 检查注册结果。

### 用一条命令安装

下面这条命令就是在线安装：它下载适合本机的发布包，校验后执行包里的 `install.sh`，由它安装二进制、把 Broker 安装为服务，并为你的账户注册 MCP 服务：

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

系统级安装用同一条命令，加上 `sudo` 与 `--system`：

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --system
```

用户级安装不要加 `sudo`。

安装指定版本时，先设好版本号，再传 `--version`：

```sh
VERSION=0.1.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --version "$VERSION"
```

系统级安装指定版本：

```sh
VERSION=0.1.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --version "$VERSION" --system
```

`--version` 必须写在所有其他参数之前。它指定一个已发布的版本；这个版本没有可安装的发布包时，脚本中止并报 `version <v> not found`。其余参数原样传给发布包里的 `install.sh`，所以下一节的选项在这里同样可用。

这条命令要求 macOS 或 Linux、amd64 或 arm64；用 `curl` 取脚本，脚本内部下载发布包时用 `curl` 或 `wget`；有 `tar`；有 `sha256sum` 或 `shasum`；能访问 github.com。系统级形式还需要 `sudo`。

除非用 `--version` 指定了版本，脚本会取最新正式版本的版本号，然后把本平台的发布包和同一版本的 `checksums.txt` 下载到临时目录，校验发布包，解压后执行其中的 `install.sh`。下载失败或校验和不符时，脚本中止，主机上不留任何改动；脚本退出或被中断时，临时目录会被删除。脚本自身没下载完整时什么都不会执行，因为它的工作在最后一行才开始。

把脚本通过管道交给 shell，信任程度与手动从 GitHub Releases 下载发布包相同。发布没有签名，`checksums.txt` 与发布包来自同一个发布，所以校验和相符只能说明下载没有损坏，证明不了这个发布本身可信。想先看过再执行，可以把脚本下载下来再运行：

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh -o agent-peer-install.sh
sh agent-peer-install.sh
```

脚本做的事情与发布包里的 `install.sh` 相同，见下一节。

### 从发布包安装

agent-peer 以 `tar.gz` 发布包的形式放在 [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest) 上。下载对应平台和架构的发布包，以及同一发布里的 `checksums.txt`：

```sh
VERSION=0.1.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

把 `<os>_<arch>` 换成 `darwin_amd64`、`darwin_arm64`、`linux_amd64` 或 `linux_arm64`。下面的校验与解压示例用 arm64 发布包；在 amd64 主机上，把 `arm64` 换成 `amd64`。

使用前先用 `checksums.txt` 校验发布包。Linux 上：

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

macOS 上用 `shasum`：

```sh
VERSION=0.1.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

新建一个目录，把发布包解压进去：发布包没有外层目录，不新建目录时，`LICENSE`、`README.md`、`third_party/` 等文件会和二进制一起直接落在当前目录：

```sh
VERSION=0.1.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
```

Linux 上把 `darwin` 换成 `linux`。

进入解压出的目录并运行脚本：

```sh
VERSION=0.1.0
cd agent-peer-${VERSION}
sh ./install.sh
```

系统级安装以 root 运行同一脚本，通常经 `sudo`：

```sh
VERSION=0.1.0
cd agent-peer-${VERSION}
sudo sh ./install.sh --system
```

用户级形式把二进制装到 `~/.local/bin`，安装并启动用户级 Broker 服务，并为你的账户注册 MCP 服务。系统级形式把二进制装到 `/usr/local/bin`，安装并启动系统级 Broker 服务，并为执行 `sudo` 的账户注册 MCP 服务。

脚本接受这些选项：

| 选项 | 作用 |
|---|---|
| `--prefix <dir>` | 把二进制装到 `<dir>`，代替默认目录：用户级安装为 `~/.local/bin`，系统级安装为 `/usr/local/bin`。 |
| `--no-service` | 不安装 Broker 服务。[启动 Broker](#启动-broker)。 |
| `--no-mcp` | 不注册 MCP 服务。 |

脚本可以重复执行：替换二进制，用新二进制重启服务，不会重复注册 MCP 服务。如果已经注册为别的命令，脚本保留原有注册；要改过来，先运行 `claude mcp remove -s user peer`，再执行一次脚本。脚本不访问网络，安装用的所有文件都来自解压出的发布包。

传了 `--no-mcp` 时，脚本跳过注册，需要自己执行手动一节里的注册命令。找不到 `claude`，或者不经 `sudo` 直接以 root 运行且没有可注册的账户时，脚本会打印要执行的 `claude mcp add` 命令。

`~/.agent-peer/config.yaml` 不存在时，由 `service install` 生成这个文件，列出全部配置项。[配置键]({{< relref "reference/configuration.md" >}})介绍这个文件。

macOS 上，脚本自己清除二进制上的隔离属性，所以用浏览器下载也不需要额外步骤。用户级安装目录不在 `PATH` 上时，脚本会提示；把它加入 `PATH` 才能按名字运行 `agent-peer`。macOS 上的用户级服务需要图形登录会话；没有图形会话的主机改用 `--system` 或 `--no-service`。

系统级安装要求二进制所在目录及其每一级上级目录属主为 root，组与其他人都不可写。装了 Homebrew 的 Intel Mac 上，`/usr/local/bin` 属于你的账户，安装会被拒绝；先用 `sudo mkdir -p <dir>` 建一个 root 所有的目录，再用 `--prefix` 指定它。

### 手动安装

照上一节下载、校验并解压发布包，然后手动安装二进制。下面的命令在 `agent-peer-${VERSION}` 目录的上一级执行；如果上一节已经进入该目录，先 `cd ..` 返回。

macOS 上，浏览器下载的文件带有隔离属性，清除之前 macOS 会阻止运行二进制。如果发布包是浏览器下载的，先从解压出的二进制上清除：

```sh
VERSION=0.1.0
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer
```

用 `curl` 或 `wget` 从命令行下载不会带上这个属性。

如果要安装系统级服务，用[把 Broker 安装为服务]({{< relref "guides/run-as-service.md" >}})里的 `sudo install` 代替下面的 `mv`。

把二进制移到 `PATH` 上的目录：

```sh
VERSION=0.1.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

最后一条命令打印的版本与下载的版本一致。

#### 注册 MCP 服务

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

#### 启动 Broker

用一条命令安装以及用发布包脚本安装，这两种方式都会替你启动服务。如果安装时用了 `--no-service`，或者手动安装，就要自己启动 Broker。

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

### 从源码构建

从源码构建需要 Go 1.27。克隆仓库并构建：

```sh
VERSION=0.1.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

不带 `-ldflags` 时，`agent-peer --version` 输出 `agent-peer dev`。不支持用 `go install …@version` 安装，因为 `go.mod` 里的 `replace` 指令指向一份随仓库附带的依赖。

构建产物 `agent-peer` 就在当前目录；把它放进 `PATH`、注册 MCP 服务并启动 Broker，做法见上面的手动安装一节。

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
