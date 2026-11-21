# peers

peers is a cross-host communication system for Claude Code sessions: a Broker runs on each host and the sessions on it join through the peers MCP tools, so sessions on paired hosts can find each other and exchange text messages.

## Install

### One-command installation

To install the latest release with one command:

```sh
curl -fsSL https://github.com/wangzw/peers/releases/latest/download/install.sh | sh
```

The script detects the platform and architecture, downloads the matching archive, verifies it against the release's `checksums.txt`, and runs the install script from the archive. That script asks whether to install for every account on the host, or for your account only; pass `--system` or `--user` after `--` to skip the question. A system-level install reruns itself through sudo from the account whose Claude Code should use the system Broker:

```sh
curl -fsSL https://github.com/wangzw/peers/releases/latest/download/install.sh | sh -s -- --system
```

`--version <version>` installs a published version instead of the latest and must come before any other option. A version that does not exist makes the command fail with `version <version> not found` and leaves the host unchanged.

Piping the script to a shell carries the same trust as downloading the archive and running it by hand: the releases are not signed. To inspect the script first, download it, read it, and run it from the directory that holds it.

### Install from the archive

Extract the archive and run the install script from the extracted directory. The script installs the binary, installs the Broker as a service and starts it, and registers the MCP server with Claude Code for your account.

Run it without a level option and it asks whether to install for every account on the host or for your account only. Pass `--system` or `--user` to skip the question; a system-level install reruns itself through sudo from the account whose Claude Code should use the system Broker:

```sh
sh ./install.sh
```

`--prefix <dir>` installs the binary in another directory, `--no-service` skips the service, and `--no-mcp` skips the MCP registration. Running the script again is safe: it replaces the binary, restarts the service, and does not register the MCP server twice.

## Get started

Open a new Claude Code session, because a session that was already open does not have the new MCP server. Start two sessions in the same project directory; both join the same default topic. Ask the model to list the agents with `list_agents` and send a message with `send_message`.

## Pair two hosts

At least one of the two hosts must be able to reach port 17443 on the other. On the host that can be reached, create a one-time pairing key and note the printed key and addresses. On the other host, connect with that key. Pairing is symmetric once it succeeds.

With a user-level Broker:

```sh
peers pairing-key create
peers peer connect <address> --key <key>
peers peer list
```

With a system-level Broker, run the same commands with `sudo`:

```sh
sudo peers pairing-key create
sudo peers peer connect <address> --key <key>
sudo peers peer list
```

An administrator account that runs no user-level Broker can run them without `sudo`; the command then selects the system-level Broker. An account that also runs a user-level Broker uses `sudo` to reach the system-level Broker.

Give the key and address to the other side through a channel outside this system. The address is written `address:port` or `hostname(address:port)`.

## Administer a system-level Broker without root

A system-level install creates the `peers` group and gives the configuration file to it. The install script asks whether to add the installing account to the group, or pass `--join-admin-group` to add it without asking; log in again or run `newgrp peers` afterwards. Members of the group can create pairing keys, change the configuration and pair with another Broker without `sudo` when their account runs no user-level Broker; an account that also runs a user-level Broker uses `sudo` so the command reaches the system-level Broker. The group can do what manual editing of the configuration file can do, and nothing more. Installing and uninstalling the service still need root; updating it does not, because the Broker installs the release itself.

For an existing installation, give the file to the group with `chgrp peers /etc/peers/config.yaml` and `chmod 0660 /etc/peers/config.yaml` as root. Skip giving the file to the group when `broker.listen` uses a port below 1024, because such a Broker refuses to start while another account can write the file.

## Update

```sh
peers update
```

The command asks for confirmation, then downloads, checks and installs the latest official release. When it replaces a user-level binary locally, it leaves the service alone, because the service may have been stopped on purpose, and prints that the new binary takes effect the next time the Broker starts. It checks write permission on the binary's directory first and suggests `sudo` when it is missing.

For a system-level installation, run `peers update` as an account that administers the Broker, without `sudo`, when that account runs no user-level Broker; the command selects the system-level Broker. An account that also runs a user-level Broker uses `sudo peers update`, so root selects the system-level Broker. The Broker replaces its own binary and the service manager restarts it. The account needs write permission on the Broker's configuration file.

A session can also start the upgrade with the `update_broker` tool. Claude Code asks for confirmation before the call runs, and that confirmation is the last manual check before the upgrade.

When the update runs through the Broker, the Broker restarts and open sessions reconnect to it automatically. The MCP server inside an already open session keeps running the old binary until you reconnect it with `/mcp` or restart the session. A local replacement does not start or restart anything; the new binary takes effect the next time the Broker starts.

## Uninstall

Remove the service, the binary, the runtime directory and this account's MCP registration:

```sh
peers uninstall
```

Add `--purge` to remove the configuration, identity, audit log and service logs as well; without it, uninstall asks before deleting them.

A system-level installation is removed as root, or by answering the question when another account runs the command:

```sh
sudo peers uninstall
```

## Documentation

- Website: <https://zhanwei.wang/peers/docs/>
- Full README and source: <https://github.com/wangzw/peers>
