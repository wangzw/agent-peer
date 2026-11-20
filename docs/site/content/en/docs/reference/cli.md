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
  update [--yes]              update to the latest release and restart the
                              installed service; --yes skips the question
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

When a newer formal release is available, `agent-peer status` prints one more line at the end:

```text
Update: <v> available (current <c>); run agent-peer update
```

No newer release means no such line. Turning off `update.check` removes the line: the Broker stops checking and stops reporting an available version. [Configuration keys]({{< relref "docs/reference/configuration" >}}) lists the two update keys.

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

## agent-peer update

Checks GitHub for the latest formal release and, after confirmation, replaces the running binary and restarts the installed service.

```text
usage: agent-peer update [--yes]
```

The command queries GitHub directly and does not contact a Broker, so it works with no Broker running. It replaces the file that the executable resolves to, so a symbolic link keeps pointing at the updated file. Replacing a system-level installation, which lives in a root-owned directory, requires `sudo agent-peer update`; that run restarts only the system-level service.

- `--yes` skips the confirmation question. Without it the command asks `Update to <v>? [y/N]` and accepts `y` or `yes`, in any case, as confirmation. When standard input is not a terminal, the command refuses to run without `--yes`.

The command proceeds in this order:

1. Refuses a version that is not a formal release. A binary built from source without a release version, which reports `agent-peer dev`, cannot update itself.
2. Locates the running executable and resolves symbolic links.
3. Checks that it can write in the target's directory, before any download. Without permission it fails and suggests `sudo agent-peer update`.
4. Refuses to continue when standard input is not a terminal and `--yes` is not given.
5. Queries the latest formal release; drafts and prereleases are ignored. When the current version is already the latest, it prints `agent-peer <c> is up to date` and exits with code 0.
6. Prints the current and available versions, the release page when there is one, and the confirmation question.
7. Downloads the archive for this platform and `checksums.txt`, verifies the archive's SHA-256, extracts the regular file named `agent-peer`, and runs the new binary's `--version` to confirm that it reports the expected version.
8. Writes the new binary next to the target, gives it the same owner and mode as the old one, and renames it into place. The old version is not kept.
9. Restarts the installed service at the caller's level: the system-level service when run as root, the user-level service otherwise. Without an installed service it asks you to restart the running Broker yourself.
10. Reminds you that open Claude Code sessions must reconnect with `/mcp` or be restarted.

The output of a successful update looks like this. `Release:` appears only when the release has a URL.

```text
agent-peer 0.1.1 is installed; version 0.1.2 is available.
Release: https://github.com/wangzw/agent-peer/releases/tag/v0.1.2
Update to 0.1.2? [y/N] y
agent-peer 0.1.2 is installed.
Restarted the agent-peer service.
User-level Brokers that run this binary keep the old version until they are restarted, for example with `agent-peer service install` as that user.
Open Claude Code sessions must reconnect with /mcp or be restarted to use the new version.
```

With `--yes` the question is not printed. When the user answers `n`, the command prints `Update canceled; the binary and service were not changed.` and exits with code 0. Without an installed service, the restart line is replaced by `The Broker is not installed as a service; restart it to use the new version.`

Failures are written to standard error with the prefix `agent-peer update: `. The messages include:

- `version "dev" is not a release build; only release builds can update themselves`
- `cannot write to <dir>: ...`, followed by `run "sudo agent-peer update" to update a system-level installation`
- `standard input is not a terminal; run "agent-peer update --yes" to update without asking`
- `checksum mismatch for <archive>`
- `downloaded binary reported "<x>", want "agent-peer <v>"`
- `restarting the service: ...`

A usage error exits with code 2. Any other failure exits with code 1. A successful update, an up-to-date result and a canceled update all exit with code 0.

## agent-peer service install

Installs a service definition, enables the service, and starts the Broker immediately. It waits up to 20 seconds for the Broker to answer; a timeout fails the install and reports where the service logs go. Reinstalling replaces the running Broker with the new binary.

- Without `--system`, installs a user-level service that starts the Broker when the account logs in. It must not run as root. On macOS it must run in a graphical login session. If `~/.agent-peer/config.yaml` does not exist, it generates the file, owned by the account that runs the install with mode `0600`; an existing file is left unchanged.
- With `--system`, installs a system-level service that starts the Broker at boot. It must run as root. If `/etc/agent-peer/config.yaml` does not exist, it generates the file, owned by root with mode `0600`; an existing file is left unchanged.

The generated file is a template that lists every configuration key: keys with a fixed default are written out, and the remaining keys are comments with an example value or an empty placeholder. When the install writes the file it prints `generated the configuration file <path>`; when the file already exists it prints `kept the existing configuration file <path>`.

If a Broker that this service did not start is already running at that level, the install refuses and asks you to stop it first.

On Linux the command uses systemd and runs `systemctl daemon-reload`, `systemctl enable` and `systemctl restart`. On macOS it writes and loads a launchd job. A `--system` install additionally requires the executable and every directory above it to be root-owned, not writable by group or others, and searchable by other accounts, and the executable to be executable by other accounts. An executable under the temporary directory is refused. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so choose a root-owned directory that group and others cannot write instead.

The service files and paths are listed under [Files and paths]({{< relref "docs/reference/files" >}}); [Guides]({{< relref "docs/guides" >}}) covers installing and running the service.

## agent-peer service uninstall

Stops and removes the installed service. Configuration, identity and audit logs are kept. If no service is installed, the command succeeds and reports that nothing is installed, so it can be repeated.

- Without `--system`, removes the user-level service.
- With `--system`, removes the system-level service. It must run as root.
