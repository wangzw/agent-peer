---
title: Update agent-peer
linkTitle: Update agent-peer
weight: 7
---

agent-peer checks for newer releases of itself, and you install them from a terminal when you choose to. The check is automatic; the installation asks for confirmation first unless you pass `--yes`. Upgrading also changes the default alias of a Broker that has no `broker.alias` set, so the agent addresses your peers see can change; see [Why did agent addresses change after upgrading?]({{< relref "/docs/faq" >}}).

## Check for new releases

A release build of the Broker checks GitHub shortly after it starts: it waits about one minute, checks once, and then checks every 24 hours by default. The check runs in the Broker process, so one Broker covers every session that connects to it.

When a newer release exists, you see it in two places. The last line of `agent-peer status` is:

```text
Update: 0.1.2 available (current 0.1.1); run agent-peer update
```

When a newer release is available, a tool result in the session ends with:

```text
agent-peer 0.1.2 is available (this host runs 0.1.1). Update it with `agent-peer update` in a terminal, using sudo for a system-level installation.
```

A session shows the notice once for a given version. If a newer release appears while the session stays open, a later tool call shows the notice again. The MCP server queries the Broker for the update state on its first tool call and then at most once every 10 minutes. A build that is not a formal release, such as a binary built from source without `-ldflags`, does not check and never shows either line.

Two configuration keys control the check: `update.check` turns it on and off, and `update.check_interval` sets the period, 24 hours by default and at least one hour. [Configuration keys]({{< relref "/docs/reference/configuration" >}}) gives their values and defaults.

## Update the binary

Run the command in a terminal:

```sh
agent-peer update
```

It prints the installed and available versions together with the release page, and asks before changing anything. Answering `y` or `yes` goes ahead; any other answer cancels the update, and the binary and the service stay as they were. With a newer version available, the output looks like this:

```text
agent-peer 0.1.1 is installed; version 0.1.2 is available.
Release: https://github.com/wangzw/agent-peer/releases/tag/v0.1.2
Update to 0.1.2? [y/N] y
agent-peer 0.1.2 is installed.
Restarted the agent-peer service.
User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user.
Open Claude Code sessions must reconnect with /mcp or be restarted to use the new version.
```

Add `--yes` to skip the question. When standard input is not a terminal, as in a script or a scheduled job, the command requires `--yes` and otherwise refuses with `standard input is not a terminal; run "agent-peer update --yes" to update without asking`. When the installed version is already the latest, the command runs without `--yes` even when standard input is not a terminal.

Before downloading anything, the command checks that it can write to the directory that holds the binary. It then downloads the archive for this platform and this architecture, checks it against `checksums.txt`, and runs the new binary to make sure that it reports the expected version. Only then does it replace the binary, keeping the original mode and owner. If any step before the replacement fails, the command stops and leaves the binary and the service unchanged. If restarting the service fails after the replacement, the new binary is already installed; restart the service yourself as the error message describes.

When the installed version is already the latest, the command prints `agent-peer 0.1.1 is up to date` and exits without changing anything.

[Command line]({{< relref "/docs/reference/cli" >}}) gives the full description of the command.

## Update a system-level installation

A system-level binary lives in a directory that your account cannot write, so run the command with `sudo`:

```sh
sudo agent-peer update
```

The write-permission check runs before any download, so an ordinary account fails immediately and is told to run `sudo agent-peer update`. `sudo` drops the proxy environment variables by default; when a proxy is needed, keep them with `sudo -E` or an `env_keep` entry such as `env_keep += "HTTP_PROXY HTTPS_PROXY NO_PROXY"`. With `sudo -E` the command is:

```sh
sudo -E agent-peer update
```

A system-level update restarts only the system-level service. If other accounts run user-level Brokers from the same binary, each of those accounts has to restart its own Broker, for example by running `agent-peer service install` as that user, before its sessions use the new version. The command prints this reminder just before the line about reconnecting sessions.

## Restart a Broker you started yourself

The update command restarts a Broker that is installed as a service. When no service is installed, it prints `The Broker is not installed as a service; restart it to use the new version.` Stop the Broker and start it again the same way you started it, for example with `agent-peer broker` in a terminal, so that it runs the new binary.

## Reconnect open sessions

An update does not reconnect sessions that are already open. Each open session keeps running the previous version of the MCP server until you reconnect it with `/mcp` or restart the session. The update command prints a reminder about this as its last line.

## Which command applies to your installation

| Installation | Command | What happens to the service |
|---|---|---|
| User-level: the online install, `sh ./install.sh`, or a binary you placed in `~/.local/bin` | `agent-peer update` | The user-level service restarts if one is installed. |
| System-level: the online install with `--system`, `sudo sh ./install.sh --system`, or a manual `sudo install` into `/usr/local/bin` | `sudo agent-peer update` | Only the system-level service restarts. |
| Built from source with the release version in `-ldflags` | `agent-peer update` | The command replaces the binary with the binary from the release archive. |

A binary built from source without `-ldflags` reports the version `dev`. It is not a release build, and the command refuses with `version "dev" is not a release build; only release builds can update themselves`. Install a release to update in place.

If the binary is a symbolic link, the command resolves the link and replaces the file it points to.

## Turn off update checks

To stop the Broker from checking:

```sh
agent-peer config set update.check false
```

The notice disappears at once: `agent-peer status` no longer prints the `Update` line, and sessions no longer show the update notice. A host without access to GitHub can leave the check off; `agent-peer update` contacts GitHub only when you run it yourself.

[The security model]({{< relref "/docs/security" >}}) describes the network access the check makes.
