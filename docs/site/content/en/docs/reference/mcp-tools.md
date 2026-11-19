---
title: MCP tools
weight: 1
---

A Claude Code session reaches agent-peer through an MCP server named `peer`. Register it once for the account with `claude mcp add --scope user peer -- agent-peer mcp`. The model then sees the tools as `mcp__peer__<tool>`, and permission rules use that name, for example `mcp__peer__send_message`.

## Tools

| Tool | What it does | Parameters | Config-file permission |
|---|---|---|---|
| `list_agents` | Lists the agents visible to this session. | `topic` (string, optional): only agents that share this topic. `broker` (string, optional): only agents on the Broker with this alias. `name` (string, optional): only agents with this name. All filters must match. | None |
| `send_message` | Sends plain text to an agent address. | `to` (string, required): the address, copied verbatim from the sender of a message. `message` (string, required): plain text. | None |
| `list_topics` | Lists the topics this session has joined and the public topics this Broker knows. | None. | None |
| `join_topic` | Joins a topic. | `name` (string, required). `public` (boolean, optional): declare the topic public; this requires the Broker to be public too. `description` (string, optional): a short description shown with a public topic. | None |
| `leave_topic` | Leaves a topic this session has joined. | `name` (string, required). | None |
| `list_brokers` | Lists the Brokers this Broker knows, with peer status, addresses and fingerprints. | None. | None |
| `connect_broker` | Pairs with another Broker. | `address` (string, required): written `host:port` or `hostname(host:port)`. `key` (string, required): the pairing key for the other side. `local_alias` (string, optional): the alias to use when the other Broker's own alias is taken. | Write |
| `disconnect_broker` | Removes the pairing with a peer Broker. | `broker` (string, required): the peer's alias or its 64-hex-digit fingerprint. | Write |
| `create_pairing_key` | Creates a one-time pairing key and reports this Broker's addresses. | None. | Write |
| `get_config` | Reads this Broker's configuration. | `key` (string, optional): one key such as `broker.alias`; omit it for every key. | Read |
| `set_config` | Changes one configuration key. | `key` (string, required). `value` (string, required): the new value as text, such as `10m`, `true` or `[a, b]`. | Write |

## Results

### Agents

`list_agents` returns `self_address` and `agents`. Each agent has an `address`, the `shared_topics` it has in common with this session, a `status`, and, for ten minutes after a rename, a `previous_name`. The status is `idle`, `busy`, `waiting` or `shell`.

### Messages

`send_message` returns `result` and `to`. The result is `delivered to the recipient's session (not necessarily read)`: success means the Broker wrote the message into the recipient session's inbox socket, not that the agent read it. If delivery definitively failed, the call returns an error beginning `Not sent`. If the request went out and no definite answer came back, it returns an error beginning `Delivery unknown`, warning that a resend may deliver a second copy.

### Topics

`list_topics` returns `joined_topics` and `public_topics`, each public topic with a `name` and an optional `description`. `join_topic` and `leave_topic` return the updated list in `topics`.

### Brokers

`list_brokers` returns `brokers`. Each entry has an `alias`, a lowercase hexadecimal `fingerprint`, `advertised_addresses` (each with a `kind` and an `addr`; the only kind is `direct`), `is_peer`, a `status` of `connected`, `connectable` or `not_directly_reachable`, and a `status_detail`. A peer also has `role`, either `dialer` or `acceptor` for this Broker, and `link_state`: `dialing`, `waiting`, `handshaking`, `established`, `backoff` or `rejected`.

`connect_broker` returns the peer's `alias` and `fingerprint`, a `shown_host`, and any `warnings`. `disconnect_broker` returns `result`. `create_pairing_key` returns `key`, `expires_at` in RFC 3339 UTC, `shown_addresses` and an optional `note`. The addresses are shown for the user to pass on; they are not part of the key.

### Configuration

`get_config` returns `entries`, each with a `key` and a `value`. For a secret key the value is only `(set)` or `(not set)`. `set_config` returns the key with its `old_value` and `new_value`.

## Config-file permission

The permission column in the table names the level of access the calling account has to the Broker's configuration file. The account is taken from the process credentials the operating system reports for the local socket, not from anything the call says.

- A user Broker's configuration file belongs to the account, so every call passes.
- A system Broker uses the file to decide: read access allows `get_config`; write access allows `connect_broker`, `disconnect_broker`, `create_pairing_key` and `set_config`. An administrator delegates these operations by granting a group access to the file.
- The messaging and topic tools, and `list_brokers`, need no file permission.

`list_agents`, `send_message`, `list_topics`, `join_topic` and `leave_topic` act for the session and need a registered session. The discovery, pairing and configuration tools also work while registration is still pending.

## Limits on set_config

- `pairing.static_key` is a secret. It can be set only by editing the configuration file; `set_config` refuses it.
- `broker.listen` cannot be changed while the Broker runs with `--listen`, because the command line fixes the listen address for that run.
- The value must match the key's type. A rejected value leaves the configuration unchanged. [Configuration keys]({{< relref "docs/reference/configuration" >}}) lists every key and its constraints.

The concepts behind these tools are in [Concepts]({{< relref "docs/concepts" >}}); step-by-step use is in [Guides]({{< relref "docs/guides" >}}).
