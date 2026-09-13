#!/usr/bin/env bash
# Render gh-stories inside a real kitty window on a real X display and capture
# the display's actual pixels.
#
# Usage: capture.sh <output.png> <command...>
set -euo pipefail

OUT="${1:?output path}"; shift
COLS="${GHS_COLS:-100}"
ROWS="${GHS_ROWS:-32}"
SETTLE="${GHS_SETTLE:-6}"

Xvfb :99 -screen 0 1280x800x24 -nolisten tcp >/tmp/xvfb.log 2>&1 &
XVFB_PID=$!
trap 'kill "$XVFB_PID" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
    xdpyinfo -display :99 >/dev/null 2>&1 && break
    sleep 0.2
done
xdpyinfo -display :99 >/dev/null 2>&1 || { echo "Xvfb never came up"; cat /tmp/xvfb.log; exit 1; }

kitty --config NONE \
      -o font_family="DejaVu Sans Mono" \
      -o font_size=14 \
      -o background=#0d1117 \
      -o foreground=#f0f6fc \
      -o cursor_blink_interval=0 \
      -o remember_window_size=no \
      -o initial_window_width="${COLS}c" \
      -o initial_window_height="${ROWS}c" \
      -o confirm_os_window_close=0 \
      "$@" >/tmp/kitty.log 2>&1 &
KITTY_PID=$!

sleep "$SETTLE"

if ! kill -0 "$KITTY_PID" 2>/dev/null; then
    echo "kitty exited early:"; cat /tmp/kitty.log; exit 1
fi

import -display :99 -window root "$OUT"
# Trim the surrounding black desktop so the capture is the terminal itself.
convert "$OUT" -bordercolor black -border 1 -trim +repage "$OUT" 2>/dev/null || true
kill "$KITTY_PID" 2>/dev/null || true
echo "captured $OUT"
identify "$OUT"
