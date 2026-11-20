---
title: 安装 agent-peer
linkTitle: 安装 agent-peer
weight: 1
---

本指南介绍安装指定版本、从发布包安装、安装脚本的选项、手动安装与从源码构建。一条命令的安装方式见[快速开始]({{< relref "/docs/getting-started" >}})；用 `agent-peer update` 保持更新，见[更新 agent-peer]({{< relref "update.md" >}})。

安装指定版本时，先设好版本号，再传 `--version`：

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --version "$VERSION"
```

系统级安装指定版本：

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --version "$VERSION" --system
```

`--version` 必须写在所有其他参数之前。它指定一个已发布的版本；这个版本没有可安装的发布包时，脚本中止并报 `version <version> not found`。其余参数原样传给发布包里的 `install.sh`，所以下一节的选项在这里同样可用。

这条命令要求 macOS 或 Linux、amd64 或 arm64；用 `curl` 取脚本，脚本内部下载发布包时用 `curl` 或 `wget`；有 `tar`；有 `sha256sum` 或 `shasum`；能访问 github.com。系统级形式还需要 `sudo`。

除非用 `--version` 指定了版本，脚本会取最新正式版本的版本号，然后把本平台的发布包和同一版本的 `checksums.txt` 下载到临时目录，校验发布包，解压后执行其中的 `install.sh`。脚本的每次下载都只走 HTTPS；无论用 `curl` 还是 `wget`，遇到跳转到 http 地址都会拒绝。下载失败或校验和不符时，脚本中止，主机上不留任何改动；脚本退出或被中断时，临时目录会被删除。脚本自身没下载完整时什么都不会执行，因为它的工作在最后一行才开始。

把脚本通过管道交给 shell，信任程度与手动从 GitHub Releases 下载发布包相同。发布没有签名，`checksums.txt` 与发布包来自同一个发布，所以校验和相符只能说明下载没有损坏，证明不了这个发布本身可信。想先看过再执行，可以先把脚本下载下来，读一遍，再在它所在的目录执行：

```sh
curl -fsSLO https://github.com/wangzw/agent-peer/releases/latest/download/install.sh
sh install.sh
```

脚本做的事情与发布包里的 `install.sh` 相同，见下一节。

## 从发布包安装

agent-peer 以 `tar.gz` 发布包的形式放在 [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest) 上。下载对应平台和架构的发布包，以及同一发布里的 `checksums.txt`：

```sh
VERSION=0.2.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

把 `<os>_<arch>` 换成 `darwin_amd64`、`darwin_arm64`、`linux_amd64` 或 `linux_arm64`。下面的校验与解压示例用 arm64 发布包；在 amd64 主机上，把 `arm64` 换成 `amd64`。

使用前先用 `checksums.txt` 校验发布包。Linux 上：

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

macOS 上用 `shasum`：

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

新建一个目录，把发布包解压进去：发布包没有外层目录，不新建目录时，`LICENSE`、`README.md`、`third_party/` 等文件会和二进制一起直接落在当前目录：

```sh
VERSION=0.2.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
```

Linux 上把 `darwin` 换成 `linux`。

进入解压出的目录并运行脚本：

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sh ./install.sh
```

