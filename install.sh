#!/usr/bin/env bash
# Small bootstrap. All executable installer files come from a checked Release.
set -euo pipefail
umask 077
if [[ "${1:-}" == --help ]]; then
  printf '%s\n' 'Guangyue one-click installer: Debian 12/13 or Ubuntu 22.04/24.04, amd64.' 'Run as root. Domains must already point to this server; turn off CDN/proxy.' 'Options: --version VERSION, --check, --resume, --plan FILE --yes.'
  exit 0
fi
[[ $(id -u) == 0 ]] || { printf '%s\n' '请使用 root 或 sudo bash install.sh 运行。' >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { printf '%s\n' '当前仅支持 Linux amd64。' >&2; exit 1; }
# shellcheck disable=SC1091
. /etc/os-release
case "${ID}:${VERSION_ID}" in debian:12|debian:13|ubuntu:22.04|ubuntu:24.04) ;; *) printf '%s\n' '仅支持 Debian 12/13、Ubuntu 22.04/24.04。' >&2; exit 1;; esac
[[ -d /run/systemd/system ]] || { printf '%s\n' '需要完整 systemd 服务器。' >&2; exit 1; }
bootstrap_dependencies=()
if [[ ! -x /usr/bin/python3 ]] || ! /usr/bin/python3 -I -c 'import json, hashlib, ssl, tarfile, urllib.request' >/dev/null 2>&1; then
  bootstrap_dependencies+=(python3)
fi
if [[ ! -s /etc/ssl/certs/ca-certificates.crt ]]; then
  bootstrap_dependencies+=(ca-certificates)
elif [[ -x /usr/bin/python3 ]] && ! /usr/bin/python3 -I -c 'import ssl; raise SystemExit(0 if ssl.create_default_context().cert_store_stats()["x509_ca"] > 0 else 1)' >/dev/null 2>&1; then
  bootstrap_dependencies+=(ca-certificates)
fi
if (( ${#bootstrap_dependencies[@]} )); then
  for arg in "$@"; do
    [[ "$arg" != --check ]] || { printf '%s\n' '只读预检查需要完整 Python 3 和可用的 CA 信任根；未安装任何依赖。' >&2; exit 1; }
  done
  printf '%s\n' "仅补齐缺失启动依赖：${bootstrap_dependencies[*]}；不安装/重启任何业务服务。"
  LC_ALL=C apt-get update -qq
  simulation=$(LC_ALL=C apt-get -s --no-install-recommends --no-upgrade install "${bootstrap_dependencies[@]}" 2>&1) || { printf '%s\n' '无法准备启动依赖；请检查 APT/dpkg/CA 状态。' >&2; exit 1; }
  if [[ "$simulation" == *'Remv '* ]] || ! printf '%s\n' "$simulation" | LC_ALL=C awk '/^Inst [^ ]+ \[/ {exit 1}'; then
    printf '%s\n' '依赖需要移除或升级既有系统包，已停止。请自行补齐 Python 3/CA 信任根。' >&2; exit 1
  fi
  DEBIAN_FRONTEND=noninteractive apt-get -y --no-install-recommends --no-upgrade -o DPkg::Lock::Timeout=30 install "${bootstrap_dependencies[@]}" >/dev/null
fi
/usr/bin/python3 -I -c 'import ssl; assert ssl.create_default_context().cert_store_stats()["x509_ca"] > 0' >/dev/null 2>&1 || { printf '%s\n' 'CA 信任根不可用；不会关闭 HTTPS 校验，请修复 ca-certificates 后重试。' >&2; exit 1; }
/usr/bin/python3 -I - "$@" <<'PY'
import hashlib, json, os, re, shutil, stat, subprocess, sys, tarfile, tempfile, urllib.request, urllib.parse
from pathlib import Path
repo='wudi000888-svg/guangyue-panel'
api='https://api.github.com/repos/'+repo+'/releases'
base='https://github.com/'+repo+'/releases/download/'
hosts={'github.com','api.github.com','objects.githubusercontent.com','release-assets.githubusercontent.com'}
def trust(url):
    u=urllib.parse.urlsplit(url)
    if u.scheme!='https' or u.hostname not in hosts or u.username or u.password or u.port not in (None,443):raise ValueError('拒绝非官方下载重定向')
class Redirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,req,fp,code,msg,headers,url):
        trust(url);return super().redirect_request(req,fp,code,msg,headers,url)
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),Redirect())
def get(url,limit):
    trust(url)
    with opener.open(urllib.request.Request(url,headers={'User-Agent':'Guangyue-Bootstrap/1'}),timeout=30) as r:
        data=r.read(limit+1)
    if len(data)>limit:raise ValueError('安装下载超过安全限制')
    return data
