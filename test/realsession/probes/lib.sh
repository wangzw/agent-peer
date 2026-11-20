#!/usr/bin/env bash
# lib.sh is sourced by every real-session probe script. It wraps tmux, the
# simulated Agents, the peer host container and the evidence helpers. Probe
# scripts only write $OUT, $R/ctl, $R/debug and $WORK, and read process
# environments through rs_proc_vars, which works only on Linux.

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

# RS_ROOT is required by the compose file for its bind mounts, and Compose
# interpolates the file for every command, not only up. Export it once so the
# exec and status calls carry it too.
export RS_ROOT="$R"

# RS_BROKER_PID is the Broker rs_start_broker started; empty when it started
# none. RS_BROKER_HOOKED keeps the stop hook registered once. RS_BROKER_LOG
# overrides the evidence log path when a probe needs another file.
RS_BROKER_PID=""
RS_BROKER_HOOKED=0
RS_BROKER_LOG=""

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

# rs_keys_literal sends text to a window character by character, without key
# name lookup. A command containing spaces or shell punctuation is typed as it
# is written, not parsed as a sequence of key names.
rs_keys_literal() {
	local window="$1" text="$2"
	tmux -L "$TMUX_SERVER" send-keys -t "$window" -l -- "$text"
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

# rs_wait_pane_literal polls the pane until it contains text as a literal
# substring, and keeps the pane in $OUT/timeout-$window.txt on a timeout.
rs_wait_pane_literal() {
	local window="$1" text="$2" seconds="$3"
	local deadline=$((SECONDS + seconds)) pane=""
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qF -- "$text"; then
			return 0
		fi
		sleep 0.1
	done
	pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
	printf '%s\n' "$pane" >"$OUT/timeout-$window.txt"
	return 1
}

# rs_wait_turn_idle waits until no turn has been running for one second. The
# "esc to interrupt" hint is used instead of the readiness criteria because
# 2.1.311 rotates the idle footer hints: "? for shortcuts" and "! for shell
# mode" take turns, so either can be absent while the session is idle.
rs_wait_turn_idle() {
	local window="$1" seconds="$2"
	local deadline=$((SECONDS + seconds)) pane="" quiet=0
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qF -- 'esc to interrupt'; then
			quiet=0
		else
			quiet=$((quiet + 1))
			if ((quiet >= 5)); then
				return 0
			fi
		fi
		sleep 0.2
	done
	return 1
}

# rs_submit_line submits one line to a session's composer. It waits for the
# previous turn to end, types the text literally (send-keys -l), waits until the
# composer echoes NEEDLE, pauses 0.3 seconds, and sends Enter as a second tmux
# write. 2.1.311 reads a text-and-Enter burst as one paste: the Enter stays in
# the composer as a newline and the line never submits. Prompts and bash-mode
# commands both go through this one path.
#
# Usage: rs_submit_line WINDOW TEXT NEEDLE [READY_SECONDS]
# READY_SECONDS bounds the idle wait and defaults to 60 seconds.
rs_submit_line() {
	local window="$1" text="$2" needle="$3" seconds="${4:-60}"
	if ! rs_wait_turn_idle "$window" "$seconds"; then
		rs_snap "$window" "prompt-busy-$window.txt"
		rs_obs "$window: previous turn still running after ${seconds}s, so the text was not typed"
		return 1
	fi
	rs_keys_literal "$window" "$text"
	if ! rs_wait_pane_literal "$window" "$needle" 10; then
		rs_obs "$window: the composer did not echo the typed text"
		return 1
	fi
	# The text is on screen; the pause keeps the last keystroke and Enter in
	# separate reads so the TUI does not fold them into one paste.
	sleep 0.3
	rs_keys "$window" Enter
}

# rs_send_prompt submits one prompt through rs_submit_line.
#
# Usage: rs_send_prompt WINDOW PROMPT [NEEDLE] [READY_SECONDS]
# NEEDLE defaults to the first 24 characters of PROMPT. A prompt that wraps far
# enough for its beginning to scroll off the composer needs a NEEDLE taken from
# the part that stays visible.
rs_send_prompt() {
	local window="$1" prompt="$2" needle="${3:-}" seconds="${4:-60}"
	if [[ -z "$needle" ]]; then
		needle="${prompt:0:24}"
	fi
	rs_submit_line "$window" "$prompt" "$needle" "$seconds"
}

# rs_send_shell submits one bash-mode command through rs_submit_line. The
# caller passes the command without the leading "!". NEEDLE defaults to the
# first 24 characters of the "!" line.
#
# Usage: rs_send_shell WINDOW COMMAND [NEEDLE] [READY_SECONDS]
rs_send_shell() {
	local window="$1" command="$2" needle="${3:-}" seconds="${4:-60}"
	if [[ -z "$needle" ]]; then
		needle="! ${command:0:22}"
	fi
	rs_submit_line "$window" "! $command" "$needle" "$seconds"
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

# rs_reply_excerpt writes the raw assistant records whose text contains marker
# to $OUT/$file. Only assistant records are read: a prompt that asks for a
# string is echoed in a user record, so matching the whole transcript would
# treat the prompt itself as the answer.
rs_reply_excerpt() {
	local marker="$1" file="$2" src
	: >"$OUT/$file"
	while IFS= read -r -d '' src; do
		jq -Rrc --arg marker "$marker" '
			fromjson?
			| select(type == "object")
			| select(.type == "assistant")
			| . as $record
			| ((.message.content? // empty)
				| if type == "string" then .
				  elif type == "array" then [.[] | select(.type == "text") | .text] | join("\n")
				  else "" end) as $text
			| select($text | contains($marker))
			| $record
		' "$src" >>"$OUT/$file" 2>/dev/null || true
	done < <(find "$R/cfg/projects" -type f -name '*.jsonl' -print0 2>/dev/null)
}

# rs_assistant_text_present reports whether any session transcript holds
# assistant text. It tells a missing reply apart from a transcript whose
# structure yields no assistant text at all.
rs_assistant_text_present() {
	local src
	while IFS= read -r -d '' src; do
		if jq -Re '
			fromjson?
			| select(type == "object")
			| select(.type == "assistant")
			| ((.message.content? // empty)
				| if type == "string" then .
				  elif type == "array" then [.[] | select(.type == "text") | .text] | join("\n")
				  else "" end)
			| select(length > 0)
		' "$src" >/dev/null 2>&1; then
			return 0
		fi
	done < <(find "$R/cfg/projects" -type f -name '*.jsonl' -print0 2>/dev/null)
	return 1
}

# rs_wait_reply polls the session transcripts until an assistant message's text
# contains marker, and writes the matching records to $OUT/$file. The pane is
# not part of the decision, because it can show the prompt that echoes the
# requested string. It returns 0 once MIN records match (default 1), 1 when
# assistant text was read but the marker never appeared, and 2 when no
# assistant text could be read at all.
rs_wait_reply() {
	local marker="$1" file="$2" seconds="$3" min="${4:-1}"
	local deadline=$((SECONDS + seconds)) count=0
	while ((SECONDS < deadline)); do
		rs_reply_excerpt "$marker" "$file"
		count="$(wc -l <"$OUT/$file" | tr -d ' ')"
		if ((count >= min)); then
			return 0
		fi
		sleep 0.2
	done
	rs_reply_excerpt "$marker" "$file"
	count="$(wc -l <"$OUT/$file" | tr -d ' ')"
	if ((count >= min)); then
		return 0
	fi
	if rs_assistant_text_present; then
		return 1
	fi
	return 2
}

# rs_proc_vars prints selected variables of a process without dumping the rest.
# A missing variable prints as "<NAME> absent"; a variable set to the empty
# string prints as "<NAME>=". The two cases are not the same: an unset variable
# and an empty one mean different things to a probe. Linux only: it reads
# /proc/<pid>/environ, which macOS does not provide.
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

# socket_owner_pid prints the pid of the process listening on the Unix socket at
# path. Linux reads the pid ss reports for that socket; macOS has no ss, so lsof
# is asked for the bound endpoint. lsof marks a connected client's name with a
# leading "->", and only the exact bound name matches, so a client that merely
# holds the socket is not mistaken for the listener. Either way a socket left by
# another process is not taken for the one the probe started.
socket_owner_pid() {
	local path="$1"
	if [[ "$(uname -s)" == Darwin ]]; then
		lsof -U -Fpn 2>/dev/null | awk -v s="$path" '
			/^p/ { pid = substr($0, 2) }
			$0 == "n" s { print pid; exit }'
		return
	fi
	ss -xlpn 2>/dev/null | awk -v s="$path" '
		index($0, s) {
			if (match($0, /pid=[0-9]+/)) {
				print substr($0, RSTART + 4, RLENGTH - 4)
				exit
			}
		}'
}

# rs_start_broker starts the account's user-level Broker and waits for the
# socket that process serves. The MCP server no longer starts a Broker, so every
# probe that uses one calls this before it starts a session. The log goes to
# $OUT/broker.log, and the Broker is stopped when the probe exits.
rs_start_broker() {
	local log="${RS_BROKER_LOG:-$OUT/broker.log}"
	local sock deadline owner
	sock="/tmp/agent-peer-$(id -u)/broker.sock"
	mkdir -p "$OUT"
	"$R/run" agent-peer broker >>"$log" 2>&1 &
	RS_BROKER_PID=$!
	if ((RS_BROKER_HOOKED == 0)); then
		rs_on_exit rs_stop_broker
		RS_BROKER_HOOKED=1
	fi
	deadline=$((SECONDS + 15))
	while ((SECONDS < deadline)); do
		if [[ -S "$sock" ]]; then
			if ! kill -0 "$RS_BROKER_PID" 2>/dev/null; then
				echo "rs_start_broker: the socket $sock is served by another process; the started Broker exited (see $log)" >&2
				return 1
			fi
			owner="$(socket_owner_pid "$sock" || true)"
			if [[ "$owner" == "$RS_BROKER_PID" ]]; then
				rs_obs "Broker ready at $sock (pid $RS_BROKER_PID)"
				return 0
			fi
			# The socket is stale or served by another process; wait for the
			# started Broker to bind it.
			sleep 0.2
			continue
		fi
		if ! kill -0 "$RS_BROKER_PID" 2>/dev/null; then
			wait "$RS_BROKER_PID" 2>/dev/null || true
			RS_BROKER_PID=""
			echo "rs_start_broker: the Broker exited before its socket appeared (see $log)" >&2
			return 1
		fi
		sleep 0.2
	done
	echo "rs_start_broker: the Broker socket $sock did not appear within 15s (see $log)" >&2
	return 1
}

# rs_stop_broker stops the Broker rs_start_broker started, if any.
rs_stop_broker() {
	if [[ -n "${RS_BROKER_PID:-}" ]]; then
		kill "$RS_BROKER_PID" 2>/dev/null || true
		wait "$RS_BROKER_PID" 2>/dev/null || true
		RS_BROKER_PID=""
	fi
}

# rs_hosta_up starts the peer host container and records its address.
rs_hosta_up() {
	docker compose -f "$RS_COMPOSE" up -d
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

# rs_artifact_label prints a name that an artifact upload accepts. Agents may
# be named after signatures that contain characters upload-artifact rejects,
# such as the "<" in a<b, or characters that break when the artifact is
# unpacked on Windows, such as a backslash. Every unsafe byte is percent-
# encoded, so distinct names stay distinct.
rs_artifact_label() {
	local s="$1" out="" ch enc i
	local LC_ALL=C
	for ((i = 0; i < ${#s}; i++)); do
		ch="${s:i:1}"
		case "$ch" in
			[A-Za-z0-9._-]) out+="$ch" ;;
			*)
				printf -v enc '%%%02X' "'$ch"
				out+="$enc"
				;;
		esac
	done
	printf '%s\n' "$out"
}

# rs_hosta_recv copies a container-side receive log out, naming the local file
# through rs_artifact_label so the Agent name cannot make the upload fail.
rs_hosta_recv() {
	local name="$1"
	docker compose -f "$RS_COMPOSE" exec -T hosta cat "/rs/$name.recv.jsonl" \
		>"$OUT/hosta-$(rs_artifact_label "$name").recv.jsonl"
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

# rs_require_model_turn makes a probe fail when its prompts never reached the
# model. It requires the environment's model-call counter to be at least MIN and
# some session transcript to hold a user record containing MARKER, and records
# the counter and both verdicts in observations.txt. Probes call it last, so a
# run whose prompt never submitted cannot pass on a pane capture alone.
#
# Usage: rs_require_model_turn MARKER [MIN]
rs_require_model_turn() {
	local marker="$1" min="${2:-1}"
	local usage total found=no src
	if [[ -z "$marker" || ! "$min" =~ ^[0-9]+$ ]]; then
		rs_obs "model-turn check: bad arguments marker=\"$marker\" min=\"$min\""
		return 1
	fi
	usage="$("$R/run" rsprobe env usage --root "$R" 2>/dev/null || true)"
	total="$(printf '%s\n' "$usage" | sed -n 's/^total //p' | tail -n 1)"
	if [[ ! "$total" =~ ^[0-9]+$ ]]; then
		total=unreadable
	fi
	while IFS= read -r -d '' src; do
		if jq -Rre --arg marker "$marker" '
			fromjson?
			| select(type == "object" and .type == "user")
			| (.message.content // empty)
			| if type == "string" then .
			  elif type == "array" then [.[] | (.text // "")] | join("\n")
			  else "" end
			| select(contains($marker))
		' "$src" >/dev/null 2>&1; then
			found=yes
			break
		fi
	done < <(find "$R/cfg/projects" -type f -name '*.jsonl' -print0 2>/dev/null)
	rs_obs "model-turn check: marker=\"$marker\" user_record=$found usage_total=$total (want >= $min)"
	if [[ "$found" != yes ]]; then
		rs_obs "model-turn check failed: no user record contains \"$marker\""
		return 1
	fi
	if [[ "$total" == unreadable ]] || ((total < min)); then
		rs_obs "model-turn check failed: usage total $total is below $min"
		return 1
	fi
	return 0
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

# RS_TRUST_DIALOG_RE matches the workspace trust dialog. It is kept separate
# from RS_READY_DIALOG_RE so rs_dismiss_trust_dialog can tell whether the
# dialog it answered has left the pane.
RS_TRUST_DIALOG_RE='Quick safety check|Yes, I trust this folder|Do you trust the files|Yes, I trust'

# RS_TRUST_DIALOG_RESEND_BUDGET bounds, in seconds, how long
# rs_dismiss_trust_dialog keeps resending when the caller passes no bound. The
# dialog has cleared after one or two 0.5-second bursts; ten seconds leaves
# room for a loaded runner, where a capture and the redraw can each take
# seconds, while staying under half of the smallest readiness budget a probe
# passes to rs_wait_ready, so a timeout there still leaves time for one retry.
RS_TRUST_DIALOG_RESEND_BUDGET=10

# rs_accept_trust_dialog sends the keys that answer the workspace trust dialog
# shown in the pane, and returns 0. It returns 2 without pressing anything when
# the dialog is on screen but neither known option carries the cursor: another
# screen may be underneath, and a blind Enter could confirm the wrong choice.
# It is separate from rs_wait_ready so the key choice can be tested without a
# tmux server.
#
# The dialog focuses "No, exit" when the probe root resolves through a symlink,
# which /tmp does on macOS (/private/tmp); Enter alone would then end the
# session. The cursor moves to the trust entry only when the marker sits on the
# refusal, so the Linux behavior is unchanged.
rs_accept_trust_dialog() {
	local window="$1" pane="$2"
	if printf '%s\n' "$pane" | grep -qF -- '❯ No, exit'; then
		rs_keys "$window" Down Enter
		return 0
	fi
	if printf '%s\n' "$pane" | grep -qF -- '❯ Yes, I trust this folder'; then
		rs_keys "$window" Enter
		return 0
	fi
	return 2
}

# rs_dismiss_trust_dialog sends the trust keys until the dialog leaves the
# pane, and prints how many key bursts that took. It returns 0 once the dialog
# is gone, 1 when the dialog is still there at the deadline, and 2 when a
# recognized dialog carries the cursor on neither known option.
#
# The pane is captured again before every send. Claude Code draws the dialog
# before it starts reading keys and may discard a burst that arrives while it
# switches the terminal to raw mode: on the macOS runner the frame captured a
# second later still showed the cursor on "No, exit", so the first burst had
# not been processed. The re-capture also keeps a second Down Enter from
# landing on the next screen if the first burst did take effect and only the
# redraw lagged.
#
# The deadline is checked only after a burst has been sent. SECONDS counts
# wall-clock time, so a clock step can put the deadline behind the helper
# before its first iteration; checking first made it print "failed after 0"
# without pressing a key. One attempt is always made, and the deadline bounds
# only the resends that follow it.
#
# A capture that fails says nothing about the dialog, so its empty output is
# not read as the dialog leaving. The capture is retried within the budget, and
# success is reported only from a capture that succeeded and showed no dialog.
rs_dismiss_trust_dialog() {
	local window="$1" seconds="${2:-$RS_TRUST_DIALOG_RESEND_BUDGET}" deadline pane="" sends=0 rc=0
	deadline=$((SECONDS + seconds))
	while :; do
		if ! pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null)"; then
			if ((SECONDS >= deadline)); then
				printf '%s\n' "$sends"
				return 1
			fi
			sleep 0.5
			continue
		fi
		if ! printf '%s\n' "$pane" | grep -qiE -- "$RS_TRUST_DIALOG_RE"; then
			printf '%s\n' "$sends"
			return 0
		fi
		rc=0
		rs_accept_trust_dialog "$window" "$pane" || rc=$?
		if ((rc == 2)); then
			printf '%s\n' "$sends"
			return 2
		fi
		sends=$((sends + 1))
		if ((SECONDS >= deadline)); then
			break
		fi
		sleep 0.5
	done
	printf '%s\n' "$sends"
	return 1
}

# rs_wait_ready polls a window until the TUI reaches its input composer and no
# dialog is on screen. A trust dialog is accepted by choosing its trust entry;
# the bypass-permissions warning opens focused on "No, exit", so it is accepted
# with Down then Enter. The criterion that matched is written to
# observations.txt, because which hint a version prints is itself evidence.
rs_wait_ready() {
	local window="$1" seconds="${2:-60}"
	local deadline=$((SECONDS + seconds)) pane="" criterion="" dialogs=0 dialog_bursts=0
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qF -- 'Yes, I accept'; then
			dialogs=$((dialogs + 1))
			printf '%s\n' "$pane" >"$OUT/dialog-$window-$dialogs.txt"
			rs_keys "$window" Down Enter
			sleep 1
			continue
		fi
		if printf '%s\n' "$pane" | grep -qiE -- "$RS_TRUST_DIALOG_RE"; then
			dialogs=$((dialogs + 1))
			printf '%s\n' "$pane" >"$OUT/dialog-$window-$dialogs.txt"
			rs_obs "$window: workspace trust dialog on screen; macOS resolves /tmp to /private/tmp and Claude Code keys trust by the resolved path, so an entry written for the literal path can miss"
			local bursts=0 rc=0
			bursts="$(rs_dismiss_trust_dialog "$window")" || rc=$?
			if ((rc == 2)); then
				rs_obs "$window: the trust dialog is on screen but the cursor is on neither known option; not pressing keys"
				return 1
			elif ((rc == 1)); then
				rs_obs "$window: the trust dialog was still on screen after $bursts key bursts; retrying"
			else
				dialog_bursts=$((dialog_bursts + bursts))
			fi
			continue
		fi
		if criterion="$(rs_ready_criterion "$pane")"; then
			if ((dialog_bursts == 1)); then
				rs_obs "$window: the trust dialog was dismissed with one key burst"
			elif ((dialog_bursts > 1)); then
				rs_obs "$window: the trust dialog took $dialog_bursts key bursts in all; the TUI discarded a burst sent while it was starting"
			fi
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
