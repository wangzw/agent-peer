---
title: Command line
weight: 2
---

The `agent-peer` binary runs the Broker, the MCP server and the terminal commands. Run it without arguments, or with `-h`, `--help` or `help`, to print the usage below.

```
usage: agent-peer <command> [arguments]

Commands:
  --version                   print the version and exit
  broker [--system] [--listen <address>] [--allow-degraded]
                              run the Broker
  mcp                         run the MCP server for one Claude Code session
  status                      show the state of a running Broker
  offline                     refuse messages from other hosts
  online                      accept messages from other hosts again
  audit [--limit <n>]         read the audit log
  config get [<key>]          show all configuration items, or one
  config set <key> <value>    change a configuration item
  pairing-key create          create a one-time pairing key
  peer list                   list known Brokers and their connection state
  peer connect <address> --key <key> [--alias <alias>]
                              pair with another Broker
  peer disconnect <broker>    remove a pairing
  update [--yes]              update through the selected Broker, or replace
                              the user-level binary locally; --yes skips the question
  service install [--system] [--admin-group <name>]
                              install and start the Broker as a user-level service,
                              or system-level with --system; --admin-group names the
                              group that administers the system-level Broker
  service start               start the installed service for this account, or
                              the system-level service as root
  service stop                stop it without disabling it
  service restart             restart it
  service uninstall [--system]
                              stop and remove only the installed service; configuration,
                              identity and audit logs are kept
  uninstall [--purge]         remove the installation for this account, or the
                              system-level installation as root; --purge deletes
                              configuration, identity, audit and service logs,
                              and at the system level the admin group
```

## agent-peer --version

Prints `agent-peer <version>` and exits. A binary built without the version linker flag reports `agent-peer dev`.

## agent-peer broker

Runs a Broker in the foreground until it receives `SIGINT` or `SIGTERM`. A Broker started inside a session's process tree fails with exit code 6.

- `--system` runs the system Broker. It requires root.
- `--listen <address>` overrides `broker.listen` for this run only. The address uses the same syntax as the configuration key, for example `:17443` or `127.0.0.1:17443`. An empty value is rejected; to stop listening, set `broker.listen` to an empty value in the configuration file.
- `--allow-degraded` keeps the Broker running after certain startup failures instead of exiting. An unavailable listen address, including one rejected by the low-port rule, leaves it serving local sessions in a local-only degraded run; an unavailable data directory or audit log leaves it in a diagnostics-only run. Either way it answers on the local socket and reports the failure. Without the option, a startup failure reports its cause on standard error and exits, and an unavailable listen address exits with code 4. Configuration errors, a `broker.local_socket_group` that does not exist, and failures before the account lock exit either way. The service definition always passes the option; a foreground run does not. A local-only run starts listening by itself once the address becomes available or `broker.listen` changes. A diagnostics-only run retries when the configuration file changes and once a minute, and `agent-peer service restart` also works after the fix. Neither recovery restarts the process.

Without `--listen`, the configured `broker.listen` applies.

## agent-peer mcp

