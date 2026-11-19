#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# broker-down answers REQ-agent-messaging-038. It stops the account Broker,
# holds its listen address with an unrelated process, then starts a session and
# calls list_agents: the MCP service must stay connected, the first call must
# report the unavailable listen address, and the same call must succeed after
# the address is released, all without reconnecting the MCP service.

PORT=27443
RUNTIME_DIR="/tmp/agent-peer-$(id -u)"
BROKER_SOCKET="$RUNTIME_DIR/broker.sock"
BROKER_LOCK="$RUNTIME_DIR/broker.lock"
PROMPT='Call mcp__peer__list_agents and show the raw result.'
ERROR_MARKER='listen address is unavailable'
SUCCESS_MARKER='down@hostB'

occupier_pid=""
cleanup() {
	if [[ -n "$occupier_pid" ]] && kill -0 "$occupier_pid" 2>/dev/null; then
		kill "$occupier_pid" 2>/dev/null || true
		wait "$occupier_pid" 2>/dev/null || true
	fi
}
rs_on_exit cleanup

# broker_pids prints the pids whose executable is R/bin/agent-peer and whose
# first argument is "broker". The intermediate broker-launcher process has the
# same executable but "internal" as its first argument and is skipped.
broker_pids() {
	local dir pid exe arg1
	for dir in /proc/[0-9]*; do
		pid="${dir#/proc/}"
		exe="$(readlink "$dir/exe" 2>/dev/null || true)"
		if [[ "$exe" != "$R/bin/agent-peer" ]]; then
			continue
		fi
		arg1="$(tr '\0' '\n' <"$dir/cmdline" 2>/dev/null | sed -n '2p' || true)"
		if [[ "$arg1" == "broker" ]]; then
			printf '%s\n' "$pid"
		fi
	done
}

