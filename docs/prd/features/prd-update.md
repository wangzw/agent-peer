# 自动更新

## 问题陈述

用户安装 peers 之后，新版本只在 GitHub 上发布，用户不会主动去查。运行中的 Broker 因此可能长期停留在旧版本，错过修正与改进。用户需要一个不必离开终端就能发现并安装新版本的方式，同时，安装新版本等同于用新的代码替换正在运行的进程，必须由用户明确决定，不能静默完成。

## 范围

本文档覆盖 Broker 对正式发布版本的周期检查、终端状态输出与会话中的更新提示，以及终端更新命令与会话中的升级工具对正式发布版本的询问、下载、校验、替换与服务重启。系统级安装的替换与重启由 Broker 和服务管理器执行。替换二进制后，由新二进制按自己的格式重写服务定义并让服务管理器重新读取，使新版本要求的启动方式生效。

### 范围外

- 预发布版本与草稿。检查和更新只针对正式发布版本。
- 发布产物的签名与签名校验。
- 非发布构建的自动更新，例如以 `dev` 或 `e2e` 版本运行的二进制。
- 未经确认的自动安装。安装必须经用户确认：在终端回答询问、显式传入跳过询问的参数，或者在会话中批准升级工具的调用。
- 经系统包管理器分发与升级。

## 参与者

| 参与者 | 权限 |
|---|---|
| 用户 | 在终端查看状态并执行更新命令，决定是否安装新版本；对系统级安装，以对配置文件有写权限的账户身份执行，不需要 root。 |
| Agent | 在工具结果中收到更新提示；可以调用升级工具发起安装，是否执行由用户确认。 |
| 管理员 | 运行系统级 Broker，把配置文件的写权限授予需要管理它的账户；这些账户不需要 root 即可更新系统级安装。 |

## 功能需求

### REQ-update-001

Broker 必须按配置的间隔检查本项目在 GitHub 上是否有比当前版本新的正式发布版本，不含预发布与草稿。检查失败不得影响其他功能。用户必须能通过配置关闭检查，也能修改检查间隔。

- **验收**：Broker 以低于最新正式版本的版本运行，启动后在检查间隔内得知新版本；把检查关闭后重启，不再发出检查请求；让检查请求失败，列出 Agent 与发送消息照常可用。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate001_PeriodicCheck`

### REQ-update-002

有可用的新版本时，用户在终端查看状态，必须看到当前版本、可用版本以及更新方法。

- **验收**：存在可用新版本时执行查看状态的命令，输出包含两个版本号和更新命令；没有新版本时，输出中不出现更新提示。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate002_StatusShowsAvailableVersion`

### REQ-update-003

有可用的新版本时，每个启用了工具的会话中，第一次工具调用的结果必须附带一条更新提示，说明可用版本和更新方法；同一会话中同一版本只提示一次。

- **验收**：存在可用新版本时，在一个会话中连续调用两次工具，只有第一次的结果带提示；另开一个会话再调用，又出现一次提示。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate003_SessionHintOncePerSession`

### REQ-update-004

用户必须能在终端执行 `peers update [--yes]`。命令显示当前版本和可用版本，询问是否更新；用户确认后，自动下载本平台的发布包，校验后替换当前二进制；已安装为服务的，重启服务。`--yes` 跳过询问；没有终端又没有 `--yes` 时，命令报错并以状态 1 退出。

- **验收**：在安装为用户级服务的主机上执行更新命令并确认，命令结束后 `peers --version` 输出新版本，Broker 以新版本运行；用户拒绝时，二进制和服务都不变；带 `--yes` 执行时，不询问，直接完成更新。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate004_ConfirmedUpdateRestartsUserService`、`TestREQUpdate004_RefusedUpdateChangesNothing`、`TestREQUpdate004_YesSkipsPrompt`

### REQ-update-005

下载的发布包校验和不符、解压失败，或者新二进制不能以预期版本运行时，更新必须失败，现有的二进制和服务都不得改动。