Runs the MCP server for one Claude Code session over the process's standard input and standard output. Claude Code starts this process from the MCP registration, so there is normally no reason to run it by hand. The session uses this account's user Broker when one is running; otherwise it uses the system Broker if this account may connect to it. [Run a system Broker]({{< relref "/docs/guides/system-broker" >}}) explains the choice and the settings. A running user Broker takes precedence over the system Broker; on an account that can use the system Broker, installing a user-level service makes its sessions use the user Broker instead of the system Broker. When no Broker is available, tool calls return an error that says what to do; [the no-Broker question in the FAQ]({{< relref "/docs/faq" >}}#what-happens-when-no-broker-is-running) has the details.

## agent-peer status

Connects to a running Broker, without starting one, and prints two tables:

- `Agents`: each registered agent's address, account, status, topics and previous name.
- `Peers`: each peer's alias, fingerprint, this Broker's role, status, link state, advertised addresses and detail.

An empty section is printed as `Agents: none` or `Peers: none`. Without read permission on the Broker's configuration file, only the caller's own agents are listed; peers and links are still shown. When this Broker is offline, a `Broker: offline; messages from other hosts are refused. Run agent-peer online to accept them.` line comes before the tables.

When the Broker runs degraded, the lines `Broker: degraded (local only): <reason>` or `Broker: degraded (diagnostics only): <reason>` and `Fix: <fix>` come before the tables. A local-only run still prints both tables. A diagnostics-only run prints only these two lines and exits with code 1.

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

The `config`, `pairing-key`, `peer`, `offline` and `online` commands connect to a running Broker, without starting one. They choose the Broker by the operating system account that runs them, with no command-line option to select a level: this account's running user Broker first, then the system Broker this account may use. An account that runs its own user Broker and wants to manage the system Broker must run the command with `sudo`. The same rule applies to `status`, `audit` and `update`. [Run a system Broker]({{< relref "/docs/guides/system-broker" >}}) explains the choice and the settings.

The Broker decides the permission of each command from the account that runs it, using the same rules as the matching tool. In the audit records left by the write commands, `account` is the account that ran the command and `actor` is empty. When an administrator runs a command with `sudo`, a system Broker authorizes it against root's permissions. A member of the system Broker's admin group can manage it without `sudo` when the account runs no user-level Broker of its own; [Manage a Broker by file permission]({{< relref "/docs/guides/management-permissions" >}}) explains the group. These commands do not pass through Claude Code's approval. When the Broker denies a command, its English error, which contains the path of the configuration file, is written to standard error and the command exits with code 1.

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

Lists every Broker known to a running Broker, including Brokers that are not peers, as a table with the columns `ALIAS`, `FINGERPRINT`, `PEER` (`yes` or `no`), `STATUS`, `DIRECTION` and `ADDRESSES`, sorted by alias. The `ADDRESSES` column joins the addresses with commas. With no known Broker it prints `Brokers: none`. When this Broker is offline, the same `Broker: offline; …` line comes before the table. When it runs degraded, the same `Broker: degraded …` and `Fix: …` lines come before the table; a diagnostics-only run writes the reason to standard error instead and exits with code 1.

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

## agent-peer offline and online

`agent-peer offline` makes this Broker refuse messages sent by other hosts, and `agent-peer online` accepts them again. The commands print `Broker is now offline` and `Broker is now online` respectively.

A message that another host sends to an agent on an offline Broker is refused with the error code `broker_offline`, reported by this Broker. Agents on this host still reach each other, and an agent on this host can still send to another host, but the receiver's reply is refused; run `agent-peer online` before a two-way exchange. Links, topic membership, directories and new pairings keep working. A peer learns that this Broker is offline only when a send is refused; its Broker list and link state do not show it.

The state is the `broker.offline` configuration key, so it survives a restart. The commands need write permission on the configuration file, like any other configuration change; without it, they fail with `permission_denied` and an error that includes the path of the configuration file. Each change is written to the audit log with the account that ran the command.

## agent-peer update

Installs the latest formal release, either by asking the selected Broker to update itself or by replacing the binary in place.

```text
usage: agent-peer update [--yes]
```

The command installs only a newer formal release: drafts and prereleases are ignored, there is no option to choose a version, and it never downgrades.

- `--yes` skips the confirmation question. Without it the command asks `agent-peer <c> is installed. Update to the latest release? [y/N]`, where `<c>` is the current version, and accepts `y` or `yes`, in any case, as confirmation. When standard input is not a terminal, the command refuses to run without `--yes`. When the user answers `n`, it prints `Update canceled; nothing was changed.` and exits with code 0.
- A usage error exits with code 2. Any other failure exits with code 1. A successful update, an up-to-date result and a canceled update all exit with code 0.

The command first checks that this binary is a formal release build, then asks for confirmation unless `--yes` is given, and only then selects the Broker. It replaces the binary locally only when no Broker is running or when the selected user-level Broker does not run under a service manager; a system-level Broker that does not run under a service manager makes the command fail instead.

### Through the Broker

When the command selects a running Broker, that Broker installs the update itself. A member of the system Broker's admin group can update it without `sudo`; an account that runs its own user Broker reaches the system Broker with `sudo agent-peer update`.

1. Sends the request to the selected Broker. The Broker decides permission from write access to its configuration file; the command does not check it first. On a denial the Broker's English error is written to standard error and the command exits with code 1, with nothing downloaded.
2. When the current version is already the latest, the command prints `agent-peer <c> is up to date` and exits with code 0.
3. The Broker downloads the archive for this platform and `checksums.txt`, verifies the archive's SHA-256, extracts the regular file named `agent-peer`, and runs the new binary's `--version` to confirm that it reports the expected version. It then replaces its own binary. A symbolic link keeps pointing at the updated file.
4. The Broker answers before it restarts. The command prints `The Broker is restarting to version <v>.` and waits until the restarted Broker reports that version, because the update is complete only when that process runs. On success it prints `The Broker is running agent-peer <v>.` and then the session notice. When the restarted Broker does not report the new version in time, the command fails and names where that level's service log is.
5. The session notice reads `Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically.`

A user-level Broker that was started from a terminal, rather than by a service manager, cannot replace and restart itself. It leaves the command to the local replacement below.

### By replacing the binary

With no running Broker, or after the user-level Broker above declines, the command replaces the binary in place:

1. Locates the running executable and resolves symbolic links. Only the file they point at is replaced.
2. Checks that it can write in the target's directory, before any download. Without permission it prints `cannot write to <dir>: ...`, then `run "sudo agent-peer update" to update a system-level installation`, and exits with code 1.
3. Queries GitHub for the latest formal release. When the current version is already the latest, it prints `agent-peer <c> is up to date` and exits with code 0.
4. Downloads the archive for this platform and `checksums.txt`, verifies the archive's SHA-256, extracts the regular file named `agent-peer`, and runs the new binary's `--version` to confirm that it reports the expected version. It prints `agent-peer <c> is installed; version <v> is available.` and, when the release has a URL, `Release: <url>`.
5. Writes the new binary next to the target, gives it the same owner and mode as the old one, and renames it into place. The old version is not kept.
6. Starts no service and restarts none, because the service may have been stopped on purpose. It prints `The new binary takes effect the next time the Broker starts.`

The output of a successful update looks like one of these. `Release:` appears only when the release has a URL.

Through the Broker:

```text
agent-peer 0.2.0 is installed. Update to the latest release? [y/N] y
The Broker is restarting to version 0.2.1.
The Broker is running agent-peer 0.2.1.
Sessions that are already open keep the old MCP server until they are reopened; this Broker's connections recover automatically.
```

By replacing the binary:

```text
agent-peer 0.2.0 is installed. Update to the latest release? [y/N] y
agent-peer 0.2.0 is installed; version 0.2.1 is available.
Release: https://github.com/wangzw/agent-peer/releases/tag/v0.2.1
The new binary takes effect the next time the Broker starts.
```

Failures are written to standard error with the prefix `agent-peer update: `. The messages include:

- `version "dev" is not a release build; only release builds can update themselves`
- `cannot write to <dir>: ...`, followed by `run "sudo agent-peer update" to update a system-level installation`
- `standard input is not a terminal; run "agent-peer update --yes" to update without asking`
- `checksum mismatch for <archive>`
- `downloaded binary reported "<x>", want "agent-peer <v>"`
- `the Broker did not report version <v> within <duration>; check the service log with <command or path>`; when the last status query failed, its reason is in parentheses after the duration: `within <duration> (<error>)`
- `the Broker is not running under a service manager, so it cannot start the new binary again; install it as a service and run the update against that service (for example, "agent-peer service install" or "sudo agent-peer service install --system")`
- `another update is already in progress`

When the update goes through a Broker, a checksum or version mismatch appears after `installing <v> failed: `, and a failure to look up the latest release appears after `looking up the latest release failed: `.

## agent-peer service install

Installs a service definition, enables the service, and starts the Broker immediately. It waits up to 20 seconds for the Broker to answer; a timeout fails the install, reports where the service logs go, and includes the reason from the last `startup_failed` entry in that log. Reinstalling replaces the running Broker with the new binary. [Install agent-peer]({{< relref "/docs/guides/install" >}}#installation-output) lists what the install reports.

- Without `--system`, installs a user-level service that starts the Broker when the account logs in. It must not run as root. On macOS it must run in a graphical login session. If `~/.agent-peer/config.yaml` does not exist, it generates the file, owned by the account that runs the install with mode `0600`; an existing file is left unchanged.
- With `--system`, installs a system-level service that starts the Broker at boot. It must run as root. The command creates the `agent-peer` group when it does not exist, and uses it without changing its members when it does. If `/etc/agent-peer/config.yaml` does not exist, it generates the file, owned by `root` with group `agent-peer` and mode `0660`; an existing file is left unchanged. `--admin-group <name>`, only with `--system`, uses another group; the name may start only with a lowercase letter or an underscore and may contain only lowercase letters, digits, underscores and hyphens.

The generated file is a template that lists every configuration key: keys with a fixed default are written out, and the remaining keys are comments with an example value or an empty placeholder. When the install writes the file it prints `generated the configuration file <path>`; when the file already exists it prints `kept the existing configuration file <path>` and, for a system-level install, `To delegate administration, run: chgrp <group> <path> && chmod 0660 <path>`. Running that command hands an existing installation to the group, whose members then manage the Broker without `sudo`; [Manage a Broker by file permission]({{< relref "/docs/guides/management-permissions" >}}) explains the group. Do not hand the file to the group when `broker.listen` uses a port below 1024; [Configuration keys]({{< relref "/docs/reference/configuration" >}}#types-and-constraints) gives the rule and the fix.

If a Broker that this service did not start is already running at that level, the install refuses and asks you to stop it first.

Before it writes or enables the service, the install checks the listen address it will use. When the address is in use, the install fails without registering a service, and the error gives the address, the configuration file and `broker.listen`. A reinstall recognizes its own running service, stops it and waits up to 20 seconds for it to release the address; when another process holds the address, an installed service is left alone. A service that a failed install wrote or enabled does not start at boot or login, and when it stopped an existing service, the error gives the command to restore it.

The service definition starts the Broker with `--allow-degraded`. When the Broker runs degraded after the service is registered, the install keeps the service installed and running, prints the reason and the fix command, and exits with code 3. `agent-peer service start` and `agent-peer service restart` exit with code 3 in the same situation.

On Linux the command uses systemd and runs `systemctl daemon-reload`, `systemctl enable` and `systemctl restart`. On macOS it writes and loads a launchd job. A `--system` install additionally requires the executable and every directory above it to be root-owned, not writable by group or others, and searchable by other accounts, and the executable to be executable by other accounts. An executable under the temporary directory is refused. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so choose a root-owned directory that group and others cannot write instead.

The service files and paths are listed under [Files and paths]({{< relref "docs/reference/files" >}}); [Guides]({{< relref "docs/guides" >}}) covers installing and running the service.

The install script in the release archive installs the service with this command.

## agent-peer service start, stop and restart

Starts, stops or restarts the installed service. The account that runs the command selects the level, and the command takes no `--system` option: root acts on the system-level service, and every other account on its own user-level service. Run it with `sudo` to act on the system-level service.

`stop` does not disable the service, so it starts again at the next login or boot. When that level has no installed service, the command exits with code 1 and says that no service is installed; an account that has only the system-level service installed is also told to run `sudo agent-peer service <command>`.

`start` and `restart` wait for the Broker to answer and check whether it runs degraded, as an install does; [agent-peer service install](#agent-peer-service-install) gives the exit codes.

## agent-peer service uninstall

Stops and removes only the installed service. The binary and the MCP registration stay, and so do configuration, identity and audit logs; [agent-peer uninstall](#agent-peer-uninstall) removes the whole installation. If no service is installed, the command succeeds and reports that nothing is installed, so it can be repeated.

- Without `--system`, removes the user-level service.
- With `--system`, removes the system-level service. It must run as root.

## agent-peer uninstall

Removes an installation: the service, the binary that the service starts, the MCP registration of the account that runs the command, and the runtime directory.

- Run as root to remove a system-level installation. Another account removes its own user-level installation; when the host also has a system-level installation, the command removes the user-level one and then says to use `sudo agent-peer uninstall` for the other.
- An account whose host has only the system-level installation is asked `Uninstall the system-wide installation (requires sudo)? [y/N]`. Answering `y` runs the command again with `sudo`; any other answer, or no terminal to ask, leaves the host unchanged, prints `sudo agent-peer uninstall` and exits with code 0.
- Unless `--purge` is given, the command asks `Also remove configuration, identity, audit and service logs? [y/N]` and keeps them on any answer other than `y` or when there is no terminal to ask. At the system level, answering `y` also removes the admin group.
- `--purge` skips that question and removes the data too; at the system level it also removes the admin group.
- The MCP registration of every other account stays; each of those accounts removes it with `claude mcp remove -s user peer`. At the system level the command removes the registration of the account that ran `sudo`; when it runs as root directly, it prints the command instead.

Before deleting a system-level binary, the command lists other accounts whose user-level Broker is running, then deletes the binary anyway.

A failure stops the command without undoing the steps that already succeeded; the error says which step failed, and running the command again continues from there. When no service is installed at either level but the MCP registration points at this binary, it uninstalls at the user level; with nothing installed, it reports that and exits with code 0.

Exit codes: 0 for a successful uninstall, for nothing to uninstall, and for declining the sudo uninstall of a system-level installation; 1 for a failure; 2 for a usage error.
