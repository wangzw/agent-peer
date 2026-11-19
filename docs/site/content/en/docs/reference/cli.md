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
  config get [<key>]          show all configuration items, or one
  config set <key> <value>    change a configuration item
  pairing-key create          create a one-time pairing key
  peer list                   list known Brokers and their connection state
  peer connect <address> --key <key> [--alias <alias>]
                              pair with another Broker
  peer disconnect <broker>    remove a pairing
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

## Broker administration commands

The `config`, `pairing-key` and `peer` commands connect to a running Broker, without starting one. They look for it in the same order as a session: the socket named by `AGENT_PEER_SOCKET`, this account's running user Broker, then the system Broker. A running user Broker takes precedence over the system Broker, so an account that wants to manage the system Broker while its own user Broker is running must set `AGENT_PEER_SOCKET` or run the command with `sudo`. [Run a system Broker]({{< relref "/docs/guides/system-broker" >}}) lists the order and the settings.

The Broker decides the permission of each command from the account that runs it, using the same rules as the matching tool. In the audit records left by the write commands, `account` is the account that ran the command and `actor` is empty. When an administrator runs a command with `sudo`, a system Broker authorizes it against root's permissions. These commands do not pass through Claude Code's approval. When the Broker denies a command, its English error, which contains the path of the configuration file, is written to standard error and the command exits with code 1.

These commands follow the output conventions of `agent-peer status`: an empty value is written `-`.

## agent-peer config get

Prints configuration items from a running Broker.

- With no argument, prints every item, one per line as `<key> = <value>`, sorted by `key`.
- With `key`, prints only that item.

`pairing.static_key` is printed as `set` or `unset`, never by value. An unknown key fails.

Requires read permission on the Broker's configuration file; on a user Broker the account has it. A usage error exits with code 2; any other failure exits with code 1.

## agent-peer config set

Changes one configuration item and prints the change as `<key>: <old> -> <new>`.

- `key` and `value` are both required.
- `pairing.static_key` cannot be changed here; it is set in the configuration file only.

When the Broker runs with `--listen`, that option fixes `broker.listen` and `config set` cannot change it; [Configuration keys]({{< relref "docs/reference/configuration" >}}) gives the rule.

Requires write permission on the Broker's configuration file. A usage error exits with code 2; any other failure, including a key the Broker rejects, exits with code 1.

## agent-peer pairing-key create

Creates a one-time pairing key on a running Broker and prints the key, its expiry time and this Broker's advertised addresses:

```text
Key: <key>
Expires: <RFC 3339 UTC>
Addresses:
  <address>
```

Each address is printed on its own line, indented by two spaces. When the Broker does not listen, which happens when `broker.listen` is empty and no `--listen` is given at startup, it rejects the request and the command exits with code 1.

Requires write permission on the Broker's configuration file. A usage error exits with code 2; any other failure exits with code 1.

## agent-peer peer list

Lists every Broker known to a running Broker, including Brokers that are not peers, as a table with the columns `ALIAS`, `FINGERPRINT`, `PEER` (`yes` or `no`), `STATUS`, `DIRECTION` and `ADDRESSES`, sorted by alias. The `ADDRESSES` column joins the addresses with commas. With no known Broker it prints `Brokers: none`.

Requires no permission. A usage error exits with code 2; any other failure exits with code 1.

## agent-peer peer connect

Pairs this Broker with another Broker and prints one line, `Paired with <alias> (<fingerprint>)`.

- `address` is written `address:port` or `hostname(address:port)`.
- `--key <key>` is a one-time pairing key created on the other Broker, or its pre-provisioned static key.
- `--alias <alias>` sets the local alias. When the other Broker's own alias conflicts with an existing one and no local alias is given, the pairing fails and asks for a local alias.

Warnings in the reply are written to standard error, one per line with the prefix `warning: `, and the command still exits with code 0.

Requires write permission on the Broker's configuration file. A usage error exits with code 2; any other failure exits with code 1.

## agent-peer peer disconnect

Removes the pairing with a Broker and prints one line, `Disconnected <broker>`.

- `broker` is the peer's alias or its full 64-character hexadecimal fingerprint.

Requires write permission on the Broker's configuration file. A usage error exits with code 2; any other failure exits with code 1.

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
