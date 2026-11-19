# agent-peer

agent-peer is a cross-host communication system for Claude Code sessions: a Broker runs on each host and the sessions on it join through the agent-peer MCP tools, so sessions on paired hosts can find each other and exchange text messages.

## Install

Extract the archive and run the install script from the extracted directory. The script installs the binary, installs the Broker as a service and starts it, and registers the MCP server with Claude Code for your account.

For a user-level Broker, run it as your own account:

```sh
sh ./install.sh
```

For a system-level Broker, run it with `sudo` from the account whose Claude Code should use the system Broker:

```sh
sudo sh ./install.sh --system
```

`--prefix <dir>` installs the binary in another directory, `--no-service` skips the service, and `--no-mcp` skips the MCP registration. Running the script again is safe: it replaces the binary, restarts the service, and does not register the MCP server twice.

## Get started

Open a new Claude Code session, because a session that was already open does not have the new MCP server. Start two sessions in the same project directory; both join the same default topic. Ask the model to list the agents with `list_agents` and send a message with `send_message`.

## Pair two hosts

At least one of the two hosts must be able to reach port 17443 on the other. On the host that can be reached, create a one-time pairing key and note the printed key and addresses. On the other host, connect with that key. Pairing is symmetric once it succeeds.

With a user-level Broker:

```sh
agent-peer pairing-key create
agent-peer peer connect <address> --key <key>
agent-peer peer list
```

With a system-level Broker, run the same commands with `sudo`:

```sh
sudo agent-peer pairing-key create
sudo agent-peer peer connect <address> --key <key>
sudo agent-peer peer list
```

Give the key and address to the other side through a channel outside this system. The address is written `address:port` or `hostname(address:port)`.

## Update

```sh
agent-peer update
```

For a system-level installation, use `sudo agent-peer update`.

## Uninstall

Stop and remove the service, remove the MCP registration, and delete the binary:

```sh
agent-peer service uninstall
claude mcp remove -s user peer
rm ~/.local/bin/agent-peer
```

For a system-level installation, use `sudo` for the service and delete the binary from its system prefix:

```sh
sudo agent-peer service uninstall --system
claude mcp remove -s user peer
sudo rm /usr/local/bin/agent-peer
```

Configuration, identity and audit logs are kept when the service is removed.

## Documentation

- Website: <https://zhanwei.wang/agent-peer/docs/>
- Full README and source: <https://github.com/wangzw/agent-peer>
