#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# The preflight probe checks that the runner isolation holds, that a session
# can authenticate, and where a session record keeps the name of the sender it
# identified. It follows the plan's "environment preflight and authentication
# baseline" item.

# The composer renders this hint once it accepts input and no dialog is up;
# the trust dialog is the only first-run screen the prepared config can leave.
prompt_re='for shortcuts'
dialog_re='Quick safety check|Yes, I trust this folder'

# wait_for_prompt polls the window until the input composer is ready. A trust
# dialog drawn over the composer is captured once and accepted with Enter.
wait_for_prompt() {
	local window="$1" seconds="$2"
	local deadline=$((SECONDS + seconds)) dialogs=0 pane=""
	while ((SECONDS < deadline)); do
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if grep -qE -- "$dialog_re" <<<"$pane"; then
			dialogs=$((dialogs + 1))
			printf '%s\n' "$pane" >"$OUT/dialog-${dialogs}.txt"
			rs_keys "$window" Enter
			sleep 1
			continue
		fi
		if grep -qE -- "$prompt_re" <<<"$pane"; then
			return 0
		fi
		sleep 0.2
	done
	printf '%s\n' "$pane" >"$OUT/timeout-${window}.txt"
	echo "preflight: $window did not reach the input prompt" >&2
	return 1
}

# 1. The peer server is configured by the workflow's prepare step.
if ! "$R/run" claude mcp list >"$OUT/mcp-list.txt"; then
	echo "preflight: claude mcp list failed" >&2
fi

# 2. Start the calibration session with the low-call-count prompt.
rs_tmux_start
rs_window calib claude --model haiku -n rs-calib \
	--append-system-prompt "When a cross-session message arrives, reply with the single word ok and do nothing else. Do not call any tool unless the user explicitly asks." \
	--debug-file "$R/debug/calib.debug"
wait_for_prompt calib 30

# 3. Wait for the session to bind its inbox and appear in the Broker's table.
socket=""
deadline=$((SECONDS + 15))
while ((SECONDS < deadline)); do
	socket="$(rs_socket_of rs-calib || true)"
	if [[ -n "$socket" ]]; then
		break
	fi
	sleep 0.2
done
if [[ -z "$socket" ]]; then
	rs_snap calib pane.txt
	echo "preflight: rs-calib did not register an inbox socket" >&2
	exit 1
fi
# A failing status is recorded as one observation and does not stop the run,
# so the frame and the reply below still gather the authentication baseline.
status_note=""
if ! "$R/run" rsprobe status >"$OUT/status.txt"; then
	status_note="rsprobe status failed"
elif ! grep -q -- 'rs-calib' "$OUT/status.txt"; then
	status_note="rsprobe status does not list rs-calib"
fi
if [[ -n "$status_note" ]]; then
	rs_obs "$status_note; continuing so the authentication baseline is still gathered"
fi

# 4. Write one frame directly to the session's inbox.
"$R/run" rsprobe frame --socket "$socket" --from-name calib@x --body calib-body-1

# 5. The session answers on screen; keep the pane and the matching record.
rs_wait_pane calib '\bok\b' 60
rs_snap calib pane.txt
rs_excerpt calib-body-1 calib-record.jsonl
