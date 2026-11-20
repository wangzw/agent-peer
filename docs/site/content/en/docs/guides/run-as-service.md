---
title: Run the Broker as a service
linkTitle: Run the Broker as a service
weight: 4
---

A Claude Code session connects to a Broker but never starts one. You start a Broker either by installing it as a service or by running it in a terminal. A service starts the Broker right away, starts it at login or at boot, keeps it running, and brings it back after a machine restart.

The install script in the release archive and the online install command already install and start the service by default, so you mainly need this page for a manual installation, for an install with `--no-service`, or for a later reinstall of the service alone. The sections on stopping a Broker you started in a terminal and on uninstalling a service apply to every installation.

## Stop a Broker you started in a terminal

If you have run `agent-peer broker` in a terminal for this account, stop it there before installing the service. The install refuses while a Broker it did not start is answering, because that Broker holds the account lock.

## Install a user-level service

If the host already has a system Broker you can use, you do not need a user-level service.

Run the install command as the account that owns the sessions:

```sh
agent-peer service install
```

The command writes the unit file or plist, enables the service, and starts the Broker immediately. It waits up to 20 seconds for the Broker to answer; if it does not answer, the install fails and reports where the service logs go. Reinstalling replaces the running Broker with the new binary. A user-level install must not run as root, because the unit belongs to one account. The install refuses a binary inside the temporary directory; this applies to both the user-level and the system-level form.

When `~/.agent-peer/config.yaml` does not exist, the install generates it as a template that lists every configuration key: keys with a fixed default are written out, and the remaining keys are comments with an example value or an empty placeholder. The file belongs to the account that runs the install and has mode `0600`; an existing file is left unchanged. The install reports either `generated the configuration file <path>` or `kept the existing configuration file <path>`. [Configuration keys]({{< relref "docs/reference/configuration" >}}) describes the generated file.

On Linux with systemd, the unit is `~/.config/systemd/user/agent-peer.service` (or under `$XDG_CONFIG_HOME`). On macOS, the LaunchAgent is `~/Library/LaunchAgents/io.github.wangzw.agent-peer.plist`, and the install must run in a graphical login session; a host without one uses the system-level service instead.

The unit or plist runs `agent-peer broker` as that account. It starts when the account logs in and restarts the Broker if it exits.

## Install a system-level service

A system-level install runs as root and makes the Broker start at boot. From an extracted release archive, `sudo sh ./install.sh --system` installs the binary and the service in one step, and so does the online system-level command in [Getting Started]({{< relref "/docs/getting-started" >}}). The steps below do the two parts by hand.

The binary must be in a directory that root owns and that group and others cannot write, such as `/usr/local/bin`. The service starts the binary as root, so an ordinary account that can modify the binary or one of its parent directories could take over the service:

```sh
VERSION=0.1.0
sudo install -o root -g 0 -m 0755 agent-peer-${VERSION}/agent-peer /usr/local/bin/agent-peer
sudo /usr/local/bin/agent-peer service install --system
```

On an Intel Mac with Homebrew, `/usr/local/bin` belongs to the ordinary user, so the installation is refused; choose a root-owned directory that group and others cannot write instead.

The install checks the binary and every directory above it, then starts the Broker immediately.

On Linux the command writes `/etc/systemd/system/agent-peer.service`, enables it and starts it. On macOS it writes and loads `/Library/LaunchDaemons/io.github.wangzw.agent-peer.plist`. A system install also generates the configuration file at `/etc/agent-peer/config.yaml` from the system-level template if it does not exist, owned by root with mode `0600`; an existing file is left unchanged.

## Uninstall a service

Run `agent-peer service uninstall` as the same account to remove a user-level service. For a system-level service, run `sudo /usr/local/bin/agent-peer service uninstall --system`. The command stops the Broker and removes the service definition; configuration, identity and audit logs are kept. If no service is installed, it succeeds and reports that nothing is installed, so it can be run again safely.

## Check the service

Run `agent-peer status` in a terminal. The output has an `Agents` table and a `Peers` table, or `Agents: none` and `Peers: none`. The service manager shows the Broker too: `systemctl --user status agent-peer.service`, `systemctl status agent-peer.service`, or `launchctl print` with the label `io.github.wangzw.agent-peer`.

Keep one Broker per account. Starting a second Broker under the same account fails with an error that an instance is already running, whichever address it would use.
