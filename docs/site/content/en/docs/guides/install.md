---
title: Install agent-peer
linkTitle: Install agent-peer
weight: 1
---

This guide covers installing a particular version, installing from the release archive, the install script's options, manual installation and building from source. [Getting Started]({{< relref "/docs/getting-started" >}}) gives the one-command installation; to keep an installation up to date with `agent-peer update`, see [Update agent-peer]({{< relref "update.md" >}}).

To install a particular version, set it first and pass `--version`:

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sh -s -- --version "$VERSION"
```

For a system-level installation of a particular version:

```sh
VERSION=0.2.0
curl -fsSL https://github.com/wangzw/agent-peer/releases/latest/download/install.sh | sudo sh -s -- --version "$VERSION" --system
```

`--version` must come before every other argument. It names a published version to install; when that version has no release archive to install, the script stops with `version <version> not found`. Every other argument is passed unchanged to the archive's `install.sh`, so the options in the next section work here as well.

The command needs macOS or Linux on amd64 or arm64; `curl` to fetch the script, while the script itself uses `curl` or `wget` to download the archive; `tar`; `sha256sum` or `shasum`; and access to github.com. The system-level form needs `sudo`.

Unless `--version` names a release, the script resolves the latest stable release; it downloads this platform's archive and the same release's `checksums.txt` into a temporary directory, verifies the archive, extracts it and runs the `install.sh` it contains. Every download the script makes goes over HTTPS; with either `curl` or `wget`, it refuses a redirect to an http address. If a download fails or the checksum does not match, it stops and leaves the host unchanged, and the temporary directory is removed when the script exits or is interrupted. An interrupted download of the script itself runs nothing, because the work starts on its last line.

Piping a script into a shell trusts it to the same degree as downloading the archive by hand from GitHub Releases. Releases are not signed, and `checksums.txt` comes from the same release as the archive, so a matching checksum catches a damaged download but does not prove that the release is trustworthy. To inspect the script first, download it, read it, and run it from the directory that holds it:

```sh
curl -fsSLO https://github.com/wangzw/agent-peer/releases/latest/download/install.sh
sh install.sh
```

The script does the same work as the `install.sh` in the archive, described in the next section.

## Install from the release archive

agent-peer is distributed as a `tar.gz` archive on [GitHub Releases](https://github.com/wangzw/agent-peer/releases/latest). Download the archive for your platform and architecture together with `checksums.txt` from the same release:

```sh
VERSION=0.2.0
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/agent-peer_${VERSION}_<os>_<arch>.tar.gz
curl -LO https://github.com/wangzw/agent-peer/releases/download/v${VERSION}/checksums.txt
```

Replace `<os>_<arch>` with `darwin_amd64`, `darwin_arm64`, `linux_amd64` or `linux_arm64`. The checksum and extraction examples below use the arm64 archive; replace `arm64` with `amd64` on an amd64 host.

Check the archive against `checksums.txt` before using it. On Linux:

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_linux_arm64.tar.gz checksums.txt | sha256sum -c -
```

On macOS, use `shasum` instead:

```sh
VERSION=0.2.0
grep agent-peer_${VERSION}_darwin_arm64.tar.gz checksums.txt | shasum -a 256 -c -
```

Create a directory and extract the archive into it: the archive has no top-level directory, so without one its files, including `LICENSE`, `README.md` and `third_party/`, land directly in the current directory:

```sh
VERSION=0.2.0
mkdir agent-peer-${VERSION}
tar -xzf agent-peer_${VERSION}_darwin_arm64.tar.gz -C agent-peer-${VERSION}
```

Replace `darwin` with `linux` on Linux.

