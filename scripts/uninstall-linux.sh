#!/bin/sh
set -eu
bridge_prefix="${PREFIX:-$HOME/.local}"
bridge_binary="$bridge_prefix/bin/WebDeviceCable"
if [ -x "$bridge_binary" ]; then
  "$bridge_binary" desktop-stop
  "$bridge_binary" stop || true
fi
if [ -L "$bridge_prefix/bin/webdevicecable" ] && [ "$(readlink "$bridge_prefix/bin/webdevicecable")" = WebDeviceCable ]; then rm -f "$bridge_prefix/bin/webdevicecable"; fi
rm -f "$bridge_binary"
printf 'WebDeviceCable removed. Your private settings and connection history are retained.\n'

rm -f "${XDG_DATA_HOME:-$HOME/.local/share}/applications/webdevicecable.desktop"

rm -f "${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/scalable/apps/webdevicecable.svg"