系统级安装以 root 运行同一脚本，通常经 `sudo`：

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sudo sh ./install.sh --system
```

用户级形式把二进制装到 `~/.local/bin`，安装并启动用户级 Broker 服务，并为你的账户注册 MCP 服务。系统级形式把二进制装到 `/usr/local/bin`，安装并启动系统级 Broker 服务，并为执行 `sudo` 的账户注册 MCP 服务；它还会创建 `agent-peer` 组，并询问是否把这个账户加入组内。管理组的含义、这个询问及其效果见[管理组]({{< relref "management-permissions.md" >}}#管理组)。

脚本接受这些选项：

| 选项 | 作用 |
|---|---|
| `--prefix <dir>` | 把二进制装到 `<dir>`，代替默认目录：用户级安装为 `~/.local/bin`，系统级安装为 `/usr/local/bin`。 |
| `--no-service` | 不安装 Broker 服务。[启动 Broker](#启动-broker)。 |
| `--no-mcp` | 不注册 MCP 服务。 |
| `--admin-group <name>` | 改用组 `<name>` 管理系统级 Broker，代替默认的 `agent-peer` 组；组名只能以小写字母或下划线开头，其后只能是小写字母、数字、下划线或连字符。只在与 `--system` 同用时有效。 |
| `--join-admin-group` | 不询问，直接把执行 `sudo` 的账户加入管理组。只在与 `--system` 同用时有效。 |
| `--no-admin-group` | 不询问是否把执行 `sudo` 的账户加入管理组。只在与 `--system` 同用时有效。 |

`--join-admin-group` 与 `--no-admin-group` 同时给出是用法错误。

脚本可以重复执行：替换二进制，用新二进制重启服务，不会重复注册 MCP 服务。如果已经注册为别的命令，脚本保留原有注册；要改过来，先运行 `claude mcp remove -s user peer`，再执行一次脚本。脚本不访问网络，安装用的所有文件都来自解压出的发布包。

传了 `--no-mcp` 时，脚本跳过注册，需要自己执行手动一节里的注册命令。找不到 `claude`，或者不经 `sudo` 直接以 root 运行且没有可注册的账户时，脚本会打印要执行的 `claude mcp add` 命令。

对应级别的配置文件不存在时，由 `service install` 生成它，列出全部配置项：用户级服务是 `~/.agent-peer/config.yaml`，系统级服务是 `/etc/agent-peer/config.yaml`。[配置键]({{< relref "/docs/reference/configuration" >}})介绍这个文件。

macOS 上，脚本自己清除二进制上的隔离属性，所以用浏览器下载也不需要额外步骤。用户级安装目录不在 `PATH` 上时，脚本会提示；把它加入 `PATH` 才能按名字运行 `agent-peer`。macOS 上的用户级服务需要图形登录会话；没有图形会话的主机改用 `--system` 或 `--no-service`。

系统级安装要求二进制所在目录及其每一级上级目录属主为 root，组与其他人都不可写。装了 Homebrew 的 Intel Mac 上，`/usr/local/bin` 属于你的账户，安装会被拒绝；先用 `sudo mkdir -p <dir>` 建一个 root 所有的目录，再用 `--prefix` 指定它。

## 手动安装

照上一节下载、校验并解压发布包，然后手动安装二进制。下面的命令在 `agent-peer-${VERSION}` 目录的上一级执行；如果上一节已经进入该目录，先 `cd ..` 返回。

macOS 上，浏览器下载的文件带有隔离属性，清除之前 macOS 会阻止运行二进制。如果发布包是浏览器下载的，先从解压出的二进制上清除：

```sh
VERSION=0.2.0
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer
```

用 `curl` 或 `wget` 从命令行下载不会带上这个属性。

如果要安装系统级服务，用[把 Broker 安装为服务]({{< relref "run-as-service.md" >}})里的 `sudo install` 代替下面的 `mv`。

把二进制移到 `PATH` 上的目录：

```sh
VERSION=0.2.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

最后一条命令打印的版本与下载的版本一致。

### 注册 MCP 服务

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

### 启动 Broker

[用一条命令安装]({{< relref "/docs/getting-started" >}})以及用发布包脚本安装，这两种方式都会替你启动服务。如果安装时用了 `--no-service`，或者手动安装，就要自己启动 Broker。

会话只连接 Broker，不会启动它。主机上已经有你能使用的系统级 Broker 时，跳过这一步，会话会连接到它。

否则，安装用户级服务，安装完成就会启动，此后随登录启动：

```sh
agent-peer service install
```

用户级安装不能用 root 运行；在 macOS 上还需要图形登录会话，没有图形会话的主机改用系统级服务：以 root 或请管理员执行 `agent-peer service install --system`，做法见[运行系统级 Broker]({{< relref "system-broker.md" >}})。另一种做法是在 Claude Code 之外的终端里运行 Broker，并保持运行：

```sh
agent-peer broker
```

在会话里启动的 Broker 会以退出码 6 退出。启动 Broker 之后，会话不必重新连接 MCP 服务。系统级与用户级两种服务形态以及卸载方法见[把 Broker 安装为服务]({{< relref "run-as-service.md" >}})。

## 从源码构建

从源码构建需要 Go 1.27。克隆仓库并构建：

```sh
VERSION=0.2.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

不带 `-ldflags` 时，`agent-peer --version` 输出 `agent-peer dev`，这样的二进制不能用 `agent-peer update` 更新。不支持用 `go install …@version` 安装，因为 `go.mod` 里的 `replace` 指令指向一份随仓库附带的依赖。

构建产物 `agent-peer` 就在当前目录；把它放进 `PATH`、注册 MCP 服务并启动 Broker，做法见上面的手动安装一节。
