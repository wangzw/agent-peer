#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# The preflight probe checks that the runner isolation holds, that a session
# can authenticate, and where a session record keeps the name of the sender it
# identified. It follows the plan's "environment preflight and authentication
# baseline" item.

# 1. The peer server is configured by the workflow's prepare step.
if ! "$R/run" claude mcp list >"$OUT/mcp-list.txt"; then
	echo "preflight: claude mcp list failed" >&2
fi

# 2. Start the calibration session with the low-call-count prompt. The
# permission mode is pinned to default so auto mode cannot answer a prompt the
# probe means to observe.
rs_tmux_start
rs_window calib claude --model haiku -n rs-calib \
	--permission-mode default \
	--append-system-prompt "When a cross-session message arrives, reply with the single word ok and do nothing else. Do not call any tool unless the user explicitly asks." \
	--debug-file "$R/debug/calib.debug"
if ! rs_wait_ready calib 30; then
	echo "preflight: rs-calib did not reach the input prompt" >&2
	exit 1
fi
rs_obs_mode calib

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

# 6. Authentication baseline: one real model call. The session must answer the
# prompt with the agreed string, and the environment's model-call counter must
# end above zero. A TUI that merely started proves nothing about the token, so
# either half missing fails the probe. The prompt goes through rs_send_prompt;
# the answer is evidence only when an assistant message in the transcript
# carries it, and the pane is not accepted as a substitute.
preflight_reply='rs-preflight-ok'
if ! rs_send_prompt calib "Reply with exactly $preflight_reply and nothing else."; then
	echo "preflight: rs-calib did not accept the reply prompt" >&2
	exit 1
fi
reply_status=0
rs_wait_reply "$preflight_reply" calib-reply-record.jsonl 120 || reply_status=$?
rs_snap calib pane-reply.txt
if ((reply_status != 0)); then
	echo "preflight: rs-calib did not reply with $preflight_reply" >&2
	exit 1
fi
rs_snap calib pane-reply.txt
rs_excerpt "$preflight_reply" calib-reply-record.jsonl
"$R/run" rsprobe env usage --root "$R" >"$OUT/usage-after-reply.txt" 2>&1 || true
total="$(sed -n 's/^total //p' "$OUT/usage-after-reply.txt" | tail -n 1)"
if [[ ! "$total" =~ ^[0-9]+$ ]] || ((total <= 0)); then
	echo "preflight: model-call count is ${total:-unreadable}, want above zero" >&2
	exit 1
fi
rs_obs "authentication baseline: rs-calib replied $preflight_reply and env usage total=$total"

# The reply must also come from a submitted prompt: a user record has to carry
# the marker the prompt asked about, and the usage counter has to be positive.
rs_require_model_turn "$preflight_reply" 1
