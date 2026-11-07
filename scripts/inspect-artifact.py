#!/usr/bin/env python3
"""Read one bounded build manifest without installing or extracting a download."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
from threading import Timer
import zipfile

LIMIT = 16384


def read_manifest(artifact, member):
    if artifact.endswith('.exe'):
        sevenzip = os.environ.get('WDC_7ZIP') or shutil.which('7zz') or shutil.which('7z')
        if not sevenzip:
            candidate = Path(os.environ.get('ProgramFiles', ''))/'7-Zip'/'7z.exe'
            if candidate.is_file():
                sevenzip = str(candidate)
        if not sevenzip:
            raise ValueError('Install 7-Zip to verify Windows installers (macOS: brew install sevenzip).')
        # The installer is only read, never executed. Bound decompressed output.
        with subprocess.Popen([sevenzip, 'e', '-so', '-r', artifact, 'build-manifest.json'],
                              stdout=subprocess.PIPE, stderr=subprocess.DEVNULL) as process:
            timer = Timer(30, process.kill)
            timer.start()
            try:
                data = process.stdout.read(LIMIT + 1)
                if len(data) > LIMIT:
                    process.kill()
                    raise ValueError('Installer build manifest exceeds limits.')
                if process.wait() != 0 or not data:
                    raise ValueError('Could not read the installer build manifest.')
            finally:
                timer.cancel()
    elif artifact.endswith('.zip'):
        with zipfile.ZipFile(artifact) as archive:
            matches = [info for info in archive.infolist() if info.filename == member]
            if len(matches) != 1 or matches[0].file_size > LIMIT:
                raise ValueError('Missing, duplicate or oversized archive build manifest.')
            data = archive.read(matches[0])
    else:
        with tarfile.open(artifact) as archive:
            matches = [info for info in archive if info.name == member]
            if len(matches) != 1 or not matches[0].isfile() or matches[0].size > LIMIT:
                raise ValueError('Missing, duplicate or oversized archive build manifest.')
            data = archive.extractfile(matches[0]).read(LIMIT + 1)
    return json.loads(data)


if __name__ == '__main__':
    try:
        print(json.dumps(read_manifest(*sys.argv[1:])))
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit(str(error))
