---
title: Command line
weight: 2
---

The `agent-peer` binary runs the Broker, the MCP server and the terminal commands. Run it without arguments, or with `-h`, `--help` or `help`, to print the usage below.

```
usage: agent-peer <command> [arguments]

Commands:
  --version                   print the version and exit
  broker [--system] [--listen <address>]
                              run the Broker
  mcp                         run the MCP server for one Claude Code session
  status                      show the state of a running Broker
  audit [--limit <n>]         read the audit log
  service install [--system]  install the Broker as a user-level service, or system-level with --system
```

## agent-peer --version

Prints `agent-peer <version>` and exits. A binary built without the version linker flag reports `agent-peer dev`.

## agent-peer broker

Runs a Broker in the foreground until it receives `SIGINT` or `SIGTERM`.

- `--system` runs the system Broker. It requires root.
- `--listen <address>` overrides `broker.listen` for this run only. The address uses the same syntax as the configuration key, for example `:17443` or `127.0.0.1:17443`. An empty value is rejected; to stop listening, set `broker.listen` to an empty value in the configuration file.

Without `--listen`, the configured `broker.listen` applies.

## agent-peer mcp

Runs the MCP server for one Claude Code session over the process's standard input and standard output. Claude Code starts this process from the MCP registration, so there is normally no reason to run it by hand. The session picks a Broker in this order: the socket named by `AGENT_PEER_SOCKET`, this account's user Broker, then the system Broker. [Run a system Broker]({{< relref "/docs/guides/system-broker" >}}) lists the order and the settings. If none is available, the process starts a user Broker for the account in the background.

## agent-peer status

Connects to a running Broker, without starting one, and prints two tables:

- `Agents`: each registered agent's address, account, status, topics and previous name.
- `Peers`: each peer's alias, fingerprint, this Broker's role, status, link state, advertised addresses and detail.

An empty section is printed as `Agents: none` or `Peers: none`. Without read permission on the Broker's configuration file, only the caller's own agents are listed and the peer section is empty.

## agent-peer audit

Prints the audit log from a running Broker, one line per event, in time order, then exits. It does not start a Broker and does not follow new events while it runs.

- `--limit <n>` reads at most `n` events, the newest ones, still printed oldest first. `0` means every event and is the default. The maximum is `4294967295`.

Reading the audit log requires read permission on the Broker's configuration file. On a user Broker the account has that permission.

## agent-peer service install

Installs a service definition and enables it. It does not start the service; the last line of output is the command that starts it now.

- Without `--system`, installs a user-level service that starts the Broker when the account logs in. It must not run as root.
- With `--system`, installs a system-level service that starts the Broker at boot. It must run as root and also creates `/etc/agent-peer/config.yaml` if the file does not exist yet.

On Linux the command uses systemd and runs `systemctl daemon-reload` and `systemctl enable`. On macOS it writes a launchd job. A `--system` install additionally requires the executable and every directory above it to be root-owned, not writable by group or others, and searchable by other accounts, and the executable to be executable by other accounts. An executable under the temporary directory is refused.

The service files and paths are listed under [Files and paths]({{< relref "docs/reference/files" >}}); [Guides]({{< relref "docs/guides" >}}) covers installing and running the service.
