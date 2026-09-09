#!/usr/bin/env python3
"""Build a source-clean Linux amd64 release; excludes downloaded GPL/MPL binaries."""
import gzip
import hashlib
import shutil
import tarfile
from pathlib import Path

root = Path(__file__).resolve().parent.parent
version = (root / 'VERSION').read_text().strip()
name = 'guangyue-panel-' + version + '-linux-amd64'
out = root / 'build/releases'
bundle = out / name
if bundle.exists():
    shutil.rmtree(bundle)
(bundle / 'bin').mkdir(parents=True)
for binary in ['guangyue-linux-amd64', 'hysteria-node-linux-amd64']:
    shutil.copyfile(root / 'build/bin' / binary, bundle / 'bin' / binary)
    (bundle / 'bin' / binary).chmod(0o755)
ignore = shutil.ignore_patterns('__pycache__', '*.pyc', 'tests')
for src, dest in [('frontend/dist', 'web'), ('deploy', 'deploy'), ('scripts', 'scripts'), ('docs', 'docs'), ('core-patches', 'core-patches'), ('examples', 'examples'), ('build/notices', 'licenses')]:
    shutil.copytree(root / src, bundle / dest, ignore=ignore)
for file in ['VERSION', 'README.md', 'README_EN.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md', 'SECURITY.md', 'CONTRIBUTING.md']:
    shutil.copyfile(root / file, bundle / file)
shutil.copyfile(root / 'LICENSE', bundle / 'licenses/PANEL-LICENSE.txt')
for license_file in (root / 'licenses').iterdir():
    if license_file.is_file(): shutil.copyfile(license_file, bundle / 'licenses' / license_file.name)
for path in (root / 'core-patches').glob('*LICENSE*'):
    shutil.copyfile(path, bundle / 'licenses' / path.name)
manifest = []
for path in sorted(bundle.rglob('*')):
    if path.is_file():
        manifest.append(hashlib.sha256(path.read_bytes()).hexdigest() + '  ' + path.relative_to(bundle).as_posix())
(bundle / 'SHA256SUMS').write_text('\n'.join(manifest) + '\n')
archive = out / (name + '.tar.gz')
def normalize(info):
    info.uid = info.gid = 0
    info.uname = info.gname = 'root'
    info.mtime = 0
    return info
with archive.open('wb') as output:
    with gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as tar:
            tar.add(bundle, arcname=name, filter=normalize)
shutil.copyfile(root / 'build/notices/SBOM.spdx.json', out / 'SBOM.spdx.json')
(out / 'SHA256SUMS').write_text('\n'.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name for p in [archive, out / 'SBOM.spdx.json']) + '\n')
print('Packaged ' + archive.name + '; Xray and Mihomo must be fetched from upstream before installation.')
