#!/bin/sh
set -eu
bridge_source=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bridge_prefix="${PREFIX:-$HOME/.local}"
mkdir -p "$bridge_prefix/bin"
if [ -x "$bridge_prefix/bin/WebDeviceCable" ] || [ -x "$bridge_prefix/bin/webdevicecable" ]; then
	"$bridge_source/WebDeviceCable" desktop-stop
  "$bridge_source/WebDeviceCable" stop --if-running
fi
bridge_stage=$(mktemp "$bridge_prefix/bin/.webdevicecable-XXXXXX")
trap 'rm -f "$bridge_stage"' EXIT HUP INT TERM
install -m 755 "$bridge_source/WebDeviceCable" "$bridge_stage"
mv -f "$bridge_stage" "$bridge_prefix/bin/WebDeviceCable"
# Preserve the previous CLI spelling as an alias, while desktop names use WebDeviceCable.
ln -sf WebDeviceCable "$bridge_prefix/bin/webdevicecable"
printf 'Installed to %s/bin/WebDeviceCable\nRun: %s/bin/WebDeviceCable start\n' "$bridge_prefix" "$bridge_prefix"

bridge_apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$bridge_apps"
# Desktop Exec is not shell syntax: escape its quoted argument, including field codes.
bridge_exec=$(printf '%s' "$bridge_prefix/bin/WebDeviceCable" | sed 's/\\/\\\\/g; s/"/\\"/g; s/%/%%/g; s/`/\\`/g; s/\$/\\$/g')
cat > "$bridge_apps/webdevicecable.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=WebDeviceCable
Exec="$bridge_exec" desktop %u
Terminal=false
NoDisplay=false
Icon=webdevicecable
Categories=Utility;
MimeType=x-scheme-handler/webdevicecable;
EOF
bridge_icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/scalable/apps"
mkdir -p "$bridge_icons"
install -m 644 "$bridge_source/webdevicecable.svg" "$bridge_icons/webdevicecable.svg"
if command -v update-desktop-database >/dev/null 2>&1; then update-desktop-database "$bridge_apps"; fi
if command -v xdg-mime >/dev/null 2>&1; then xdg-mime default webdevicecable.desktop x-scheme-handler/webdevicecable || printf 'Open WebDeviceCable from your application menu to finish protocol registration.\n' >&2; fi
