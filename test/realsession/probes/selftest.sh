#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

rs_tmux_start
rs_window echo sh -c 'echo selftest-ok; sleep 30'
rs_wait_pane echo 'selftest-ok' 5
rs_snap echo pane.txt
rs_obs "env check passed, tmux works"
