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
  service install [--system]  install and start the Broker as a user-level service,
                              or system-level with --system
  service uninstall [--system]
                              stop and remove the installed service; configuration,
                              identity and audit logs are kept
```

## agent-peer --version

Prints `agent-peer <version>` and exits. A binary built without the version linker flag reports `agent-peer dev`.

## agent-peer broker

Runs a Broker in the foreground until it receives `SIGINT` or `SIGTERM`. A Broker started inside a session's process tree fails with exit code 6.

- `--system` runs the system Broker. It requires root.
- `--listen <address>` overrides `broker.listen` for this run only. The address uses the same syntax as the configuration key, for example `:17443` or `127.0.0.1:17443`. An empty value is rejected; to stop listening, set `broker.listen` to an empty value in the configuration file.

Without `--listen`, the configured `broker.listen` applies.

## agent-peer mcp

Runs the MCP server for one Claude Code session over the process's standard input and standard output. Claude Code starts this process from the MCP registration, so there is normally no reason to run it by hand. The session picks a Broker in this order: the socket named by `AGENT_PEER_SOCKET`, this account's user Broker, then the system Broker. [Run a system Broker]({{< relref "/docs/guides/system-broker" >}}) lists the order and the settings. A running user Broker takes precedence over the system Broker; on an account that can use the system Broker, installing a user-level service makes its sessions use the user Broker instead of the system Broker. When no Broker is available, tool calls return an error that says what to do, and the guidance depends on the case. A first lookup that finds neither a user Broker nor a system Broker, and a user Broker whose socket has disappeared or refuses connections, as after a hard kill, get the same message: it says to ask the administrator to start a system Broker if the host has one, then gives the two user-level methods, `agent-peer service install` and `agent-peer broker` in a terminal outside Claude Code. A session that was registered on the system Broker and loses it is told only to contact the administrator. A user Broker whose socket still answers but whose handshake or a call fails is reported as unavailable, with a note that the MCP server keeps retrying in the background and no instruction to start a Broker. A Broker started inside a session fails with exit code 6.

## agent-peer status

Connects to a running Broker, without starting one, and prints two tables:

- `Agents`: each registered agent's address, account, status, topics and previous name.
- `Peers`: each peer's alias, fingerprint, this Broker's role, status, link state, advertised addresses and detail.

An empty section is printed as `Agents: none` or `Peers: none`. Without read permission on the Broker's configuration file, only the caller's own agents are listed; peers and links are still shown.

## agent-peer audit

Prints the audit log from a running Broker, one line per event, in time order, then exits. It does not start a Broker and does not follow new events while it runs.

- `--limit <n>` reads at most `n` events, the newest ones, still printed oldest first. `0` means every event and is the default. The maximum is `4294967295`.

Reading the audit log requires read permission on the Broker's configuration file. On a user Broker the account has that permission.

## agent-peer service install

Installs a service definition, enables the service, and starts the Broker immediately. It waits up to 20 seconds for the Broker to answer; a timeout fails the install and reports where the service logs go. Reinstalling replaces the running Broker with the new binary.

- Without `--system`, installs a user-level service that starts the Broker when the account logs in. It must not run as root. On macOS it must run in a graphical login session.
- With `--system`, installs a system-level service that starts the Broker at boot. It must run as root, and it creates `/etc/agent-peer/config.yaml` if the file does not exist yet.

If a Broker that this service did not start is already running at that level, the install refuses and asks you to stop it first.

On Linux the command uses systemd and runs `systemctl daemon-reload`, `systemctl enable` and `systemctl restart`. On macOS it writes and loads a launchd job. A `--system` install additionally requires the executable and every directory above it to be root-owned, not writable by group or others, and searchable by other accounts, and the executable to be executable by other accounts. An executable under the temporary directory is refused. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so choose a root-owned directory that group and others cannot write instead.

The service files and paths are listed under [Files and paths]({{< relref "docs/reference/files" >}}); [Guides]({{< relref "docs/guides" >}}) covers installing and running the service.

## agent-peer service uninstall

Stops and removes the installed service. Configuration, identity and audit logs are kept. If no service is installed, the command succeeds and reports that nothing is installed, so it can be repeated.

- Without `--system`, removes the user-level service.
- With `--system`, removes the system-level service. It must run as root.
