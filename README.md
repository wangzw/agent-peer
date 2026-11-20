# agent-peer

agent-peer lets Claude Code sessions running on different hosts discover each other and exchange text messages.

Claude Code's built-in cross-session messaging covers sessions on one host. agent-peer runs a Broker on each host, and a session joins the Broker on its own host through MCP tools. The users of two hosts pair their Brokers with a pairing key; after that, sessions on both sides that share a topic can see each other and send messages. An idle session picks up a message and starts working on it without user input, and the session's own inbound controls and permission prompts still apply.

Documentation: <https://zhanwei.wang/agent-peer/> (English) and <https://zhanwei.wang/agent-peer/zh/> (Chinese).

## Install

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh
```

The command installs the binary, installs and starts the Broker as a service, and registers the MCP server for your account. It asks whether to install system-wide, serving every account on the host, or for your account only. Pass `--system` or `--user` after `sh -s --` to skip the question:

```sh
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --system
```

Reopen any Claude Code session that is already open, and check the registration with `claude mcp get peer`.

[Install agent-peer](https://zhanwei.wang/agent-peer/docs/guides/install/) covers a particular version, the release archive, the install script's options, manual installation and building from source.

## Try it

On one host, start two sessions in the same project directory. Both join the same default topic, taken from the project configuration file `.agent-peer.yaml` or, when there is none, from the workspace's git origin address. Ask the model to list the agents with `list_agents`, then send a message with `send_message`.

Between two hosts, at least one must be able to reach port 17443 on the other. On the host that can be reached, ask the model to call `create_pairing_key`, and pass the key and the displayed address to the other host through a channel outside agent-peer. On the other host, ask the model to call `connect_broker` with the address and the key. Sessions on both hosts that share a topic can now see each other.

[Getting Started](https://zhanwei.wang/agent-peer/docs/getting-started/) walks through both.

## Platform

agent-peer runs on macOS and Linux, on amd64 and arm64. Claude Code v2.1.224 or later is required.

## Known limitations

See [Known limitations](https://zhanwei.wang/agent-peer/docs/limitations/) for what agent-peer does not do.

## Development

The build gates live in the Makefile. `make lint`, `make test` and `make docs-check` run on any supported platform. The end-to-end suite (`make e2e`) needs Linux and a working Docker daemon; pass the job count explicitly, for example `make e2e JOBS=4`. The requirements, design, architecture decision records and glossary are under [docs/](docs/), written in Chinese; start with the [system overview](docs/prd/overview.md) and the [architecture](docs/design/architecture.md).

## License

agent-peer is released under the [Apache License 2.0](LICENSE).
