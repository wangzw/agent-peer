#!/bin/sh
# hosta-init.sh prepares the peer host container. It writes the container's
# Broker configuration and the simulated Agents' configuration, starts the
# Broker in the background, and prints the container IP on standard output.
#
# The script runs as root inside the container, so the Broker keeps its
# runtime directory at /run/agent-peer and its data under /root/.agent-peer.
set -eu

BROKER_SOCKET=/run/agent-peer/broker.sock

mkdir -p /root/.agent-peer /rs/cfg/sessions /rs/work
# The Broker refuses a data directory that is accessible by group or other.
chmod 700 /root/.agent-peer /rs/cfg/sessions

cat >/root/.agent-peer/config.yaml <<'EOF'
broker:
  listen: 0.0.0.0:17443
  alias: hostA
EOF
chmod 600 /root/.agent-peer/config.yaml

cat >/rs/work/.agent-peer.yaml <<'EOF'
topics: [rs-probe]
EOF

agent-peer broker >/rs/broker.log 2>&1 &
broker_pid=$!

# The Broker prints its readiness on standard error; the probe only needs the
# socket, so poll for it. This is a script, not a test: at most 10 seconds in
# steps of 100 milliseconds.
attempt=0
while [ ! -S "$BROKER_SOCKET" ]; do
	attempt=$((attempt + 1))
	if [ "$attempt" -gt 100 ]; then
		echo "hosta-init: broker socket $BROKER_SOCKET did not appear" >&2
		exit 1
	fi
	if ! kill -0 "$broker_pid" 2>/dev/null; then
		echo "hosta-init: broker exited before creating $BROKER_SOCKET" >&2
		cat /rs/broker.log >&2 || true
		exit 1
	fi
	sleep 0.1
done

# Print only the address so the caller can capture it directly.
hostname -i | awk '{print $1}'
