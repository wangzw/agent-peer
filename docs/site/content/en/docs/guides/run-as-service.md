---
title: Run the Broker as a service
linkTitle: Run the Broker as a service
weight: 4
---

A Broker started by a session runs in the background but has no run log, and no service manager starts or restarts it. Installing it as a service starts it at login or at boot, keeps it running, and brings it back after a machine restart.

## Stop a running Broker first

Installing a service does not stop a Broker that a session already started. That Broker holds the account lock, so the service exits on start with an error that an instance is already running, and the service manager retries it every 5 seconds. A running session whose peer tools are enabled also starts a Broker again about 250 ms after one disappears, so stopping the Broker is not enough while such a session is open.

Do these steps in this order:

1. Exit every session of this account that has the peer tools enabled, or disable `peer` in those sessions with `/mcp`.
2. Stop the Broker and confirm that it has stopped.
3. Start the service.
4. Open the sessions again.

A session-started Broker is not managed by systemd or launchd, and it exits on `SIGTERM`. The process name alone also matches `agent-peer mcp`, so find it by its command line:

```sh
pgrep -u "$USER" -f 'agent-peer broker'
```

Send `SIGTERM` to the listed process:

```sh
kill -TERM <pid>
```

The Broker exits with status 0 and removes its local socket. It has stopped when `pgrep` prints nothing and `agent-peer status` reports that it cannot connect. The account lock file stays, but its lock is released when the process exits, so the service can start afterwards. For a system-level install, the Broker that contends with the service takes root's account lock, not the lock of the account that runs `sudo`; run the same commands against root, with `sudo pgrep -u root -f 'agent-peer broker'` and `sudo kill -TERM <pid>`.

## Install a user-level service

Run the install command as the account that owns the sessions:

```sh
agent-peer service install
```

The command writes the unit file or plist and, on Linux, enables it; it does not start the Broker. The last line of its output is the command that starts it now. A user-level install must not run as root, because the unit belongs to one account. The install refuses a binary inside the temporary directory; this applies to both the user-level and the system-level form.

On Linux with systemd, the unit is `~/.config/systemd/user/agent-peer.service` (or under `$XDG_CONFIG_HOME`) and the start command is:

```sh
systemctl --user start agent-peer.service
```

On macOS, the LaunchAgent is `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist`, and the printed start command is `launchctl bootstrap gui/<uid> <absolute plist path>`, with your numeric user ID in place of `<uid>`.

The unit or plist runs `agent-peer broker` as that account. It starts when the account logs in and restarts the Broker if it exits. On macOS, starting a user Broker at login and restoring its links is still to be verified by hand; see [known limitations]({{< relref "/docs/limitations" >}}).

## Install a system-level service

A system-level install runs as root and makes the Broker start at boot. The binary must be in a directory that root owns and that group and others cannot write:

```sh
sudo install -o root -g root -m 0755 agent-peer /usr/local/bin/agent-peer
sudo /usr/local/bin/agent-peer service install --system
```

The install checks the binary and every directory above it.

On Linux the command writes `/etc/systemd/system/agent-peer.service` and enables it. On macOS it writes `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`. A system install also creates `/etc/agent-peer/config.yaml` if it does not exist, owned by root with mode 0600.

The start command on Linux is:

```sh
sudo systemctl start agent-peer.service
```

On macOS the printed command is `launchctl bootstrap system /Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`.

System-level installation is verified end to end on Linux only. On macOS, install the launch daemon and check it by hand.

## Check the service

Run `agent-peer status` in a terminal. The output has an `Agents` table and a `Peers` table, or `Agents: none` and `Peers: none`. The service manager shows the Broker too: `systemctl --user status agent-peer.service`, `systemctl status agent-peer.service`, or `launchctl print` with the label `io.github.wangzw.agent-peer`.

Keep one Broker per account. Starting a second Broker under the same account fails with an error that an instance is already running, whichever address it would use.
