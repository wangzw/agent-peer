#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

if ! rs_start_broker; then
	exit 1
fi

# registry.sh probes whether the Broker follows the Claude Code session
# registry reliably on Linux, and whether a renamed session is listed under its
# new name with its previous name.
#
# The pre-push check of the plan ("Status must not reread records") holds: the
# Status handler in internal/broker/status.go reads only the in-memory table,
# and the Reread matches under internal/broker are in delivery, send, receive,
# listagents and tracking. rsprobe status therefore shows exactly what the
# notification path applied.
#
# The probe reads the registry through two independent witnesses: rsprobe watch
# scans the directory itself and compares its poll observations with fsnotify
# notifications, and rsprobe status reports the Broker's table.

PROMPT='When a cross-session message arrives, reply with the single word ok and do nothing else. Do not call any tool unless the user explicitly asks.'

printf 'op\tsubject\tattempt\telapsed_ms\tdetail\n' >"$OUT/timings.tsv"

# reg_status prints the Broker's Agent table. A transient failure prints
# nothing; the polling callers retry until their deadline.
reg_status() {
	"$R/run" rsprobe status 2>>"$OUT/status.err" || true
}

# reg_line_of reads a status table on standard input and prints the line of the
# Agent whose address carries name. Addresses are "name@alias" or
# "name [ref]@alias"; comparing the name exactly keeps reg-a from matching
# reg-a2 and ab from matching a hexadecimal short ref.
reg_line_of() {
	awk -v want="$1" '
		/^peer / { next }
		{
			addr = $2
			sub(/@.*/, "", addr)
			sub(/ \[[0-9a-f]+\]$/, "", addr)
			if (addr == want) { print; exit }
		}'
}

# reg_line_present reports whether the table lists name.
reg_line_present() {
	local name="$1" table line
	table="$(reg_status)"
	line="$(printf '%s\n' "$table" | reg_line_of "$name")"
	[[ -n "$line" ]]
}

# reg_wait_present waits until the table lists name.
reg_wait_present() {
	local name="$1" seconds="$2" deadline
	deadline=$((SECONDS + seconds))
	while ((SECONDS < deadline)); do
		if reg_line_present "$name"; then
			return 0
		fi
		sleep 0.2
	done
	reg_line_present "$name"
}

# reg_now_ms prints the current time in milliseconds.
reg_now_ms() {
	date +%s%3N
}

# reg_timing appends one row to timings.tsv.
reg_timing() {
	printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" >>"$OUT/timings.tsv"
}

# reg_measure_present polls until name is listed and records the elapsed
# milliseconds plus the previous-name field. The first poll runs immediately,
# as the plan requires for the rename measurement.
reg_measure_present() {
	local name="$1" op="$2" attempt="$3" seconds="$4" start="$5"
	local deadline table line now elapsed prev
	deadline=$((SECONDS + seconds))
	while :; do
		table="$(reg_status)"
		line="$(printf '%s\n' "$table" | reg_line_of "$name")"
		if [[ -n "$line" ]]; then
			now="$(reg_now_ms)"
			elapsed=$((now - start))
			prev="$(printf '%s\n' "$line" | sed -n 's/.* prev=\(.*\)$/\1/p')"
			printf '%s\n' "$table" >"$OUT/status-$op-$attempt.txt"
			reg_timing "$op" "$name" "$attempt" "$elapsed" "prev=$prev"
			return 0
		fi
		if ((SECONDS >= deadline)); then
			printf '%s\n' "$table" >"$OUT/status-$op-$attempt.txt"
			reg_timing "$op" "$name" "$attempt" "timeout" "not listed within ${seconds}s"
			return 1
		fi
		sleep 0.2
	done
}

# reg_measure_gone polls until name is no longer listed. A table that does not
# parse (for example while the Broker restarts) is not taken as removal.
reg_measure_gone() {
	local name="$1" op="$2" attempt="$3" seconds="$4" start="$5"
	local deadline table line now elapsed
	deadline=$((SECONDS + seconds))
	while :; do
		table="$(reg_status)"
		if [[ "$table" == *status=* ]]; then
			line="$(printf '%s\n' "$table" | reg_line_of "$name")"
			if [[ -z "$line" ]]; then
				now="$(reg_now_ms)"
				elapsed=$((now - start))
				printf '%s\n' "$table" >"$OUT/status-$op-$attempt.txt"
				reg_timing "$op" "$name" "$attempt" "$elapsed" "removed"
				return 0
			fi
		fi
		if ((SECONDS >= deadline)); then
			printf '%s\n' "$table" >"$OUT/status-$op-$attempt.txt"
			reg_timing "$op" "$name" "$attempt" "timeout" "still listed after ${seconds}s"
			return 1
		fi
		sleep 0.2
	done
}