- **验收**：分别让发布包的校验和不符、发布包损坏、新二进制报出其他版本号，三种情况下 `peers update` 都报错，`peers --version` 仍输出原版本，服务照常运行。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate005_FailedUpdateKeepsBinaryAndService`

### REQ-update-006

用户级安装在本地替换二进制，执行更新的账户对当前二进制所在目录没有写权限时，更新必须在下载之前失败，并说明需要以对目录有写权限的身份执行。系统级安装的更新不适用这条检查，它由 Broker 执行（`REQ-update-007`）。

- **验收**：以对当前二进制所在目录没有写权限的账户执行用户级更新命令，命令在发出任何下载请求之前就以权限不足报错，并说明所需的身份。
- **状态**：已实现
- **端到端测试**：`TestREQUpdate006_UpdateRequiresWritePermission`

### REQ-update-007

对系统级 Broker 的配置文件有写权限的账户，必须能在不取得 root 的情况下执行更新：Broker 自行下载、校验并替换二进制，由服务管理器以新二进制重新启动；更新命令在 Broker 以新版本运行之后才报告成功。Broker 不在服务管理器下运行时，这种更新必须被拒绝并说明原因。

- **验收**：在安装为系统级服务、二进制位于 root 所有目录的主机上，以对配置文件有写权限、但不是 root 的账户执行更新命令并确认。命令结束时，Broker 以新版本运行，二进制的属主与权限不变，审计中记录该账户与新旧版本；已登记的会话不重新连接 MCP 服务，下一次列出 Agent 成功。以没有写权限的账户执行时，在任何下载之前返回权限不足。在终端手工运行的系统级 Broker 上执行时，命令报错说明需要服务管理器，二进制不变。
- **状态**：已实现
- **端到端测试**：`test/e2e/system/self_update_test.go` 的 `TestREQUpdate007_SystemUpdateByAdminGroup`、`TestREQUpdate007_SystemUpdateRefusedOutsideAdminGroup`、`TestREQUpdate007_CLIWithoutUserBrokerUsesSystemBroker`、`TestREQUpdate007_CLIWithUserBrokerUsesUserBroker`、`TestREQUpdate007_RootUpdateUsesSystemBroker`、`TestREQUpdate007_SudoWithUserBrokerUsesSystemBroker`、`TestREQUpdate007_TerminalSystemBrokerNeedsServiceManager`、`TestREQUpdate007_TerminalBrokerWithInheritedServiceEnvironmentIsRefused`

### REQ-update-008

会话中必须能发起升级：Agent 可以调用一个不带参数的工具，让当前会话所连的 Broker 下载、校验并安装本项目最新的正式发布版本，然后由服务管理器以新版本重新启动该 Broker。工具在收到 Broker 的应答后立即返回，不等待重启完成。Broker 不在服务管理器下运行时，工具必须失败，并说明可以在终端执行更新命令。

- **验收**：在安装为服务的用户级与系统级 Broker 上，会话调用该工具：返回升级前的版本与目标版本，Broker 随后以新版本运行，会话不需要重新连接 MCP 服务，下一次列出 Agent 成功；已经是最新版本时返回说明，Broker 不重启；对配置文件没有写权限的账户调用时返回权限不足并给出配置文件路径，且没有发出下载请求；Broker 不在服务管理器下运行时返回错误，并给出终端替代做法；已有一次升级在进行时返回相应的错误。检查工具声明，它不带只读标记，并且与其他工具分开声明，Claude Code 的权限设置可以单独放行或拒绝它。
- **状态**：已实现
- **端到端测试**：`test/e2e/system/self_update_test.go` 的 `TestREQUpdate008_UpdateBrokerUnderUserService`、`TestREQUpdate008_UpdateBrokerUnderSystemService`、`TestREQUpdate008_UpdateBrokerAlreadyLatest`、`TestREQUpdate008_UpdateBrokerRefusedOutsideAdminGroup`；已有升级在进行时返回 `aborted` 由单元测试覆盖（`internal/broker` 的 `TestApplyUpdateConcurrentIsAborted`、`internal/mcpserver` 的 `TestUpdateBroker` 中 aborted 子用例）

### REQ-update-009

Broker 替换二进制之后、重启之前，必须用新二进制按新版本的格式重写本级别的服务定义，并让服务管理器加载它。

- **验收**：在系统级安装上从当前版本自更新到一个启动方式不同的下一版本。更新结束后，服务管理器实际加载的定义带有只有新版本才会写入的一项设置，用户此前加入的代理等环境设置仍然有效，Broker 以新版本正常运行。Linux 与 macOS 分别验收。Linux 上重新读取在更新命令收到应答之前完成，服务管理器没有加载新定义时更新命令报告失败。macOS 上重新读取由应答之后脱离任务的辅助进程执行，失败不影响已经返回的成功应答，只在审计记录与 `post-update.log` 中报告。
- **状态**：已实现
- **端到端测试**：`test/e2e/system/update_post_install_test.go` 的 `TestREQUpdate009_UpdateRewritesServiceUnitInNewFormat`；`test/e2e/darwinservice/update_test.go` 的 `TestREQUpdate007_LaunchDaemonReplacedByApplyUpdate`

### REQ-update-010

新二进制不提供更新后步骤时，Broker 必须把它视为不需要更新后步骤，继续完成更新与重启。

- **验收**：用一个不实现更新后步骤的新版本执行更新，更新成功，Broker 以新版本运行。
- **状态**：已实现
- **端到端测试**：`test/e2e/system/update_post_install_test.go` 的 `TestREQUpdate010_UpdateWithoutPostUpdateStep`；单元测试 `internal/broker/update` 的 `TestInstallPostUpdateNotNeeded` 覆盖同一契约

### REQ-update-011

更新后步骤返回其他非零退出码或超过时限时，Broker 必须把二进制回滚为旧版本、不重启，并向调用方报错说明原因。

- **验收**：在新版本的更新后步骤会失败或超时的两种情况下执行更新，更新都失败，二进制与运行中的 Broker 保持旧版本，命令输出包含原因。
- **状态**：已实现
- **端到端测试**：`test/e2e/system/update_post_install_test.go` 的 `TestREQUpdate011_FailedPostUpdateRollsBack`；超时由 `internal/broker/update` 的 `TestInstallPostUpdateTimeoutRollsBack` 覆盖，非零退出由同目录的 `TestInstallPostUpdateFailureRollsBack` 覆盖

## 假设与依赖

- 发布产物托管在 GitHub Releases，每个正式发布版本带有本平台与架构的安装包和 `checksums.txt`。
- 用户执行更新命令的主机能访问 GitHub；不能访问时，检查与更新都失败，其他功能不受影响。
- 版本号是语义化版本，当前版本必须是正式发布版本才能自动更新。