# lock_holders prints the pids that have the account lock file open. The Broker
# releases the flock when it exits, so an empty list means the lock is free.
lock_holders() {
	local dir pid fd target
	for dir in /proc/[0-9]*; do
		pid="${dir#/proc/}"
		for fd in "$dir"/fd/*; do
			target="$(readlink "$fd" 2>/dev/null || true)"
			if [[ "$target" == "$BROKER_LOCK" ]]; then
				printf '%s\n' "$pid"
				break
			fi
		done
	done
}

# wait_for polls a predicate every 200 ms for the given number of seconds.
wait_for() {
	local seconds="$1" check="$2"
	local deadline=$((SECONDS + seconds))
	while ((SECONDS < deadline)); do
		if "$check"; then
			return 0
		fi
		sleep 0.2
	done
	"$check"
}

# mcp_pids prints the pids whose executable is R/bin/agent-peer and whose first
# argument is "mcp". The MCP service process is the one Claude Code keeps for
# the session; its pid staying the same across recovery is the file evidence
# that the session never reconnected the service.
mcp_pids() {
	local dir pid exe arg1
	for dir in /proc/[0-9]*; do
		pid="${dir#/proc/}"
		exe="$(readlink "$dir/exe" 2>/dev/null || true)"
		if [[ "$exe" != "$R/bin/agent-peer" ]]; then
			continue
		fi
		arg1="$(tr '\0' '\n' <"$dir/cmdline" 2>/dev/null | sed -n '2p' || true)"
		if [[ "$arg1" == "mcp" ]]; then
			printf '%s\n' "$pid"
		fi
	done
}

# usage_total prints the model-call counter. An unreadable counter prints 0 so
# the caller's arithmetic still works; rs_require_model_turn reports the
# unreadable value as a failure on its own.
usage_total() {
	local out total
	out="$("$R/run" rsprobe env usage --root "$R" 2>/dev/null || true)"
	total="$(printf '%s\n' "$out" | sed -n 's/^total //p' | tail -n 1)"
	if [[ ! "$total" =~ ^[0-9]+$ ]]; then
		total=0
	fi
	printf '%s\n' "$total"
}

no_broker_running() {
	[[ -z "$(broker_pids)" ]]
}

no_lock_holder() {
	[[ -z "$(lock_holders)" ]]
}

broker_socket_present() {
	[[ -S "$BROKER_SOCKET" ]]
}

# wait_for_record polls the session records until a line contains the marker.
# The marker is not copied anywhere here; evidence is taken with rs_excerpt.
wait_for_record() {
	local marker="$1" seconds="$2"
	local deadline=$((SECONDS + seconds)) file
	while ((SECONDS < deadline)); do
		while IFS= read -r -d '' file; do
			if grep -qF -- "$marker" "$file"; then
				return 0
			fi
		done < <(find "$R/cfg/projects" -type f -name '*.jsonl' -print0 2>/dev/null)
		sleep 0.5
	done
	return 1
}

# Step 1: stop the Broker the runner may already have, and confirm the account
# lock is free before the listen address is held.
existing="$(broker_pids)"
if [[ -n "$existing" ]]; then
	rs_obs "stopping the running Broker: $(printf '%s' "$existing" | tr '\n' ' ')"
	while IFS= read -r pid; do
		if [[ -n "$pid" ]]; then
			kill -TERM "$pid" 2>/dev/null || true
		fi
	done <<<"$existing"
	if ! wait_for 20 no_broker_running; then
		rs_obs "a Broker process did not exit after SIGTERM"
		exit 1
	fi
else
	rs_obs "no Broker was running before the probe"
fi
if ! wait_for 10 no_lock_holder; then
	rs_obs "the account lock is still held after the Broker stopped"
	exit 1
fi
rs_obs "account lock released"

# Step 2: hold 127.0.0.1:$PORT so a newly launched Broker cannot bind it.
"$R/run" python3 -c '
import socket

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
sock.bind(("127.0.0.1", 27443))
sock.listen(8)
print("occupying", flush=True)
while True:
    conn, _ = sock.accept()
    conn.close()
' >"$OUT/occupier.log" 2>&1 &
occupier_pid=$!
deadline=$((SECONDS + 10))
until grep -q '^occupying$' "$OUT/occupier.log" 2>/dev/null; do
	if ((SECONDS >= deadline)) || ! kill -0 "$occupier_pid" 2>/dev/null; then
		rs_obs "the port occupier failed to bind 127.0.0.1:$PORT"
		exit 1
	fi
	sleep 0.2
done
rs_obs "port $PORT is held by pid $occupier_pid"

# Step 3: start the interactive session. The MCP server it spawns finds no
# Broker socket and keeps trying to launch one against the occupied address.
rs_tmux_start
rs_window down claude --model haiku -n down --allowedTools mcp__peer__list_agents

socket=""
deadline=$((SECONDS + 40))
while ((SECONDS < deadline)); do
	if socket="$(rs_socket_of down)"; then
		break
	fi
	sleep 0.2
done
if [[ -z "$socket" ]]; then
	rs_obs "session down did not register an inbox socket"
	rs_snap down timeout-down-start.txt
	exit 1
fi
rs_obs "session down uses inbox socket $socket"

# Step 4: capture the MCP view, where peer must show as available although the
# Broker cannot start. Claude Code 2.1.311's /mcp list marks an available
# server as "✔ peer   N tools" and does not print "connected". The snapshot is
# the evidence; the pane only paces the probe.
sleep 2
rs_keys down '/mcp'
rs_keys down Enter
if rs_wait_pane_literal down '✔ peer' 20; then
	rs_obs "the /mcp view lists peer as available (2.1.311's list form, ✔ peer) before recovery (see mcp-before.txt)"
else
	rs_obs "the /mcp view did not list ✔ peer within 20s (see mcp-before.txt)"
fi
rs_snap down mcp-before.txt
rs_keys down Escape
sleep 1

# The MCP service process is recorded before recovery, so the after-recovery
# pid set can show that the same process stayed connected.
mcp_pids | sort >"$OUT/mcp-pids-before.txt"
mcp_before="$(tr '\n' ' ' <"$OUT/mcp-pids-before.txt")"
if [[ -z "${mcp_before// /}" ]]; then
	rs_obs "no MCP service process is running before recovery"
	rs_snap down timeout-down-no-mcp.txt
	exit 1
fi
rs_obs "MCP service process before recovery: $mcp_before"

# Step 5: the first call must return the unavailable listen address.
rs_keys down "$PROMPT"
rs_keys down Enter
rs_obs "submitted the first list_agents prompt while the address was held"
if ! wait_for_record "$ERROR_MARKER" 120; then
	rs_obs "the first list_agents result did not name the unavailable listen address"
	rs_snap down timeout-down-first.txt
	exit 1
fi
rs_excerpt "$ERROR_MARKER" list-agents-error.jsonl
rs_obs "the first list_agents result names the unavailable listen address"

# The first prompt must be a real model turn, not a pane echo. Wait for the
# turn to end (pacing only) and freeze the usage baseline before the second
# prompt, so the second prompt can be required to add its own call.
if ! rs_wait_turn_idle down 120; then
	rs_obs "the first list_agents turn did not end within 120s; the usage baseline may be early"
fi
rs_require_model_turn "$PROMPT" 1
usage_after_first="$(usage_total)"
rs_obs "usage total after the first prompt: $usage_after_first"

# Step 6: release the address. No reconnection is done in Claude Code; the MCP
# service must retry and launch the Broker by itself within its 30 s cap.
kill "$occupier_pid" 2>/dev/null || true
wait "$occupier_pid" 2>/dev/null || true
occupier_pid=""
rs_obs "released port $PORT; waiting for the MCP service to retry"

if ! wait_for 45 broker_socket_present; then
	rs_obs "the Broker socket did not reappear within 45s"
	rs_snap down timeout-down-socket.txt
	exit 1
fi
rs_obs "the Broker socket reappeared at $BROKER_SOCKET"

registered=0
deadline=$((SECONDS + 30))
while ((SECONDS < deadline)); do
	if "$R/run" rsprobe status >"$OUT/status-after-recovery.txt" 2>&1 &&
		grep -qF -- "$SUCCESS_MARKER" "$OUT/status-after-recovery.txt"; then
		registered=1
		break
	fi
	sleep 0.5
done
if ((registered != 1)); then
	rs_obs "the Broker did not list session down within 30s of the socket reappearing"
	rs_snap down timeout-down-register.txt
	exit 1
fi
rs_obs "the Broker lists session down after the background retry"

broker_pids >"$OUT/broker-pids.txt" 2>&1 || true
while IFS= read -r pid; do
	if [[ -n "$pid" ]]; then
		rs_obs "Broker running after recovery: pid $pid"
	fi
done <"$OUT/broker-pids.txt"

# Step 7: the same prompt must now succeed.
rs_keys down "$PROMPT"
rs_keys down Enter
rs_obs "submitted the same list_agents prompt after recovery"
if ! wait_for_record "$SUCCESS_MARKER" 120; then
	rs_obs "the second list_agents result did not list session down"
	rs_snap down timeout-down-second.txt
	exit 1
fi
rs_excerpt "$SUCCESS_MARKER" list-agents-recovered.jsonl
rs_obs "the second list_agents result lists session down"

# Step 8: capture the MCP view again. It must still list peer as available
# without any manual reconnection. Claude Code 2.1.311 marks an available
# server as "✔ peer   N tools" rather than printing "connected". The turn is
# waited out first so the slash command is not typed into a running turn.
if ! rs_wait_turn_idle down 120; then
	rs_obs "the second list_agents turn did not end within 120s before the /mcp view"
fi
rs_keys down '/mcp'
rs_keys down Enter
if rs_wait_pane_literal down '✔ peer' 20; then
	rs_obs "the /mcp view still lists peer as available (2.1.311's list form, ✔ peer) after recovery (see mcp-after.txt)"
else
	rs_obs "the /mcp view did not list ✔ peer after recovery (see mcp-after.txt)"
fi
rs_snap down mcp-after.txt
rs_keys down Escape

# The MCP service process must be the same one that was recorded before
# recovery. A different pid would mean the service exited and Claude Code
# started it again, which is a reconnection the session should not need.
mcp_pids | sort >"$OUT/mcp-pids-after.txt"
mcp_after="$(tr '\n' ' ' <"$OUT/mcp-pids-after.txt")"
rs_obs "MCP service process after recovery: $mcp_after"
if cmp -s "$OUT/mcp-pids-before.txt" "$OUT/mcp-pids-after.txt"; then
	rs_obs "the MCP service process pid set is unchanged across recovery"
else
	rs_obs "the MCP service process pid set changed across recovery; compare mcp-pids-before.txt and mcp-pids-after.txt"
	exit 1
fi

# Both list_agents prompts are user-visible model turns. A run whose prompt
# never submitted can still show the MCP view and a stale Broker socket, so the
# probe ends only when each prompt is in a user record and the usage counter
# grew after the second one.
rs_require_model_turn "$PROMPT" $((usage_after_first + 1))