# reg_call_error invokes a tool whose refusal is the observation. rs_call from
# lib.sh cannot record it: under set -e it stops before writing the answer of a
# failing call, and step 4 expects the refusal to the old address as evidence.
reg_call_error() {
	local name="$1" tool="$2" args="$3" reply rc=0
	reply="$("$R/run" rsprobe agent call --ctl "$R/ctl/$name.sock" "$tool" "$args" 2>>"$OUT/agent-call.err")" || rc=$?
	printf '%s\n' "$reply" >>"$OUT/calls.jsonl"
	printf '%s\t%s\t%s\n' "$tool" "$rc" "$reply" >>"$OUT/call-results.tsv"
	printf '%s\n' "$reply"
}

# reg_broker_pid prints the pid of the Broker rs_start_broker started. It runs
# as "<exe> broker" with its standard error in $OUT/broker.log.
reg_broker_pid() {
	local proc exe second
	for proc in /proc/[0-9]*; do
		exe="$(readlink "$proc/exe" 2>/dev/null)" || continue
		[[ "$exe" == "$R/bin/agent-peer" ]] || continue
		[[ -r "$proc/cmdline" ]] || continue
		second="$(tr '\0' '\n' <"$proc/cmdline" | sed -n '2p')"
		if [[ "$second" == broker ]]; then
			printf '%s\n' "${proc#/proc/}"
			return 0
		fi
	done
	return 1
}

# Step 2: watch the registry independently of the Broker.
"$R/run" rsprobe watch --dir "$R/cfg/sessions" --out "$OUT/watch.jsonl" \
	>"$OUT/watch-summary.txt" 2>"$OUT/watch.err" &
WATCH_PID=$!
rs_obs "watching $R/cfg/sessions (watch pid $WATCH_PID)"

# Step 3: start three interactive sessions and wait until all are registered.
rs_tmux_start
rs_window reg-a claude --model haiku -n reg-a --permission-mode default --append-system-prompt "$PROMPT" --debug-file "$R/debug/reg-a.debug"
rs_window reg-b claude --model haiku -n reg-b --permission-mode default --append-system-prompt "$PROMPT" --allowedTools "Bash(sleep:*)" --debug-file "$R/debug/reg-b.debug"
rs_window reg-c claude --model haiku -n reg-c --permission-mode default --append-system-prompt "$PROMPT" --debug-file "$R/debug/reg-c.debug"
for name in reg-a reg-b reg-c; do
	if ! reg_wait_present "$name" 30; then
		rs_snap "$name" "timeout-$name.txt" || true
		rs_obs "session $name did not register within 30s"
		exit 1
	fi
done
# Registration happens before the composer accepts keys; the rename and prompt
# below must not be typed into a starting session.
for name in reg-a reg-b reg-c; do
	if ! rs_wait_ready "$name" 30; then
		rs_snap "$name" "ready-$name-timeout.txt" || true
		rs_obs "session $name did not reach its input prompt within 30s"
		exit 1
	fi
	rs_obs_mode "$name"
done
reg_status >"$OUT/status-initial.txt"
rs_obs "reg-a, reg-b and reg-c registered"

# Step 4: rename reg-a, then check the Broker and the simulated Agent see the
# new name and reject the old address.
start="$(reg_now_ms)"
rs_keys reg-a '/rename reg-a2' Enter
if reg_measure_present reg-a2 rename 1 5 "$start"; then
	rs_obs "rename reg-a -> reg-a2 reflected"
else
	rs_obs "rename reg-a -> reg-a2 was not reflected within 5s"
fi

rs_agent reg-obs
list_reply="$(rs_call reg-obs list_agents '{}')"
if [[ "$list_reply" == *reg-a2* ]]; then
	rs_obs "list_agents lists reg-a2 after the rename"
else
	rs_obs "list_agents does not list reg-a2 after the rename"
fi
stale_reply="$(reg_call_error reg-obs send_message '{"to":"reg-a@hostB","message":"x"}')"
if [[ "$stale_reply" == *'"ok":false'* && "$stale_reply" == *reg-a2* ]]; then
	rs_obs "send_message to reg-a@hostB refused and named reg-a2"
else
	rs_obs "send_message to reg-a@hostB answered: $stale_reply"
fi

# Step 5: reg-b runs a sleeping Bash command; record idle -> busy -> idle.
start="$(reg_now_ms)"
rs_send_prompt reg-b 'Run the shell command "sleep 20" with the Bash tool, then reply done.' 'Run the shell command'
busy_seen=0
idle_seen=0
deadline=$((SECONDS + 60))
while ((SECONDS < deadline)); do
	table="$(reg_status)"
	line="$(printf '%s\n' "$table" | reg_line_of reg-b)"
	label="$(printf '%s\n' "$line" | sed -n 's/.* status=\([^ ]*\) .*/\1/p')"
	if [[ "$label" == busy ]] && ((busy_seen == 0)); then
		busy_seen=1
		now="$(reg_now_ms)"
		printf '%s\n' "$table" >"$OUT/status-busy-reg-b.txt"
		reg_timing status reg-b 1 "$((now - start))" "idle->busy"
	elif [[ "$label" == idle ]] && ((busy_seen == 1)); then
		idle_seen=1
		now="$(reg_now_ms)"
		printf '%s\n' "$table" >"$OUT/status-idle-reg-b.txt"
		reg_timing status reg-b 1 "$((now - start))" "busy->idle"
		break
	fi
	sleep 0.5
