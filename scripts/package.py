#!/usr/bin/env python3
"""Build WebDeviceCable downloads locally. Never publishes or installs them.
Preview builds need no publisher credentials. --community creates manual-update
test downloads with ad-hoc macOS signing. --release requires trusted publisher
certificates. Finish a combined release with release.mjs.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys
import tarfile
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / 'dist'
VERSION = json.loads((ROOT/'package.json').read_text())['version']
TARGETS = ['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64', 'windows/amd64', 'windows/arm64']
SPARKLE_VERSION = '2.10.1'
SPARKLE_SHA256 = '1a856d2a9edb8e628de88121d7be04cf0e2167f8641650b78fca00b14244cf27'

def run(argv, env=None):
    subprocess.run([str(v) for v in argv], cwd=ROOT, env=env, check=True)

def require(name):
    value = os.environ.get(name)
    if not value:
        raise SystemExit(f'{name} is required for an official release; use a preview build without --release.')
    return value

def sparkle():
    folder = ROOT/'.cache'/f'sparkle-{SPARKLE_VERSION}'
    folder.mkdir(parents=True, exist_ok=True)
    archive = folder/f'Sparkle-{SPARKLE_VERSION}.tar.xz'
    if not archive.exists():
        urllib.request.urlretrieve(f'https://github.com/sparkle-project/Sparkle/releases/download/{SPARKLE_VERSION}/{archive.name}', archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != SPARKLE_SHA256:
        raise SystemExit('Sparkle checksum verification failed.')
    # Re-extract verified bytes before signing; an expanded cache is not a trust anchor.
    for name in ['Sparkle.framework','bin']:
        existing=folder/name
        if existing.is_symlink(): raise SystemExit('Unexpected link in the Sparkle build cache.')
        if existing.exists(): shutil.rmtree(existing)
    with tarfile.open(archive) as source:
        source.extractall(folder, filter='data')
    return folder

def notarize(path):
    run(['xcrun','notarytool','submit',path,'--keychain-profile',require('WDC_NOTARY_PROFILE'),'--wait'])

def zip_stage(stage, destination, mac):
    destination.unlink(missing_ok=True)
    if mac:
        # ditto preserves framework links, executable modes and notarization tickets.
        run(['ditto','-c','-k','--keepParent',stage,destination])
    else:
        with zipfile.ZipFile(destination,'w',zipfile.ZIP_DEFLATED) as archive:
            for path in stage.rglob('*'):
                if path.is_file(): archive.write(path,str(Path(stage.name)/path.relative_to(stage)))

def checksums(paths):
    return ''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in sorted(paths))

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target',action='append',choices=TARGETS)
    mode=parser.add_mutually_exclusive_group()
    mode.add_argument('--release',action='store_true',help='Require publisher signing and update trust configuration')
    mode.add_argument('--community',action='store_true',help='Manual updates, ad-hoc macOS signing, unsigned Windows binaries')
    parser.add_argument('--out-dir',type=Path,default=OUT,help='Keep release artifacts in a separate directory')
    args=parser.parse_args()
    targets=args.target or TARGETS
    out=args.out_dir.resolve()
    distribution='official' if args.release else 'community' if args.community else 'unsigned-preview'
    pub=''
    if args.community:
        pub=require('WDC_RELEASE_PUBLIC_KEY')
        if len(base64.b64decode(pub,validate=True)) != 32: raise SystemExit('WDC_RELEASE_PUBLIC_KEY must encode 32 bytes.')
    source=subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    if args.release:
        pub=require('WDC_RELEASE_PUBLIC_KEY')
        if len(base64.b64decode(pub,validate=True)) != 32: raise SystemExit('WDC_RELEASE_PUBLIC_KEY must encode 32 bytes.')
        if any(t.startswith('darwin/') for t in targets):
            for key in ['WDC_SIGN_IDENTITY','WDC_INSTALLER_IDENTITY','WDC_NOTARY_PROFILE','WDC_SPARKLE_PUBLIC_KEY']: require(key)
            if len(base64.b64decode(require('WDC_SPARKLE_PUBLIC_KEY'),validate=True)) != 32: raise SystemExit('WDC_SPARKLE_PUBLIC_KEY must encode 32 bytes.')
            if sys.platform!='darwin': raise SystemExit('Sign macOS packages on macOS.')
        if any(t.startswith('windows/') for t in targets):
            require('WDC_WINDOWS_CERTIFICATE')
            if sys.platform!='win32': raise SystemExit('Sign Windows packages on Windows.')
    out.mkdir(parents=True,exist_ok=True)
    cache=ROOT/'.cache';(cache/'tmp').mkdir(parents=True,exist_ok=True)
    run(['npm.cmd' if os.name=='nt' else 'npm','run','build:sdk'])
    run(['node','scripts/sdk-package.mjs'])
    produced=[]
    for target in targets:
        system,arch=target.split('/')
        stem=f'WebDeviceCable-{VERSION}-{system}-{arch}'
        stage=out/'staging'/stem
        if stage.exists(): shutil.rmtree(stage)
        stage.mkdir(parents=True)
        env=dict(os.environ,GOOS=system,GOARCH=arch,CGO_ENABLED='0',GOCACHE=str(cache/'go-build'),GOMODCACHE=str(cache/'go-mod'),GOPATH=str(cache/'gopath'),TMPDIR=str(cache/'tmp'))
        binary=stage/('WebDeviceCable.exe' if system=='windows' else 'WebDeviceCable')
        base=[os.environ.get('BRIDGE_GO','go'),'build','-trimpath']
        flags=f'-s -w -X main.version={VERSION}'
        if args.release: flags+=f' -X main.official=true -X main.releasePublicKey={require("WDC_RELEASE_PUBLIC_KEY")}'
        if args.community: flags+=f' -X main.community=true'
        run(base+['-ldflags='+flags,'-o',binary,'./cmd/webdevicecable'],env)
        for name in ['LICENSE','THIRD_PARTY_NOTICES.md','README.md','PRIVACY.md','CONTRIBUTING.md','SECURITY.md','INSTALL.md']:
            if (ROOT/name).exists(): shutil.copy(ROOT/name,stage)
        shutil.copytree(OUT/'sdk',stage/'sdk')
        shutil.copytree(ROOT/'examples',stage/'examples',ignore=shutil.ignore_patterns('__pycache__'))
        example=stage/'examples'/'server.mjs';example.write_text(example.read_text().replace('../internal/bridge/web/','../sdk/'))
        shutil.copytree(ROOT/'docs',stage/'docs')
        shutil.copy(ROOT/'examples'/'config.yaml',stage/'config.example.yaml')
        (stage/'build-manifest.json').write_text(json.dumps(dict(name='WebDeviceCable',version=VERSION,target=target,distribution=distribution,protocolVersion=2,stateVersion=1,sourceCommit=source,automaticUpdates=bool(args.release),releasePublicKey=pub),indent=2)+'\n')
        if system=='darwin':
            app=stage/'WebDeviceCable.app'/'Contents'
            (app/'MacOS').mkdir(parents=True);(app/'Resources').mkdir()
            shutil.copy(binary,app/'Resources'/'WebDeviceCable')
            shutil.copy(ROOT/'internal'/'bridge'/'web'/'logo.svg',app/'Resources'/'status-icon.svg')
            swift=['xcrun','swiftc','-parse-as-library','-O','-target',('arm64' if arch=='arm64' else 'x86_64')+'-apple-macos12.0','scripts/Launcher.swift','scripts/UpdateDriver.swift','-module-cache-path',str(cache/'swift-modules')]
            info=dict(CFBundleIdentifier='org.webdevicecable.local',CFBundleName='WebDeviceCable',CFBundleDisplayName='WebDeviceCable',CFBundleExecutable='wdc-launcher',CFBundleURLTypes=[dict(CFBundleURLName='WebDeviceCable connection',CFBundleURLSchemes=['webdevicecable'])],CFBundlePackageType='APPL',CFBundleShortVersionString=VERSION,CFBundleVersion=VERSION,LSMinimumSystemVersion='12.0',LSUIElement=True)
            if args.release:
                framework=sparkle()
                (app/'Frameworks').mkdir();shutil.copytree(framework/'Sparkle.framework',app/'Frameworks'/'Sparkle.framework',symlinks=True)
                swift+=['-F',str(framework),'-framework','Sparkle','-Xlinker','-rpath','-Xlinker','@executable_path/../Frameworks']
                info.update(SUFeedURL=f'https://github.com/thomasmikava/WebDeviceCable/releases/latest/download/appcast-darwin-{arch}.xml',SUPublicEDKey=require('WDC_SPARKLE_PUBLIC_KEY'),SUEnableAutomaticChecks=False,SUAutomaticallyUpdate=False,SUSendProfileInfo=False,SURequireSignedFeed=True,SUVerifyUpdateBeforeExtraction=True)
            run(swift+['-o',str(app/'MacOS'/'wdc-launcher')])
            with (app/'Info.plist').open('wb') as handle: plistlib.dump(info,handle)
            if args.release:
                run(['sh','scripts/sign-macos.sh',app.parent])
                run(['codesign','--force','--options','runtime','--timestamp','--sign',require('WDC_SIGN_IDENTITY'),binary])
            elif args.community:
                # Ad-hoc signatures need no name, email, certificate or Apple account.
                for executable in [binary,app/'Resources'/'WebDeviceCable',app/'MacOS'/'wdc-launcher']:
                    run(['codesign','--force','--timestamp=none','--sign','-',executable])
                run(['codesign','--force','--timestamp=none','--sign','-',app.parent])
                run(['codesign','--verify','--deep','--strict',app.parent])
            archive=out/f'{stem}.zip';zip_stage(stage,archive,True)
            if args.release:
                notarize(archive);run(['xcrun','stapler','staple',app.parent]);run(['xcrun','stapler','validate',app.parent]);run(['spctl','--assess','--type','execute','--verbose=2',app.parent]);zip_stage(stage,archive,True)
            produced.append(archive)
            if shutil.which('pkgbuild'):
                payload=out/'staging'/f'{stem}-payload'
                if payload.exists(): shutil.rmtree(payload)
                (payload/'Applications').mkdir(parents=True);shutil.copytree(app.parent,payload/'Applications'/'WebDeviceCable.app',symlinks=True)
                pkg=out/f'{stem}.pkg';pkg.unlink(missing_ok=True)
                command=['pkgbuild','--root',str(payload),'--identifier',f'org.webdevicecable.local.{arch}','--version',VERSION,'--install-location','/']
                if args.release: command+=['--sign',require('WDC_INSTALLER_IDENTITY')]
                run(command+[str(pkg)])
                if args.release:
                    notarize(pkg);run(['xcrun','stapler','staple',pkg]);run(['xcrun','stapler','validate',pkg]);run(['pkgutil','--check-signature',pkg])
                produced.append(pkg)
        elif system=='windows':
            run(base+['-ldflags='+flags+' -H windowsgui -X main.gui=true','-o',stage/'WebDeviceCable Launcher.exe','./cmd/webdevicecable'],env)
            for name in ['Install.cmd','Install.ps1','Uninstall.cmd','Uninstall.ps1']: shutil.copy(ROOT/'scripts'/name,stage)
            if args.release:
                for name in ['WebDeviceCable.exe','WebDeviceCable Launcher.exe','Install.ps1','Uninstall.ps1']:
                    run(['powershell.exe','-NoProfile','-File','scripts/sign-windows.ps1','-Path',stage/name,'-CertificateThumbprint',require('WDC_WINDOWS_CERTIFICATE')])
            archive=out/f'{stem}.zip';zip_stage(stage,archive,False);produced.append(archive)
        else:
            shutil.copy(ROOT/'internal'/'bridge'/'web'/'logo.svg',stage/'webdevicecable.svg')
            for source,name in [('install-linux.sh','install.sh'),('uninstall-linux.sh','uninstall.sh')]:
                shutil.copy(ROOT/'scripts'/source,stage/name);(stage/name).chmod(0o755)
            archive=out/f'{stem}.tar.gz';archive.unlink(missing_ok=True)
            with tarfile.open(archive,'w:gz') as handle: handle.add(stage,arcname=stem)
            produced.append(archive)
        print(f'Built WebDeviceCable {target} ({distribution})',flush=True)
    (out/'SHA256SUMS').write_text(checksums(produced+([] if args.community else list(OUT.glob('webdevicecable-sdk-*.tgz')))))
    report=dict(name='WebDeviceCable',version=VERSION,targets=targets,distribution=distribution,sourceCommit=source,automaticUpdates=bool(args.release),releasePublicKey=pub,artifacts=[p.name for p in produced],toolchain=subprocess.check_output([os.environ.get('BRIDGE_GO','go'),'version'],text=True).strip(),validation='See docs/validation.md and docs/testing.md. Native Windows/Linux provider execution awaits tester feedback.')
    (out/'build-manifest.json').write_text(json.dumps(report,indent=2)+'\n')

if __name__=='__main__': main()