Enter the extracted directory and run the script:

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sh ./install.sh
```

For a system-level installation, run the same script as root, usually through `sudo`:

```sh
VERSION=0.2.0
cd agent-peer-${VERSION}
sudo sh ./install.sh --system
```

The user-level form installs the binary into `~/.local/bin`, installs and starts the user-level Broker service, and registers the MCP server for your account. The system-level form installs the binary into `/usr/local/bin`, installs and starts the system-level Broker service, and registers the MCP server for the account that ran `sudo`; it also creates the `agent-peer` group and asks whether to add that account to it. [The admin group]({{< relref "management-permissions.md" >}}#the-admin-group) covers the group, the question and its effect.

The script accepts these options:

| Option | Effect |
|---|---|
| `--prefix <dir>` | Install the binary into `<dir>` instead of the default, `~/.local/bin` for a user-level installation or `/usr/local/bin` for a system-level one. |
| `--no-service` | Do not install the Broker service. [Start the Broker](#start-the-broker) yourself. |
| `--no-mcp` | Do not register the MCP server. |
| `--admin-group <name>` | Manage the system Broker through the group `<name>` instead of the default `agent-peer` group; the name may start only with a lowercase letter or an underscore and may contain only lowercase letters, digits, underscores and hyphens. Valid only with `--system`. |
| `--join-admin-group` | Add the account that ran `sudo` to the admin group without asking. Valid only with `--system`. |
| `--no-admin-group` | Do not ask whether to add the account that ran `sudo` to the admin group. Valid only with `--system`. |

Giving both `--join-admin-group` and `--no-admin-group` is a usage error.

The script is safe to run again: it replaces the binary, restarts the service with it, and does not register the MCP server twice. If the server is already registered with a different command, the script keeps the existing registration; to change it, run `claude mcp remove -s user peer` and run the script again. It never accesses the network; everything it installs comes from the extracted archive.

With `--no-mcp`, the script skips the registration, and you run the registration command in the manual section yourself. When it cannot find `claude`, or runs as root without `sudo` and has no account to register for, it prints the `claude mcp add` command to run.

If the configuration file for the level does not exist, `service install` creates it with every configuration key listed: `~/.agent-peer/config.yaml` for a user-level service, and `/etc/agent-peer/config.yaml` for a system-level one. [Configuration keys]({{< relref "/docs/reference/configuration" >}}) describes the file.

On macOS, the script clears the quarantine attribute from the binary itself, so a browser download needs no extra step. If the user-level install directory is not on `PATH`, the script says so; add it to `PATH` to run `agent-peer` by name. A user-level service on macOS needs a graphical login session; on a host without one, use `--system` or `--no-service`.

A system-level installation requires the binary's directory and every directory above it to be owned by root and not writable by group or others. On an Intel Mac with Homebrew, `/usr/local/bin` belongs to your account, so the installation is refused; create a root-owned directory with `sudo mkdir -p <dir>` and pass it with `--prefix` instead.

## Install manually

Download, verify and extract the archive as described in the previous section, then install the binary by hand. The commands below run from the directory that contains the extracted `agent-peer-${VERSION}` directory, so run `cd ..` first if you entered it in the previous section.

On macOS, a browser download carries the quarantine attribute, and macOS blocks the binary until it is removed. Clear it from the extracted binary if a browser downloaded the archive:

```sh
VERSION=0.2.0
xattr -d com.apple.quarantine agent-peer-${VERSION}/agent-peer
```

A command-line download with `curl` or `wget` does not set the attribute.

If you install a system-level service, use the `sudo install` step in [Run the Broker as a service]({{< relref "run-as-service.md" >}}) instead of the `mv` below.

Move the binary into a directory on your `PATH`:

```sh
VERSION=0.2.0
mkdir -p ~/.local/bin
mv agent-peer-${VERSION}/agent-peer ~/.local/bin/
agent-peer --version
```

The printed version matches the version you downloaded.

### Register the MCP server

Register the server once for your account:

```sh
claude mcp add --scope user peer -- agent-peer mcp
```

The server name must be `peer`. Claude Code shows the tools as `mcp__peer__<tool>`, and you write permission rules with that name. If `agent-peer` is not on your `PATH`, use its absolute path.

Check the registration:

```sh
claude mcp get peer
```

The output shows `Scope: User config`. A session started afterwards lists `peer` in `/mcp`.

### Start the Broker

[Installing with one command]({{< relref "/docs/getting-started" >}}) or with the archive script starts the service for you. If you installed with `--no-service` or by hand, start the Broker yourself.

Sessions connect to a Broker but never start one. If the host already has a system Broker you can use, skip this step; the session connects to it.

Otherwise, install a user-level service; the install starts it right away, and from then on it starts at login:

```sh
agent-peer service install
```

A user-level install must not run as root, and on macOS it needs a graphical login session; on a host without one, install the system-level service with `agent-peer service install --system`, as root or through an administrator; see [Run a system Broker]({{< relref "system-broker.md" >}}). Alternatively, run the Broker in a terminal outside Claude Code and leave it running:

```sh
agent-peer broker
```

A Broker started inside a session exits with code 6. Starting the Broker does not require reconnecting the MCP server. [Run the Broker as a service]({{< relref "run-as-service.md" >}}) covers both service forms and uninstalling it.

## Build from source

To build from source you need Go 1.27. Clone the repository and build:

```sh
VERSION=0.2.0
git clone https://github.com/wangzw/agent-peer
cd agent-peer
go build -trimpath -ldflags "-X main.version=$VERSION" -o agent-peer ./cmd/agent-peer
```

Without `-ldflags`, `agent-peer --version` prints `agent-peer dev`, and `agent-peer update` does not update such a binary. Installing with `go install …@version` is not supported, because `go.mod` uses a `replace` directive that points at a vendored dependency.

The build leaves `agent-peer` in the current directory; follow the manual installation steps above to put it on `PATH`, register the MCP server and start the Broker.
