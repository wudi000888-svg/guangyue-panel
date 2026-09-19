#!/usr/bin/env python3
"""Increment the patch version and update every release-checked version file."""
import argparse
import json
import re
from pathlib import Path

root = Path(__file__).resolve().parent.parent
version_path = root / "VERSION"
current = version_path.read_text().strip()
match = re.fullmatch(r"(\d+)\.(\d+)\.(\d+)", current)
if not match:
    raise SystemExit(f"invalid VERSION: {current!r}")
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--version", help="Explicit newer semantic version, for a feature release")
args = parser.parse_args()
next_version = args.version or f"{match.group(1)}.{match.group(2)}.{int(match.group(3)) + 1}"
if not re.fullmatch(r"\d+\.\d+\.\d+", next_version) or tuple(map(int, next_version.split('.'))) <= tuple(map(int, current.split('.'))):
    raise SystemExit("release version must be newer than " + current)
version_path.write_text(next_version + "\n")

config = root / "backend/internal/controlplane/config.go"
config_text = config.read_text()
config_text, count = re.subn(
    r'(const version = ")\d+\.\d+\.\d+("\s*)',
    rf'\g<1>{next_version}\g<2>',
    config_text,
    count=1,
)
if count != 1:
    raise SystemExit("could not update Go version constant")
config.write_text(config_text)

for name in ("frontend/package.json", "frontend/package-lock.json"):
    path = root / name
    data = json.loads(path.read_text())
    data["version"] = next_version
    if name.endswith("package-lock.json"):
        data["packages"][""]["version"] = next_version
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")

readme = root / "README.md"
readme_text, count = re.subn(
    r"(shields\.io/badge/version-)\d+\.\d+\.\d+(-2563eb)",
    rf"\g<1>{next_version}\g<2>",
    readme.read_text(),
    count=1,
)
if count == 1:
    readme.write_text(readme_text)

changelog = root / "CHANGELOG.md"
old = changelog.read_text()
entry = f"# 变更记录\n\n## {next_version} — 自动构建发布\n\n- 由主分支提交自动生成；安装包、校验清单和 SBOM 随 Release 提供。\n\n"
if not old.startswith("# 变更记录\n"):
    raise SystemExit("unexpected CHANGELOG format")
changelog.write_text(entry + old[len("# 变更记录\n\n"):])
print(next_version)
