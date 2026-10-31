#!/bin/sh
# Local release signing only. Notarization and final packaging belong to package.py.
set -eu
: "${WDC_SIGN_IDENTITY:?Set the Developer ID Application identity}"
bridge_app="${1:?Usage: sign-macos.sh /path/to/WebDeviceCable.app}"
python3 - "$bridge_app" "$WDC_SIGN_IDENTITY" <<'PY'
from pathlib import Path
import subprocess,sys
app=Path(sys.argv[1]);identity=sys.argv[2]
def sign(path):
    subprocess.run(['codesign','--force','--options','runtime','--timestamp','--preserve-metadata=entitlements','--sign',identity,str(path)],check=True)
magic={b'\xcf\xfa\xed\xfe',b'\xce\xfa\xed\xfe',b'\xfe\xed\xfa\xcf',b'\xca\xfe\xba\xbe',b'\xbe\xba\xfe\xca',b'\xca\xfe\xba\xbf'}
for path in sorted(app.rglob('*')):
    if path.is_file() and not path.is_symlink():
        with path.open('rb') as handle: header=handle.read(4)
        if header in magic: sign(path)
for path in sorted((p for p in app.rglob('*') if p.is_dir() and not p.is_symlink() and p.suffix in ['.app','.xpc','.framework']),key=lambda p:len(p.parts),reverse=True): sign(path)
sign(app)
subprocess.run(['codesign','--verify','--deep','--strict','--verbose=2',str(app)],check=True)
PY