version=None
args=sys.argv[1:]
if '--resume' in args:
    receipt=Path('/var/lib/guangyue-installer/receipt.json')
    fd=os.open(receipt,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd) as source:
        info=os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid!=0 or info.st_mode&0o077 or info.st_size>1<<20:raise ValueError('缺少可信的安装回执，不能续装')
        version=json.loads(source.read((1<<20)+1)).get('plan',{}).get('version')
if '--version' in args:
    index=args.index('--version')
    if index+1>=len(args):raise ValueError('缺少版本号')
    if version and version!=args[index+1]:raise ValueError('续装不能替换原安装版本')
    version=args[index+1]
if version and not re.fullmatch(r'(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})',version):raise ValueError('正式版本号无效')
data=json.loads(get(api+('/tags/v'+version if version else '?per_page=30'),4<<20))
chosen=None
for item in [data] if isinstance(data,dict) else data:
    tag=item.get('tag_name','')
    if item.get('draft') or item.get('prerelease') or not re.fullmatch(r'v(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})',tag):continue
    v=tag[1:];name='guangyue-installer-'+v+'.tar.gz'
    names={a.get('name') for a in item.get('assets',[]) if a.get('size',0)>0}
    if {name,'SHA256SUMS','SBOM.spdx.json','install.sh','DOCKER_IMAGES.json','guangyue-panel-docker-'+v+'-linux-amd64.tar.gz','guangyue-panel-lite-'+v+'-linux-amd64.tar.gz','guangyue-panel-pro-'+v+'-linux-amd64.tar.gz'}<=names:
        chosen=(v,name);break
if not chosen:raise ValueError('尚无包含完整一键安装组件的正式版本')
version,name=chosen
sums=get(base+'v'+version+'/SHA256SUMS',64<<10).decode()
matches=re.findall(r'^([a-f0-9]{64})  '+re.escape(name)+r'$',sums,re.M)
if len(matches)!=1:raise ValueError('一键安装包没有唯一 SHA256 记录')
archive=get(base+'v'+version+'/'+name,16<<20)
if hashlib.sha256(archive).hexdigest()!=matches[0]:raise ValueError('一键安装包校验失败')
# Bootstrap downloads are disposable; --check creates no installation receipt.
work=Path(tempfile.mkdtemp(prefix='guangyue-bootstrap-'));work.chmod(0o700)
tarpath=work/'installer.tar.gz';tarpath.write_bytes(archive)
prefix='guangyue-installer-'+version
with tarfile.open(tarpath,'r:gz') as tar:
    members=[];seen=set();total=0
    for item in tar:
        if len(members)>=2000:raise ValueError('安装包文件数量超限')
        parts=item.name.split('/')
        if parts[0]!=prefix or any(p in ('','..','.') for p in parts) or '\\' in item.name or item.name in seen or not(item.isdir() or item.isfile()):raise ValueError('安装包包含不安全路径或链接')
        seen.add(item.name);total+=item.size
        if item.size<0 or total>32<<20:raise ValueError('安装器解压大小超限')
        members.append(item)
    for item in members:
        target=work.joinpath(*item.name.split('/')[1:])
        if item.isdir():target.mkdir(mode=0o700,parents=True,exist_ok=True)
        else:
            target.parent.mkdir(mode=0o700,parents=True,exist_ok=True)
            with tar.extractfile(item) as src,target.open('xb') as dst:shutil.copyfileobj(src,dst)
            target.chmod(0o600)
entry=work/'deploy/oneclick.py'
if not entry.is_file():raise ValueError('缺少可信安装器入口')
if '--version' not in args and '--resume' not in args:args=['--version',version]+args
try:
    code=subprocess.call([sys.executable,'-I',str(entry),*args])
finally:
    shutil.rmtree(work)
raise SystemExit(code)
PY
