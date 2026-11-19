#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# permissions.sh answers whether Claude Code asks the user before reading the
# Broker configuration and before changing it, and whether generating a pairing
# key is governed by the same write permission as set_config.
#
# Each session runs with a different permission allowlist supplied through
# --settings, so the shared R/cfg/settings.json is never modified and the
# probe stays within $OUT.

# The permission dialog of the pinned Claude Code version offers
# "Do you want to proceed?", "Yes, and don't ask again for ..." and
# "No, and tell Claude what to do differently"; Escape chooses the last one.
dialog_pattern='Do you want to proceed\?|Do you want to allow .*to use|Yes, and don.t ask again|No, and tell Claude'

# PERM_DIALOGS counts the permission dialogs the probe denied.
PERM_DIALOGS=0

# perm_write_settings writes a permissions override for one session.
perm_write_settings() {
	local file="$1" tool="$2"
	cat >"$file" <<EOF
{
  "permissions": {
    "allow": ["$tool"]
  }
}
EOF
}

# perm_wait_ready waits until the session appears in the registry and its
# input box is available. Readiness itself is the shared predicate, so the
# prompt that the permission dialog will cover is never mistaken for it.
perm_wait_ready() {
	local window="$1" seconds="$2"
	local deadline=$((SECONDS + seconds))
	while ((SECONDS < deadline)); do
		if rs_socket_of "$window" >/dev/null 2>&1; then
			if rs_wait_ready "$window" "$((deadline - SECONDS))"; then
				rs_obs_mode "$window"
				return 0
			fi
			return 1
		fi
		sleep 0.2
	done
	rs_snap "$window" "ready-$window-timeout.txt" || true
	return 1
}

# perm_send submits a model prompt through the shared helper.
perm_send() {
	local window="$1" text="$2" needle="${3:-}"
	rs_send_prompt "$window" "$text" "$needle" 60
}

# perm_run_turn follows one turn: every permission dialog is captured and
# denied, and the function returns once the turn is over or the bound elapses.
# A dialog is recognised on screen, so each denial is followed by a pause that
# lets the dialog leave the visible pane before the next poll.
perm_run_turn() {
	local window="$1" label="$2"
	local deadline=$((SECONDS + 180))
	local started=0 sent_at=$SECONDS now pane
	while ((SECONDS < deadline)); do
		now=$SECONDS
		pane="$(tmux -L "$TMUX_SERVER" capture-pane -p -t "$window" 2>/dev/null || true)"
		if printf '%s\n' "$pane" | grep -qE -- "$dialog_pattern"; then
			PERM_DIALOGS=$((PERM_DIALOGS + 1))
			printf '%s\n' "$pane" >"$OUT/$window-dialog-$PERM_DIALOGS.txt"
			rs_keys "$window" Escape
			started=1
			sleep 3
			continue
		fi
		if printf '%s\n' "$pane" | grep -qF 'esc to interrupt'; then
			started=1
			sleep 0.3
			continue
		fi
		if rs_ready_criterion "$pane" >/dev/null; then
			if ((started == 1 || now - sent_at >= 30)); then
				break
			fi
		fi
		sleep 0.3
	done
	rs_snap "$window" "$label"
}

# perm_close ends a session and removes its window.
perm_close() {
	local window="$1"
	# /exit is a slash command, not a model prompt, so it bypasses the helper.
	rs_keys_literal "$window" "/exit"
	rs_keys "$window" Enter
	sleep 2
	tmux -L "$TMUX_SERVER" kill-window -t "$window" 2>/dev/null || true
}

rs_tmux_start

# The shared settings file must survive the probe unchanged; record both hashes
# so the record can show it was never edited.
sha256sum "$R/cfg/settings.json" >"$OUT/settings-before.sha256"

# Case one: reading the configuration is allowlisted, changing it is not.
perm_write_settings "$OUT/perm-a.settings.json" mcp__peer__get_config
rs_window perm-a claude --model haiku -n perm-a --permission-mode default --settings "$OUT/perm-a.settings.json"
perm_wait_ready perm-a 30
rs_snap perm-a perm-a-start.txt
perm_send perm-a 'Call mcp__peer__get_config for key broker.alias and show the result. Then call mcp__peer__set_config with key broker.alias and value hostB2.'
perm_run_turn perm-a perm-a-final.txt
rs_obs "perm-a finished with $PERM_DIALOGS dialog(s); allowlist was mcp__peer__get_config"
perm_close perm-a

# Case two: generating a pairing key is allowlisted, changing the configuration
# is still not.
before=$PERM_DIALOGS
perm_write_settings "$OUT/perm-b.settings.json" mcp__peer__create_pairing_key
rs_window perm-b claude --model haiku -n perm-b --permission-mode default --settings "$OUT/perm-b.settings.json"
perm_wait_ready perm-b 30
rs_snap perm-b perm-b-start.txt
perm_send perm-b 'Call mcp__peer__create_pairing_key and show the result. Then call mcp__peer__set_config with key broker.alias and value hostB2.'
perm_run_turn perm-b perm-b-final.txt
rs_obs "perm-b finished with $((PERM_DIALOGS - before)) dialog(s); allowlist was mcp__peer__create_pairing_key"

# The shared settings file was never edited.
sha256sum "$R/cfg/settings.json" >"$OUT/settings-after.sha256"
if cmp -s "$OUT/settings-before.sha256" "$OUT/settings-after.sha256"; then
	rs_obs "shared settings.json unchanged: hashes match"
else
	rs_obs "shared settings.json CHANGED: compare settings-before.sha256 and settings-after.sha256"
fi

# The Broker data directory is the account's real home, not $R/home.
account_home="$(getent passwd "$(id -un)" | cut -d: -f6)"
if [[ -z "$account_home" ]]; then
	account_home="$HOME"
fi
config_file="$account_home/.agent-peer/config.yaml"
{
	printf 'config_file=%s\n' "$config_file"
	if [[ -f "$config_file" ]]; then
		grep -n -E '^[[:space:]]*alias[[:space:]]*:' "$config_file" || true
	else
		printf 'missing\n'
	fi
} >"$OUT/config-alias.txt"
if [[ -f "$config_file" ]] && ! grep -qF 'hostB2' "$config_file"; then
	rs_obs "alias unchanged: $config_file contains no hostB2"
else
	rs_obs "alias check failed: inspect config-alias.txt"
fi

if ! "$R/run" rsprobe status >"$OUT/status.txt" 2>"$OUT/status.err"; then
	rs_obs "rsprobe status failed; see status.err"
fi
perm_close perm-b
rs_obs "permissions probe finished with $PERM_DIALOGS dialog(s) denied in total"

# A denied dialog is evidence only if the prompt that raised it started a model
# turn. The perm-a prompt is the first one typed, so its text is the marker.
rs_require_model_turn 'Call mcp__peer__get_config for key broker.alias' 1