done
if ((busy_seen == 0)); then
	rs_obs "reg-b never reported busy within 60s of the Bash prompt"
elif ((idle_seen == 0)); then
	rs_obs "reg-b stayed busy past the 60s deadline"
fi

# Step 6: an unusable name is not registered; renaming to a usable one is.
rs_window invalid claude --model haiku -n 'a@b' --permission-mode default --append-system-prompt "$PROMPT" --allowedTools mcp__peer__list_agents --debug-file "$R/debug/invalid.debug"
if ! rs_wait_ready invalid 45; then
	rs_obs "the a@b session did not reach its input prompt within 45s"
fi
rs_obs_mode invalid
reg_status >"$OUT/status-invalid.txt"
if reg_line_present ab; then
	rs_obs "the a@b session is listed as ab before /rename"
else
	rs_obs "the a@b session is not listed before /rename"
fi
rs_send_prompt invalid 'Call the mcp__peer__list_agents tool and show me its raw result.' 'Call the mcp__peer__list_agents'
if ! rs_wait_pane invalid 'code: ' 60; then
	rs_obs "no tool error text appeared in the a@b pane within 60s"
fi
rs_snap invalid invalid-call.txt
rs_excerpt 'mcp__peer__list_agents' invalid-call-record.jsonl
if ! rs_pane_idle invalid 30; then
	rs_obs "the a@b pane was not idle within 30s before /rename"
fi
start="$(reg_now_ms)"
rs_keys invalid '/rename ab' Enter
if reg_measure_present ab rename-invalid 1 5 "$start"; then
	rs_obs "rename a@b -> ab reflected"
else
	rs_obs "rename a@b -> ab was not reflected within 5s"
fi

# Step 7: end reg-c and measure when Status drops it.
start="$(reg_now_ms)"
rs_keys reg-c '/exit' Enter
if reg_measure_gone reg-c exit 1 5 "$start"; then
	rs_obs "exit of reg-c reflected"
else
	rs_obs "exit of reg-c was not reflected within 5s"
fi

# Step 8: repeat the rename and exit measurements nine more times.
for attempt in 2 3 4 5 6 7 8 9 10; do
	if ((attempt % 2 == 0)); then
		new_name=reg-a
	else
		new_name=reg-a2
	fi
	start="$(reg_now_ms)"
	rs_keys reg-a "/rename $new_name" Enter
	if ! reg_measure_present "$new_name" rename "$attempt" 5 "$start"; then
		rs_obs "rename attempt $attempt to $new_name was not reflected within 5s"
	fi

	exit_name="reg-exit-$attempt"
	rs_window "$exit_name" claude --model haiku -n "$exit_name" --permission-mode default --append-system-prompt "$PROMPT" --debug-file "$R/debug/$exit_name.debug"
	if ! reg_wait_present "$exit_name" 30; then
		rs_snap "$exit_name" "timeout-$exit_name.txt" || true
		rs_obs "session $exit_name did not register within 30s"
		exit 1
	fi
	if ! rs_wait_ready "$exit_name" 30; then
		rs_obs "session $exit_name did not reach its input prompt within 30s"
		exit 1
	fi
	rs_obs_mode "$exit_name"
	start="$(reg_now_ms)"
	rs_keys "$exit_name" '/exit' Enter
	if ! reg_measure_gone "$exit_name" exit "$attempt" 5 "$start"; then
		rs_obs "exit attempt $attempt ($exit_name) was not reflected within 5s"
	fi
done

# Record the inotify capacity and how many instances the Broker holds.
max_instances=unreadable
if [[ -r /proc/sys/fs/inotify/max_user_instances ]]; then
	max_instances="$(cat /proc/sys/fs/inotify/max_user_instances)"
fi
{
	printf 'max_user_instances=%s\n' "$max_instances"
	if broker_pid="$(reg_broker_pid)"; then
		instances=0
		for fdinfo in "/proc/$broker_pid"/fdinfo/*; do
			[[ -e "$fdinfo" ]] || continue
			if grep -q '^inotify ' "$fdinfo"; then
				instances=$((instances + 1))
			fi
		done
		printf 'broker_pid=%s\nbroker_inotify_instances=%s\n' "$broker_pid" "$instances"
	else
		printf 'broker_pid=not found\nbroker_inotify_instances=unknown\n'
	fi
} >"$OUT/inotify.txt"

# Step 9: stop the watcher and record what it saw.
reg_status >"$OUT/status-final.txt"
kill -INT "$WATCH_PID" 2>/dev/null || true
watch_rc=0
wait "$WATCH_PID" || watch_rc=$?
watch_summary="$(sed -n '1p' "$OUT/watch-summary.txt")"
printf 'watch_rc=%s\n%s\n' "$watch_rc" "$watch_summary" >"$OUT/watch-result.txt"
rs_obs "watch exited with rc=$watch_rc: $watch_summary"

# The status prompt is the first model prompt the probe types; a run that never
# submitted it, or that recorded no model call at all, is a false pass.
rs_require_model_turn 'Run the shell command' 1
