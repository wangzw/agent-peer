---
title: Pairing
weight: 4
---

Pairing is how two Brokers establish trust. The accepting side creates a one-time pairing key, and the initiating side uses it to connect; after a successful handshake each Broker saves the other's identity and recognizes it by its public-key fingerprint. No certificate authority is involved, and the trust is built one pair at a time.

## Pairing keys

The accepting side creates a one-time pairing key. It is valid for 10 minutes and stops working after one successful pairing. The user passes the key, together with the Broker's address, to the other user through a channel outside agent-peer; the address is not secret and can travel separately.

A Broker can also use a pre-provisioned static key written into its configuration file by the user or administrator. The key can be used for any number of pairings and does not expire. agent-peer never generates it.

The initiating side calls `connect_broker` with the other Broker's address and the key. The address is written `host:port`, or `hostname(host:port)` in the form shown by `create_pairing_key`. A wrong, expired or already used key makes the pairing fail, and neither side saves the other's identity.

## After pairing

The two Brokers are now peers. If the new peer's alias matches this Broker's own alias or is already used by another peer, the initiating side must choose a local alias before pairing completes.

Pairing survives a restart of either Broker. Re-establishing a link after a network failure needs no pairing key.

See [Broker]({{< relref "/docs/concepts/broker" >}}) for the alias and fingerprint, [Links]({{< relref "/docs/concepts/links" >}}) for the connection that pairing allows, [Pair two hosts]({{< relref "/docs/guides/pair-hosts" >}}) for the pairing steps, and [MCP tools]({{< relref "/docs/reference/mcp-tools" >}}) and [Configuration keys]({{< relref "/docs/reference/configuration" >}}) for `connect_broker` and the static key.
