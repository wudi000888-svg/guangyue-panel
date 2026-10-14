#!/usr/bin/env bash
# Publish only after all eight assets have been downloaded and verified again.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
tag=${1:?version tag required}
test "$tag" = "v$(cat VERSION)"
version=${tag#v}
assets=("build/releases/guangyue-panel-lite-$version-linux-amd64.tar.gz" "build/releases/guangyue-panel-pro-$version-linux-amd64.tar.gz" "build/releases/guangyue-installer-$version.tar.gz" "build/releases/guangyue-panel-docker-$version-linux-amd64.tar.gz" build/releases/install.sh build/releases/DOCKER_IMAGES.json build/releases/SHA256SUMS build/releases/SBOM.spdx.json)
for file in "${assets[@]}"; do test -s "$file"; done
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
python3 - "$version" "$tmp/notes.md" <<'PY'
from pathlib import Path
import sys
version, destination = sys.argv[1:]
entry = Path('CHANGELOG.md').read_text().split('## ' + version + ' ', 1)[1].split('\n## ', 1)[0]
guides = f'''

## 安装与使用 / Installation

- 默认 Docker，可选原生 systemd；域名须先解析到服务器并关闭 CDN，公网 TCP / UDP 443 必须空闲且放行。
- Docker 当前仅 IPv4；需要 IPv6 时选择原生。支持 Linux amd64 / x86_64，具体系统与资源要求见安装指南。
- Lite 用于独立站 / 子站，Pro 用于主站管理。已有站点使用面板的更新与回退流程。
- The installer defaults to Docker, with native systemd available. Use a directly resolved domain with CDN proxying off and free public TCP / UDP 443. Docker currently supports IPv4 only.

[七语言介绍 / Introduction in 7 languages](https://github.com/wudi000888-svg/guangyue-panel/tree/v{version}) · [安装指南 / Install guide](https://github.com/wudi000888-svg/guangyue-panel/blob/v{version}/docs/INSTALL.md) · [更新 / Updates](https://github.com/wudi000888-svg/guangyue-panel/blob/v{version}/docs/UPDATES.md)

## 下载文件 / Assets

| 文件 / File | 用途 / Purpose |
| --- | --- |
| `install.sh` | 一键安装入口 / Interactive installer entry point |
| `guangyue-installer-{version}.tar.gz` | 版本化安装器 / Versioned installer |
| `guangyue-panel-lite-{version}-linux-amd64.tar.gz` | Lite 完整运行包 / Complete Lite runtime |
| `guangyue-panel-pro-{version}-linux-amd64.tar.gz` | Pro 完整运行包 / Complete Pro runtime |
| `guangyue-panel-docker-{version}-linux-amd64.tar.gz` | 配套 Docker 镜像归档 / Docker image archive |
| `DOCKER_IMAGES.json` | 镜像与版本信息 / Image manifest |
| `SHA256SUMS` | 文件校验 / File checksums |
| `SBOM.spdx.json` | 依赖信息 / Dependency inventory |

正式发布前重新下载并核对全部资产。校验可检测文件变化，不替代对发布者的信任。 / Assets are downloaded and checked before publication. Checksums detect changes; they do not replace trust in the publisher.
'''
Path(destination).write_text('## ' + version + ' ' + entry.strip() + guides + '\n')
PY
if gh release view "$tag" --json isDraft >/dev/null 2>&1; then
  test "$(gh release view "$tag" --json isDraft --jq .isDraft)" = true || { echo 'Published releases are immutable; use a new version.'; exit 1; }
  gh release upload "$tag" "${assets[@]}" --clobber
  gh release edit "$tag" --notes-file "$tmp/notes.md"
else
  gh release create "$tag" --verify-tag --draft --title "广月面板 $tag" --notes-file "$tmp/notes.md" "${assets[@]}"
fi
mkdir "$tmp/download"
gh release download "$tag" --dir "$tmp/download"
test "$(find "$tmp/download" -type f | wc -l)" -eq 8
cmp build/releases/SHA256SUMS "$tmp/download/SHA256SUMS"
(cd "$tmp/download" && sha256sum -c SHA256SUMS)
gh release edit "$tag" --draft=false --latest
