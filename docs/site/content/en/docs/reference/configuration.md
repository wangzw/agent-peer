---
title: Configuration keys
weight: 3
---

Both deployment forms read one YAML file: `~/.agent-peer/config.yaml` for a user Broker and `/etc/agent-peer/config.yaml` for a system Broker. Every key is optional; an absent key takes its default. The Broker watches the file, so a valid change takes effect while it runs, whether the change is written by hand, by `set_config`, or by `agent-peer config set`. The two keepalive keys are the exception: they are read when a listener or a link is created, so a change applies only to listeners or links established afterwards.

## Keys

| Key | Type | Default | Meaning | `set_config`, `agent-peer config set` |
|---|---|---|---|---|
| `broker.alias` | string | derived | The alias this Broker reports. Derived from the hostname and, for a user Broker, the account when not set. 1 to 24 characters from letters, digits, `.`, `-` and `_`. The default is described below the table. | Yes |
| `broker.public` | boolean | `false` | Whether this Broker publishes a broker card and is visible to the whole connected network. | Yes |
| `broker.listen` | string | `:17443` | The address and port to bind, written `host:port`, `:port` or `[v6]:port`. An empty host means every local address; an empty string means do not listen. | Yes, except under `--listen` |
| `broker.advertise` | string | empty | Replaces the automatically detected advertised address. Accepted forms are empty, `host`, `host:port` and `:port`. `host` takes the listen port; `:port` takes the addresses of the default-route interfaces and requires listening on every address. A non-empty value requires a listen address. | Yes |
| `broker.advertise_extra` | string list | empty | Extra advertised addresses to add. Each item is `host` or `host:port`; an item without a port uses the port in `broker.advertise` when it has one, otherwise the listen port. Requires a listen address. | Yes |
| `broker.address_check_interval` | duration | `1m` | How often the Broker re-reads the host's local addresses. | Yes |
| `broker.local_socket_group` | string | empty | The group that owns the system Broker's local socket. Empty keeps the runtime directory's group. The group must exist in the account database; the value must not contain whitespace, `:`, or `/`. Ignored by a user Broker. | Yes |
| `broker.local_socket_mode` | string | `0666` | The file mode of the system Broker's local socket, written as 3 or 4 octal digits. A user Broker always uses `0600`. | Yes |
| `pairing.static_key` | secret string | empty | A pre-provisioned static pairing key, 16 to 128 characters. The key is active as soon as it is set. | No, file only |
| `pairing.max_failures` | integer | `5` | Failed verifications allowed before a one-time pairing key is discarded. | Yes |
| `pairing.max_attempts_per_minute` | integer | `10` | Pairing requests per minute that match no pairing key. Requests matched by the static key do not count. | Yes |
| `topics.auto_join` | boolean | `true` | Whether sessions join their default topics automatically. | Yes |
| `directory.reconcile_interval` | duration | `10m` | How often peers reconcile their directories. | Yes |
| `directory.max_records` | integer | `1000` | Maximum number of records in the directory. | Yes |
| `directory.max_records_per_link` | integer | `250` | Maximum number of records accepted over one link. | Yes |
| `directory.max_topics_per_publisher` | integer | `16` | Maximum number of public-topic declarations and records of unknown kind from one publisher. Broker cards and revocations do not count. | Yes |
| `send.link_retries` | integer | `5` | Retries when a link is not established. | Yes |
| `send.link_backoff` | duration | `500ms` | Wait before the first retry; it doubles each time, up to 8 seconds per wait. | Yes |
| `send.request_timeout` | duration | `20s` | Total time budget for one send, including retries and waits. | Yes |
| `send.inbox_retries` | integer | `2` | Retries when the inbox write definitely did not happen. | Yes |
| `send.inbox_backoff` | duration | `200ms` | Wait between inbox write retries. | Yes |
| `send.inbox_timeout` | duration | `5s` | Deadline for one inbox write. | Yes |
| `send.dedupe_window` | duration | `5m` | How long entries stay in the duplicate-message table. | Yes |
| `link.dial_timeout` | duration | `5s` | Connection deadline for each address while dialing. | Yes |
| `link.reconnect_min` | duration | `1s` | Initial backoff when re-establishing a link. | Yes |
| `link.reconnect_max` | duration | `1m` | Maximum backoff when re-establishing a link. | Yes |
| `link.keepalive_interval` | duration | `15s` | Keepalive probe interval. gRPC raises an interval below 10 seconds to 10 seconds. | Yes |
| `link.keepalive_timeout` | duration | `10s` | Deadline for a keepalive reply. | Yes |
| `log.level` | string | `info` | Run log level: `debug`, `info`, `warn` or `error`. | Yes |

When `broker.alias` is not set, a system Broker uses the first segment of the system hostname, the part before the first `.`. A user Broker uses that segment, a `.`, and the name of the account that runs it, for example `workstation` and `workstation.alice`. The alias does not depend on the listen address, the listen port, or the advertised addresses. A character outside the alias character set is replaced with `-`. When the account name cannot be found, the decimal uid takes its place, for example `workstation.1000`. When the hostname cannot be found, the hostname part is `broker`. An alias longer than 24 characters is shortened first in its hostname part, which keeps at least 1 character, and, if it is still too long, then in its account part. After an upgrade from 0.1.0, a Broker without this key usually reports a different alias; a Broker that did not listen and whose hostname is already a single segment keeps the same alias. Set the key here to keep the old one, such as `workstation-17443`.

## Types and constraints

- A duration is a string in Go duration syntax, for example `10m`, `500ms`, `1s` or `1h30m`. Durations must be greater than zero.
- Integers must be greater than zero, except `send.link_retries` and `send.inbox_retries`, which accept `0`.
- `link.reconnect_max` must be greater than or equal to `link.reconnect_min`, and `link.keepalive_timeout` must be less than `link.keepalive_interval`. Changing one key of either pair is checked against the other.
- `broker.listen` is written `host:port`, `:port` or `[v6]:port`; the port is 1 to 65535. Setting a different value probes the address, and an address that cannot be bound is rejected.
- `broker.advertise` and `broker.advertise_extra` require a listen address, and a port-only `broker.advertise` also requires listening on every address.
- The configured advertised addresses, including the implicit one for a specific listen address, may hold at most 8 entries. Addresses taken from the default-route interfaces are dropped from the end when the list would exceed 8; configured entries are all kept.
- `broker.local_socket_group` must name a group that exists; startup fails when it does not.
- `broker.local_socket_mode` accepts 3 or 4 octal digits, up to `0777`.
- `pairing.static_key` accepts 16 to 128 characters; an empty value means no static key.

A value that fails a check is rejected, and the configuration remains as it was.

## Changing the configuration

`set_config` takes the new value as text and returns the old and new values. `agent-peer config set` does the same from the terminal and follows the same rules. Neither can change `pairing.static_key`, and neither can change `broker.listen` while the Broker runs with `--listen`. When the file is edited by hand, the Broker applies the valid changes; a key whose value is invalid keeps its old value and the Broker writes an error to its log.

The file locations and their permissions are listed under [Files and paths]({{< relref "docs/reference/files" >}}). Which topics sessions join by default is described under [Project configuration file]({{< relref "docs/reference/project-config" >}}).
