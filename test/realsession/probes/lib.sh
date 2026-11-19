#!/usr/bin/env bash
# lib.sh is sourced by every real-session probe script. It wraps tmux, the
# simulated Agents, the peer host container and the evidence helpers. Probe
# scripts only write $OUT, $R/ctl, $R/debug and $WORK, and read process
# environments through rs_proc_vars.

set -euo pipefail

: "${R:?lib.sh needs R}"
: "${PROBE:?lib.sh needs PROBE}"

OUT="$R/out/$PROBE"
WORK="$R/work/$PROBE"
TMUX_SERVER=agent-peer-rs

# RS_COMPOSE is resolved from this script's location rather than the caller's
# working directory, so every probe finds the peer host compose file whether the
# workflow runs it from the repository root or a developer runs it from /tmp.
RS_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
RS_COMPOSE="$RS_REPO_ROOT/test/realsession/compose.yaml"

# RS_CONTAINER_WAIT bounds how long a probe waits for a started process, on
# the host or inside the peer host container, to create its control socket.
# The bound covers a cold container start and an external process start; it is
# not the 3-second rule the Go tests use.
RS_CONTAINER_WAIT=10

# RS_HOLD_CALL_WAIT bounds the background send_message call a ref-clear probe
# makes under the hold policy. Whether the call returns at once is itself an
# observation, so the bound only keeps the probe from waiting forever. The
# default is 10 seconds; an environment value overrides it.
RS_HOLD_CALL_WAIT="${RS_HOLD_CALL_WAIT:-10}"

# rs_on_exit registers a cleanup hook. Probe scripts register their cleanup
# with it instead of installing their own EXIT trap, so the library's single
# trap always runs every registered cleanup before it captures the windows of a
# failed run. Hooks run in registration order; one that returns non-zero makes
# the final exit status non-zero.
RS_EXIT_HOOKS=()
rs_on_exit() {
	RS_EXIT_HOOKS+=("$1")
}

