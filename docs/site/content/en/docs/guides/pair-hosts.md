---
title: Pair two hosts
linkTitle: Pair two hosts
weight: 1
---

Pairing makes two Brokers trust each other by their fingerprints. It is the only way to connect two hosts, and it starts with a key that one side creates and both sides share.

## Before you start

- Both Brokers are running and are not paired yet.
- On the host that will accept the pairing, the Broker listens on its default address: port 17443 on every local address. The other host can reach one of the accepting Broker's advertised addresses; that other host may be behind NAT.
- The two users have a trusted channel outside agent-peer for the key.
- The account running each session or command has write permission on its Broker's configuration file. A user Broker belongs to that account, so it does. On a system Broker an ordinary account has no write permission by default, and on that side both creating the key and connecting need write permission, so the administrator runs `sudo agent-peer pairing-key create` and `sudo agent-peer peer connect <address> --key <key>`, or grants write access as [Manage a Broker by file permission]({{< relref "management-permissions.md" >}}) describes.

## Create the pairing key

On the accepting host, ask the model to call `create_pairing_key` and approve the call, or run:

```sh
agent-peer pairing-key create
```

Either way the result gives:

- a one-time key that expires after 10 minutes and stops working after one successful pairing;
- the Broker's advertised addresses, each shown with its host name.

Pass the key and one address to the other user through a channel outside agent-peer. The address is not secret and may travel separately from the key.

## Connect from the other host

On the other host, ask the model to call `connect_broker` with the `address` and the `key`, or run:

```sh
agent-peer peer connect <address> --key <key>
```

The address is written `host:port`, or `hostname(host:port)` exactly as the other side showed it; the host name in that form is for the user and is not used for the connection.

Either way the result gives the accepting Broker's alias and fingerprint.

## Check the link

Ask the model to call `list_brokers` on both hosts, or run `agent-peer peer list`. Each side lists the other as a peer with status `connected`. For a peer, the result also gives this Broker's role: the side that called `connect_broker` or ran `agent-peer peer connect` is the `dialer`, and the side that created the key is the `acceptor`.

The dialer re-establishes the link after a network interruption without any user action. The acceptor never dials, so the two-way link only needs one reachable direction.

## Special cases

- The key expired or was used. A one-time key is valid for 10 minutes and for one pairing. Using it later returns an error that the key is invalid or expired, and neither side stores the other's identity. Create a new key and start again.
- The alias is already taken. If the accepting Broker's alias matches this Broker's own alias or another peer's alias, `connect_broker` asks for a local alias. Call it again with `local_alias` set to a name of up to 24 characters, or run `agent-peer peer connect <address> --key <key> --alias <alias>` with the same limit; the alias applies only on this Broker.
- The shown address does not work. The addresses returned with the key may not be reachable from the other side, for example when the accepting host is behind port forwarding. Pass an address that the other side can reach and use it in `connect_broker` or `agent-peer peer connect`.
- The network drops. Once the Brokers are paired, the dialer reconnects without a key. Wait a moment and check again with `list_brokers` or `agent-peer peer list`; the peer returns to `connected`.
- The deployment is repeated. Instead of a one-time key, the accepting Broker can have a pre-provisioned static key in its configuration file, under `pairing.static_key`. The value is 16 to 128 characters, does not expire, and works for any number of pairings. The tools never generate it, set it or return it; write it in the file before the deployment and use it as the `key` in `connect_broker` or `agent-peer peer connect <address> --key <key>`.
