#!/usr/bin/env python3
"""Build Linux amd64 releases including the reproducible MPL Xray extension."""
import ast
import gzip
import hashlib
import shutil
import tarfile
from pathlib import Path

root = Path(__file__).resolve().parent.parent
version = (root / 'VERSION').read_text().strip()
archives = []
for edition in ['lite', 'pro']:
    name = 'guangyue-panel-' + edition + '-' + version + '-linux-amd64'
    out = root / 'build/releases'
    bundle = out / name
    if bundle.exists():
        shutil.rmtree(bundle)
    (bundle / 'bin').mkdir(parents=True)
    (bundle / 'EDITION').write_text(edition + '\n')
    for binary in ['guangyue-linux-amd64', 'hysteria-node-linux-amd64', 'xray-linux-amd64']:
        shutil.copyfile(root / 'build/bin' / binary, bundle / 'bin' / binary)
        (bundle / 'bin' / binary).chmod(0o755)
    ignore = shutil.ignore_patterns('__pycache__', '*.pyc', 'tests')
    for src, dest in [('frontend/dist', 'web'), ('deploy', 'deploy'), ('scripts', 'scripts'), ('docs', 'docs'), ('core-patches', 'core-patches'), ('examples', 'examples'), ('build/notices', 'licenses')]:
        shutil.copytree(root / src, bundle / dest, ignore=ignore)
    for file in ['VERSION', 'README.md', 'README_EN.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'CHANGELOG.md', 'SECURITY.md', 'CONTRIBUTING.md']:
        shutil.copyfile(root / file, bundle / file)
    # Both current and older updaters read this literal map before executing
    # installation. Pin the bundled build so an old upstream core is never reused.
    installer = bundle / 'deploy/install.py'
    code = installer.read_text()
    hashes = next(ast.literal_eval(n.value) for n in ast.parse(code).body if isinstance(n, ast.Assign) and any(isinstance(t, ast.Name) and t.id == 'UPSTREAM_HASHES' for t in n.targets))
    assert code.count(hashes['xray']) == 1
    installer.write_text(code.replace(hashes['xray'], hashlib.sha256((bundle / 'bin/xray-linux-amd64').read_bytes()).hexdigest()))
    # Ship all MPL-covered source files, including our modifications, and locked
    # build inputs alongside the binary. The patch and fixed upstream commit are
    # also included for independent reproduction.
    shutil.copyfile(root / 'build/xray-source.tar.gz', bundle / 'licenses/xray-source.tar.gz')
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
    archives.append(archive)
shutil.copyfile(root / 'build/notices/SBOM.spdx.json', out / 'SBOM.spdx.json')
(out / 'SHA256SUMS').write_text('\n'.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name for p in archives + [out / 'SBOM.spdx.json']) + '\n')
print('Packaged ' + ', '.join(a.name for a in archives) + '; Xray source and session extension included. Fetch Mihomo before installation.')