# rs_run_exit_hooks is the only EXIT trap. It runs the registered cleanups,
# then copies every window's pane into $OUT when the script is failing, so a
# failed run leaves a transcript of each window even if a cleanup failed or an
# earlier trap would otherwise have replaced this one.
rs_run_exit_hooks() {
	local status=$? hook
	if ((${#RS_EXIT_HOOKS[@]} > 0)); then
		for hook in "${RS_EXIT_HOOKS[@]}"; do
			"$hook" || status=1
		done
	fi
	if ((status != 0)) && [[ -d "$OUT" ]]; then
		local window
		while IFS= read -r window; do
			[[ -n "$window" ]] || continue
			tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" >"$OUT/fail-$window.txt" 2>/dev/null || true
		done < <(tmux -L "$TMUX_SERVER" list-windows -F '#{window_name}' 2>/dev/null || true)
	fi
	exit "$status"
}
trap rs_run_exit_hooks EXIT

# rs_tmux_start starts the dedicated tmux server under the sanitized wrapper.
rs_tmux_start() {
	"$R/run" tmux -L "$TMUX_SERVER" -f /dev/null new-session -d -s base -c "$WORK"
}

# rs_window opens a window named $1 that runs the remaining arguments.
rs_window() {
	local window="$1"
	shift
	tmux -L "$TMUX_SERVER" new-window -d -n "$window" -c "$WORK" "$R/run" "$@"
}

# rs_keys sends keys to a window.
rs_keys() {
	local window="$1"
	shift
	tmux -L "$TMUX_SERVER" send-keys -t "$window" "$@"
}

# rs_snap writes a pane capture to $OUT/$2.
rs_snap() {
	local window="$1" file="$2"
	tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" >"$OUT/$file"
}

# rs_wait_pane polls the pane until the extended regular expression matches.
rs_wait_pane() {
	local window="$1" pattern="$2" seconds="$3"
	local deadline=$((SECONDS + seconds))
	local pane=""
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qE -- "$pattern"; then
			return 0
		fi
		sleep 0.2
	done
	pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
	printf '%s\n' "$pane" >"$OUT/timeout-$window.txt"
	return 1
}

# rs_wait_file polls until the path exists.
rs_wait_file() {
	local path="$1" seconds="$2"
	local deadline=$((SECONDS + seconds))
	while ((SECONDS < deadline)); do
		if [[ -e "$path" ]]; then
			return 0
		fi
		sleep 0.2
	done
	if [[ -e "$path" ]]; then
		return 0
	fi
	return 1
}

# rs_socket_of prints the inbox socket path registered for a session name.
rs_socket_of() {
	local name="$1" file
	for file in "$R"/cfg/sessions/*.json; do
		if [[ ! -e "$file" ]]; then
			continue
		fi
		if jq -e --arg name "$name" 'select(.name == $name) | .messagingSocketPath' "$file" >/dev/null 2>&1; then
			jq -r --arg name "$name" 'select(.name == $name) | .messagingSocketPath' "$file"
			return 0
		fi
	done
	return 1
}

# rs_agent starts a simulated Agent in the background and waits for its control socket.
rs_agent() {
	local name="$1" dir="${2:-$WORK}"
	"$R/run" rsprobe agent run \
		--config-dir "$R/cfg" \
		--name "$name" \
		--cwd "$dir" \
		--ctl "$R/ctl/$name.sock" \
		--recv-log "$OUT/$name.recv.jsonl" \
		--agent-peer "$R/bin/agent-peer" \
		>>"$OUT/agents.log" 2>&1 &
	# An external process starting is slower than the Go tests' 3-second rule.
	rs_wait_file "$R/ctl/$name.sock" "$RS_CONTAINER_WAIT"
}

# rs_call calls a tool on a simulated Agent and appends the reply to calls.jsonl.
rs_call() {
	local name="$1" tool="$2" args="${3-}" reply
	if [[ -z "$args" ]]; then
		args='{}'
	fi
	reply="$("$R/run" rsprobe agent call --ctl "$R/ctl/$name.sock" "$tool" "$args")"
	printf '%s\n' "$reply" >>"$OUT/calls.jsonl"
	printf '%s\n' "$reply"
}

# rs_excerpt copies the session records and debug lines that mention a marker.
rs_excerpt() {
	local marker="$1" file="$2" src
	: >"$OUT/$file"
	: >"$OUT/$file.debug"
	while IFS= read -r -d '' src; do
		grep -F -- "$marker" "$src" >>"$OUT/$file" || true
	done < <(find "$R/cfg/projects" -type f -name '*.jsonl' -print0 2>/dev/null)
	if [[ -d "$R/debug" ]]; then
		grep -RhF -- "$marker" "$R/debug" >>"$OUT/$file.debug" 2>/dev/null || true
	fi
}

# rs_proc_vars prints selected variables of a process without dumping the rest.
# A missing variable prints as "<NAME> absent"; a variable set to the empty
# string prints as "<NAME>=". The two cases are not the same: an unset variable
# and an empty one mean different things to a probe.
rs_proc_vars() {
	local pid="$1" var line environ
	shift
	for var in "$@"; do
		if [[ ! "$var" =~ ^[A-Z0-9_]+$ ]]; then
			echo "rs_proc_vars: refusing to read variable name $var" >&2
			return 1
		fi
		if [[ "$var" == "CLAUDE_CODE_OAUTH_TOKEN" ]]; then
			echo "rs_proc_vars: refusing to read CLAUDE_CODE_OAUTH_TOKEN" >&2
			return 1
		fi
	done
	environ="$(tr '\0' '\n' <"/proc/$pid/environ")"
	for var in "$@"; do
		line="$(printf '%s\n' "$environ" | grep -E -m1 -- "^${var}=" || true)"
		if [[ "$line" == "$var="* ]]; then
			printf '%s=%s\n' "$var" "${line#"$var="}"
		else
			printf '%s absent\n' "$var"
		fi
	done
}

# rs_hosta_up starts the peer host container and records its address.
rs_hosta_up() {
	RS_ROOT="$R" docker compose -f "$RS_COMPOSE" up -d
	HOSTA_IP="$(docker compose -f "$RS_COMPOSE" exec -T hosta /hosta-init.sh)"
}

# rs_hosta_agent starts a simulated Agent inside the container.
rs_hosta_agent() {
	local name="$1" deadline
	docker compose -f "$RS_COMPOSE" exec -d -T hosta \
		/usr/local/bin/rsprobe agent run \
		--config-dir /rs/cfg \
		--name "$name" \
		--cwd /rs/work \
		--ctl "/rs/$name.ctl" \
		--recv-log "/rs/$name.recv.jsonl" \
		--agent-peer /usr/local/bin/agent-peer
	deadline=$((SECONDS + RS_CONTAINER_WAIT))
	while ((SECONDS < deadline)); do
		if docker compose -f "$RS_COMPOSE" exec -T hosta test -S "/rs/$name.ctl"; then
			return 0
		fi
		sleep 0.2
	done
	docker compose -f "$RS_COMPOSE" exec -T hosta test -S "/rs/$name.ctl"
}

# rs_hosta_call calls a tool on a simulated Agent inside the container.
rs_hosta_call() {
	local name="$1" tool="$2" args="${3-}" reply
	if [[ -z "$args" ]]; then
		args='{}'
	fi
	reply="$(docker compose -f "$RS_COMPOSE" exec -T hosta \
		/usr/local/bin/rsprobe agent call --ctl "/rs/$name.ctl" "$tool" "$args")"
	printf '%s\n' "$reply"
}

# rs_hosta_recv copies a container-side receive log out.
rs_hosta_recv() {
	local name="$1"
	docker compose -f "$RS_COMPOSE" exec -T hosta cat "/rs/$name.recv.jsonl" \
		>"$OUT/hosta-$name.recv.jsonl"
}

# rs_pair_text prints a tool reply's .text for diagnostics with pairing keys
# redacted, so a failure never writes a whole key to the probe log.
rs_pair_text() {
	printf '%s' "$1" | sed -E 's/("key"[[:space:]]*:[[:space:]]*")[^"]*(")/\1<redacted>\2/g'
}

# rs_pair_hosta pairs the runner Broker with hosta and checks its alias. Both
# tools return a JSON object in .text; anything else fails the probe instead of
# falling back to whatever text arrived.
rs_pair_hosta() {
	local key reply text alias
	rs_hosta_agent pairer
	reply="$(rs_hosta_call pairer create_pairing_key '{}')"
	if ! text="$(printf '%s' "$reply" | jq -er '.text')"; then
		echo "rs_pair_hosta: create_pairing_key reply has no text field" >&2
		return 1
	fi
	if ! key="$(printf '%s' "$text" | jq -er '.key // empty')" || [[ -z "$key" ]]; then
		echo "rs_pair_hosta: create_pairing_key .text is not a key object; got: $(rs_pair_text "$text")" >&2
		return 1
	fi
	rs_agent pairer-b
	reply="$(rs_call pairer-b connect_broker "{\"address\":\"$HOSTA_IP:17443\",\"key\":\"$key\"}")"
	if ! text="$(printf '%s\n' "$reply" | jq -er '.text')"; then
		echo "rs_pair_hosta: connect_broker reply has no text field" >&2
		return 1
	fi
	if ! alias="$(printf '%s' "$text" | jq -er '.alias // empty')" || [[ -z "$alias" ]]; then
		echo "rs_pair_hosta: connect_broker .text is not an alias object; got: $(rs_pair_text "$text")" >&2
		return 1
	fi
	if [[ "$alias" != "hostA" ]]; then
		echo "rs_pair_hosta: expected alias hostA, got: $alias" >&2
		return 1
	fi
}

# rs_obs appends a timestamped observation.
rs_obs() {
	printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$OUT/observations.txt"
}

# RS_READY_DIALOG_RE matches the startup dialogs and selection menus that must
# not be on screen when a session is called ready. The trust dialog and the
# bypass-permissions warning are dismissed by rs_wait_ready; the remaining
# markers only keep readiness from being declared over an unknown menu.
RS_READY_DIALOG_RE='Quick safety check|Yes, I trust this folder|Do you trust the files|Yes, I trust|Yes, I accept|Enter to confirm'

# rs_ready_criterion prints the readiness criterion the pane satisfies and
# returns 0, or returns 1 when the pane shows no composer or still shows a
# dialog. The three criteria are the ones Claude Code prints: older versions
# end the hint with "? for shortcuts", 2.1.311 ends the permission-mode line
# with "(shift+tab to cycle)", and the empty composer shows a placeholder that
# starts with Try ". A bare "❯" is not a criterion: it is visible behind a
# dialog as well.
rs_ready_criterion() {
	local pane="$1"
	if printf '%s\n' "$pane" | grep -qiE -- "$RS_READY_DIALOG_RE"; then
		return 1
	fi
	if printf '%s\n' "$pane" | grep -qF -- '? for shortcuts'; then
		printf '%s\n' '? for shortcuts'
		return 0
	fi
	if printf '%s\n' "$pane" | grep -qF -- "(shift+tab to cycle)"; then
		printf '%s\n' "(shift+tab to cycle)"
		return 0
	fi
	if printf '%s\n' "$pane" | grep -qF -- 'Try "'; then
		printf '%s\n' 'Try " placeholder'
		return 0
	fi
	return 1
}

# rs_wait_ready polls a window until the TUI reaches its input composer and no
# dialog is on screen. A trust dialog is accepted with Enter; the
# bypass-permissions warning opens focused on "No, exit", so it is accepted
# with Down then Enter. The criterion that matched is written to
# observations.txt, because which hint a version prints is itself evidence.
rs_wait_ready() {
	local window="$1" seconds="${2:-60}"
	local deadline=$((SECONDS + seconds)) pane="" criterion="" dialogs=0
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qF -- 'Yes, I accept'; then
			dialogs=$((dialogs + 1))
			printf '%s\n' "$pane" >"$OUT/dialog-$window-$dialogs.txt"
			rs_keys "$window" Down Enter
			sleep 1
			continue
		fi
		if printf '%s\n' "$pane" | grep -qiE -- 'Quick safety check|Yes, I trust this folder|Do you trust the files|Yes, I trust'; then
			dialogs=$((dialogs + 1))
			printf '%s\n' "$pane" >"$OUT/dialog-$window-$dialogs.txt"
			rs_keys "$window" Enter
			sleep 1
			continue
		fi
		if criterion="$(rs_ready_criterion "$pane")"; then
			rs_obs "$window: ready; matched criterion: $criterion"
			return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$pane" >"$OUT/timeout-$window.txt"
	rs_obs "$window: not ready within ${seconds}s (see timeout-$window.txt)"
	return 1
}

# rs_mode_text prints the permission-mode line from a window's bottom bar.
# 2.1.311 writes a line ending in "(shift+tab to cycle)"; older versions wrote
# "? for shortcuts" with no mode. An empty result means the version or the
# screen state prints no mode line; rs_obs_mode records that fact rather than
# guessing a mode.
rs_mode_text() {
	local window="$1" pane line=""
	pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
	line="$(printf '%s\n' "$pane" | grep -F -- "(shift+tab to cycle)" | tail -n 1 || true)"
	if [[ -z "$line" ]]; then
		line="$(printf '%s\n' "$pane" | grep -F -- '? for shortcuts' | tail -n 1 || true)"
	fi
	printf '%s\n' "$line" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'
}

# rs_obs_mode records the permission-mode text a session shows after startup.
# A session that prints no mode line is recorded as such instead of being
# assigned a mode the probe did not see.
rs_obs_mode() {
	local window="$1" line
	line="$(rs_mode_text "$window")"
	if [[ -z "$line" ]]; then
		rs_obs "$window: bottom bar shows no permission-mode text"
	else
		rs_obs "$window: bottom bar mode text: $line"
	fi
}

# rs_pane_idle waits until the window is back at its composer and no turn is
# running. The "esc to interrupt" hint marks a running turn, which the
# readiness criteria alone would not exclude.
rs_pane_idle() {
	local window="$1" seconds="$2"
	local deadline=$((SECONDS + seconds)) pane=""
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if ! printf '%s\n' "$pane" | grep -qF -- 'esc to interrupt' &&
			rs_ready_criterion "$pane" >/dev/null; then
			return 0
		fi
		sleep 0.5
	done
	return 1
}
