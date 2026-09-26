#!/bin/sh
# Builds ARFABIT from this folder and runs it, in place of any copy already
# running, for trying changes on a Mac.
#
#   ./macos-dev.sh
#
# A copy started by autostart is stopped until the next login, when it starts
# again as it was set up. Stopping this one (Ctrl-C) stops ARFABIT.

cd "$(dirname "$0")" || exit 1

# The copy autostart runs, then anything else still on ARFABIT's port.
launchctl bootout "gui/$(id -u)/com.arfabit.arfabit" 2>/dev/null
lsof -ti :7847 -sTCP:LISTEN | xargs kill 2>/dev/null

go build -o arfabit ./cmd/arfabit && exec ./arfabit
