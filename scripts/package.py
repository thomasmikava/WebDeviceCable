#!/usr/bin/env python3
"""Build self-contained preview packages with Go, Python and the built SDK.
macOS application bundles additionally require the Xcode Swift command-line tools.
No upload, signing service, installation, git operation, or provider invocation.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'dist'
VERSION = '0.1.0'
TARGETS = ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64']

def run(argv, env=None):
    subprocess.run(argv, cwd=ROOT, env=env, check=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', action='append', choices=TARGETS)
    args = parser.parse_args()
    targets = args.target or TARGETS
    OUT.mkdir(exist_ok=True)
    cache = ROOT / '.cache'
    (cache / 'tmp').mkdir(parents=True, exist_ok=True)
    run(['node', 'scripts/sdk-package.mjs'])
    for target in targets:
        system, arch = target.split('/')
        stem = f'webdevicecable-{VERSION}-{system}-{arch}'
        stage = OUT / 'staging' / stem
        if stage.exists():
            shutil.rmtree(stage)
        stage.mkdir(parents=True)
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0',
                   GOCACHE=str(cache/'go-build'), GOMODCACHE=str(cache/'go-mod'),
                   GOPATH=str(cache/'gopath'), TMPDIR=str(cache/'tmp'))
        binary = stage / ('webdevicecable.exe' if system == 'windows' else 'webdevicecable')
        base = [os.environ.get('BRIDGE_GO', 'go'), 'build', '-trimpath']
        run(base + ['-ldflags=-s -w', '-o', str(binary), './cmd/webdevicecable'], env)
        shutil.copy(ROOT/'LICENSE', stage)
        shutil.copy(ROOT/'THIRD_PARTY_NOTICES.md', stage)
        shutil.copy(ROOT/'README.md', stage)
        shutil.copytree(OUT/'sdk', stage/'sdk')
        shutil.copytree(ROOT/'examples', stage/'examples', ignore=shutil.ignore_patterns('__pycache__'))
        example_server = stage/'examples'/'server.mjs'
        example_server.write_text(example_server.read_text().replace('../internal/bridge/web/', '../sdk/'))
        shutil.copytree(ROOT/'docs', stage/'docs', dirs_exist_ok=True)
        shutil.copy(ROOT/'examples'/'config.yaml', stage/'config.example.yaml')
        if system == 'darwin':
            app = stage / 'WebDeviceCable.app' / 'Contents'
            (app/'MacOS').mkdir(parents=True)
            (app/'Resources').mkdir(parents=True)
            shutil.copy(binary, app/'Resources'/'webdevicecable')
            shutil.copy(ROOT/'internal'/'bridge'/'web'/'logo.svg', app/'Resources'/'status-icon.svg')
            swift_target = ('arm64' if arch == 'arm64' else 'x86_64') + '-apple-macos12.0'
            run(['xcrun','swiftc','-O','-target',swift_target,'scripts/Launcher.swift','-module-cache-path',str(cache/'swift-modules'),'-o',str(app/'MacOS'/'wdc-launcher')])
            with (app/'Info.plist').open('wb') as handle:
                plistlib.dump(dict(CFBundleIdentifier='org.webdevicecable.local', CFBundleName='WebDeviceCable',
                                  CFBundleExecutable='wdc-launcher', CFBundleURLTypes=[dict(CFBundleURLName='WebDeviceCable connection', CFBundleURLSchemes=['webdevicecable'])], CFBundlePackageType='APPL',
                                  CFBundleShortVersionString=VERSION, CFBundleVersion='1',
                                  LSMinimumSystemVersion='12.0', LSUIElement=True), handle)
            if shutil.which('pkgbuild'):
                payload = OUT/'staging'/f'{stem}-payload'
                (payload/'Applications').mkdir(parents=True, exist_ok=True)
                shutil.copytree(app.parent, payload/'Applications'/'WebDeviceCable.app', dirs_exist_ok=True)
                run(['pkgbuild', '--root', str(payload), '--identifier', f'org.webdevicecable.local.{arch}',
                     '--version', VERSION, '--install-location', '/', str(OUT/f'{stem}.pkg')])
        elif system == 'windows':
            run(base + ['-ldflags=-s -w -H windowsgui -X main.gui=true', '-o', str(stage/'WebDeviceCable Launcher.exe'), './cmd/webdevicecable'], env)
            for filename in ['Install.cmd', 'Install.ps1', 'Uninstall.cmd', 'Uninstall.ps1']:
                shutil.copy(ROOT/'scripts'/filename, stage)
        else:
            shutil.copy(ROOT/'internal'/'bridge'/'web'/'logo.svg',stage/'webdevicecable.svg')
            shutil.copy(ROOT/'scripts'/'install-linux.sh', stage/'install.sh')
            (stage/'install.sh').chmod(0o755)
            shutil.copy(ROOT/'scripts'/'uninstall-linux.sh', stage/'uninstall.sh')
            (stage/'uninstall.sh').chmod(0o755)
        if system == 'linux':
            with tarfile.open(OUT/f'{stem}.tar.gz', 'w:gz') as archive:
                archive.add(stage, arcname=stem)
        else:
            with zipfile.ZipFile(OUT/f'{stem}.zip', 'w', zipfile.ZIP_DEFLATED) as archive:
                for path in stage.rglob('*'):
                    if path.is_file():
                        archive.write(path, str(Path(stem)/path.relative_to(stage)))
        print(f'Built {target}', flush=True)
    manifest = dict(version=VERSION, protocolVersion=2, distribution='unsigned-preview',
                    targets=targets, toolchain=subprocess.check_output([os.environ.get('BRIDGE_GO', 'go'), 'version'], cwd=ROOT, text=True).strip(),
                    dependencies=[line.strip() for line in (ROOT/'go.mod').read_text().splitlines() if line.startswith('\t')],
                    validation='See docs/validation.md; platform compilation alone does not establish native execution.')
    (OUT/'build-manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    files = sorted(path for path in OUT.glob(f'webdevicecable-{VERSION}-*') if path.suffix in ['.zip', '.gz', '.pkg', '.tgz'])
    files += sorted(OUT.glob('webdevicecable-sdk-*.tgz'))
    files.append(OUT/'build-manifest.json')
    (OUT/'SHA256SUMS').write_text(''.join(f'{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n' for path in files))

if __name__ == '__main__':
    main()
