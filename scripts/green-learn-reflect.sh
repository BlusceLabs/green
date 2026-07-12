#!/usr/bin/env bash
# green-learn-reflect.sh — sessionEnd hook that feeds a finished session into
# the self-improving learning loop. green's hook dispatcher writes the event
# payload (JSON) to this script's stdin; we pull out sessionId and run
# `green learn reflect` so memory, the user profile, and skills are updated
# automatically after each session.
set -euo pipefail

payload="$(cat)"

# Extract the sessionId from the JSON payload. Tolerate both "sessionId" and
# "sessionID" spellings and missing values.
session_id="$(printf '%s' "$payload" | grep -o '"sessionId"[^,}]*' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)".*/\1/')"
if [ -z "$session_id" ]; then
  session_id="$(printf '%s' "$payload" | grep -o '"sessionID"[^,}]*' | head -1 | sed -E 's/.*:[[:space:]]*"([^"]+)".*/\1/')"
fi
if [ -z "$session_id" ]; then
  exit 0
fi

# Best-effort: reflect, but never fail the session over a learning hiccup.
green learn reflect "$session_id" >/dev/null 2>&1 || true
