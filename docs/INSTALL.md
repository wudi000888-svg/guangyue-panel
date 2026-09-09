# 安装指南

适用于广月面板 0.15.0。首次部署按本文从上到下执行。已有部署使用[升级流程](OPERATIONS.md)，不要重新运行安装器覆盖数据。

本文默认安装 Lite；安装 Pro 或从 Lite 升级，先阅读[双版本部署指南](EDITIONS.md)。证书、Nginx 与代理核心步骤两版通用。

## 1. 系统、资源与域名

| 项目 | 要求 |
| --- | --- |
| 系统 | Debian 12/13 或 Ubuntu 22.04/24.04，systemd 作为 PID 1 |
| 架构 | amd64 / x86_64；当前发布包不支持 ARM64 |
| 资源建议 | 1 vCPU、1 GiB RAM、至少 2 GiB 可用磁盘；大量导入/测速建议更多内存 |
| 权限 | root 或 sudo，用于服务、证书与 Nginx 安装 |
| 公网入口 | TCP 80（ACME）、TCP 443（面板/Reality）、UDP 443（HY2） |
| 域名 | 最少 1 个，推荐 2 个；若配置 AAAA，IPv6 必须确实可达 |

推荐 DNS：`panel.example.com` → VPS 公网 IP；`node.example.com` → 同一 IP。两者也可使用同一个域名。**示例域名必须替换**。初次部署使用仅 DNS 解析；普通 CDN 的 HTTP 代理不能直接转发 Reality 和 HY2。

Reality SNI 默认 `www.cloudflare.com`，它是 TLS 伪装目标，不要把此域名解析到自己的 VPS。自定义目标必须具备兼容的 TLS 1.3 / HTTP/2 能力，且不能是本站域名或内网地址。

已有 Nginx/Caddy/Apache 或其他服务占用 TCP/UDP 443，先阅读 [Nginx 共存](NGINX.md)。本安装器不会抢占端口、覆盖其他虚拟主机或更改防火墙。

## 2. 安装系统依赖

```bash
sudo apt-get update
sudo apt-get install -y nginx libnginx-mod-stream certbot python3 curl unzip ca-certificates openssl
sudo systemctl enable --now nginx
```

保留发行版的 `www-data` Nginx worker 身份、`modules-enabled` 与 `sites-enabled` 结构。安装器会验证 stream 模块、realip 模块与实际配置语法。安装器提供的 `listen ... http2` 兼容老版本 Nginx；新版本可能显示弃用提示，但仍有效。

在云安全组和主机防火墙放行 TCP 80/443、UDP 443。**保留当前 SSH 端口放行**，不要直接套用清空防火墙规则的命令。

## 3. 申请证书

### 新 Nginx：使用默认 webroot

确认公网访问 `http://panel.example.com/.well-known/acme-challenge/...` 和节点域名能到达本机 `/var/www/html`。默认站点已删除或改过时，应先自行建立只提供 ACME webroot 的临时 HTTP 站点。

```bash
sudo install -d -m 0755 /var/www/html/.well-known/acme-challenge
sudo certbot certonly --webroot -w /var/www/html \
  --cert-name panel.example.com \
  -d panel.example.com -d node.example.com \
  --agree-tos --register-unsafely-without-email
```

使用上述命令表示同意 ACME 服务条款；示例选择不设置邮箱。需要邮件提醒时，用 `--email you@example.com` 代替 `--register-unsafely-without-email`。只有一个域名时只保留一个 `-d`。

证书必须同时覆盖面板和节点域名，剩余有效期超过 24 小时，证书与密钥匹配。可用自有证书代替 Certbot；安装器同样校验，但续期需由你现有证书系统调用部署脚本。

已有同名临时 HTTP 虚拟主机，申请完成后停用该临时配置，`nginx -t` 再 reload；安装器会创建正式 ACME 路由。不要删除其他站点。详见 [Nginx 指南](NGINX.md)。

## 4. 获取固定版本

### 从 GitHub Release 获取

仓库处于私有阶段时，先通过 GitHub CLI 登录有访问权限的账号。草稿 Release 也需要仓库权限；公开后可以直接从 Releases 下载同名文件。不要把访问令牌写进命令历史或脚本。

```bash
# 在工作电脑或服务器执行；请使用已经发布/准备好的固定版本。
gh release download v0.15.0 --repo wudi000888-svg/guangyue-panel \
  --pattern 'guangyue-panel-0.15.0-linux-amd64.tar.gz' --pattern 'SHA256SUMS'
# Linux：
sha256sum --ignore-missing -c SHA256SUMS
# macOS 对已下载文件可用 shasum -a 256，并与 SHA256SUMS 对照。
tar -xzf guangyue-panel-0.15.0-linux-amd64.tar.gz
cd guangyue-panel-0.15.0-linux-amd64
```

校验文件来自同一个 Release；SHA256 检测损坏，不替代对发布账号与签名的信任。此版本不声称有独立的离线签名。

### 从源码构建

在开发机安装 Go 1.26.8、Node.js 24 LTS、npm、Git、Python 3、curl、unzip，然后：

```bash
git clone https://github.com/wudi000888-svg/guangyue-panel.git
cd guangyue-panel
git checkout v0.15.0
bash scripts/check.sh
bash scripts/build.sh
bash scripts/build-hy2-core.sh --test
python3 scripts/third-party.py
python3 scripts/package.py
```

输出位于 `build/releases/`。源码构建支持 macOS/Linux 开发机，部署产物固定为 Linux amd64。Go 工具链按 `go.mod` 的 toolchain 指令固定到 1.26.8。编译所需内存显著高于面板运行内存，建议在工作机或 CI 构建。

## 5. 下载代理核心

应用发布包包含广月程序、前端与经过节点路由补丁的 Hysteria。Xray 和 Mihomo 使用脚本从各自官方 Release 下载固定版本并核验 SHA256，不捆绑在应用压缩包内。

在已解压的版本包目录执行：

```bash
GY_BIN_DIR="$PWD/bin" bash scripts/fetch-xray.sh
GY_BIN_DIR="$PWD/bin" bash scripts/fetch-mihomo.sh
```

离线主机：在相同版本的解压目录完成下载后，连同完整目录通过 SCP/其他安全方式传入服务器；不要替换为未知来源二进制。Mihomo 是机场订阅节点作为出口的桥接核心；没有桥接资源时不会常驻启动。

## 6. 预检查与安装

```bash
sudo python3 deploy/install.py --bundle "$PWD" \
  --panel-domain panel.example.com --node-domain node.example.com \
  --cert /etc/letsencrypt/live/panel.example.com/fullchain.pem \
  --key /etc/letsencrypt/live/panel.example.com/privkey.pem
```

预检查是只读操作。它验证系统、端口、文件校验和、证书、Nginx worker 与实际模板。通过后使用同一命令加 `--apply`：

```bash
sudo python3 deploy/install.py --bundle "$PWD" \
  --panel-domain panel.example.com --node-domain node.example.com \
  --cert /etc/letsencrypt/live/panel.example.com/fullchain.pem \
  --key /etc/letsencrypt/live/panel.example.com/privkey.pem --apply
```

可用 `--reality-sni www.example-target.com` 指定默认伪装目标；先确认目标可达且兼容。已有其他网站时，按 Nginx 文档迁移后，每个网站用 `--web-domain shop.example.com` 显式加入分流表。不要把节点域名用作 Reality 伪装目标。

安装生成随机密钥和初始管理员，将状态存入 `/var/lib/guangyue`，创建三个非 root 服务。失败会尝试恢复 Nginx 与清理本次新建文件，失败状态保存在 root 专有备份目录。不会操作其他项目服务。

## 7. 首次登录与验收

```bash
sudo systemctl is-active nginx guangyue guangyue-xray guangyue-hy2
curl -fsS http://127.0.0.1:19100/api/health
# 只在自己的本地终端查看，不截图公开或提交到 Git。
sudo cat /var/lib/guangyue/initial-owner.json
```

浏览器打开 `https://panel.example.com`。初始用户名 `owner`，密码随机生成；修改密码后初始凭证文件会删除。健康接口返回 200 只证明面板进程可用，**仍要做节点真实连接测试**：

1. 创建成员，确认私有订阅里有默认 VLESS 和 HY2 直连节点。
2. 用支持 Reality/Vision 和 HY2 的客户端分别连接，两次分别查看外部出口 IP。
3. 测试 TCP 网页、DNS；需要 UDP 的业务再单独测试 UDP。HTTP 出口通常不提供 UDP。
4. 添加一个你有权限使用的 HTTP/SOCKS5 出口，绑定新节点后核对出口变化。
5. 核对用户可见节点、质量报告、流量与禁用用户后的连接撤销。

## 8. 自动续期

```bash
sudo systemctl enable --now certbot.timer
sudo certbot renew --dry-run
```

安装器创建 `/etc/letsencrypt/renewal-hooks/deploy/guangyue-panel`。续期成功时只处理本面板证书，校验域名与密钥匹配，将完整证书对切换到新目录，再 reload Nginx 并重启 HY2。会短暂重连 HY2。保留一代旧证书；失败回退。

`--dry-run` 默认不会执行部署钩子。确认需要验证完整部署时运行：

```bash
sudo python3 /opt/guangyue-personal/deploy/certificates.py
```

自有证书：更新 `/etc/guangyue-certificate.json` 指向的源文件，再运行同一脚本。不要直接修改运行中证书的一半。

## 9. 卸载（默认保留数据）

先在面板下载完整备份并离线保存。然后停止面板服务：

```bash
sudo systemctl disable --now guangyue guangyue-xray guangyue-hy2
```

从 `/etc/nginx/nginx.conf` 删除 `include /etc/nginx/guangyue-stream.conf;`，移除 `sites-enabled/guangyue-panel.conf`，恢复其他网站原来的监听方式，然后 `sudo nginx -t && sudo systemctl reload nginx`。删除本项目三个 unit、对应 Certbot 钩子和 tmpfiles 配置后 `sudo systemctl daemon-reload`。

确认没有其他网站引用后，可移除 `/opt/guangyue-personal` 和本项目 Nginx 片段。**默认保留** `/var/lib/guangyue`、配置、证书与 `/root/guangyue-backups`，它们包含用户数据和恢复密钥。需要彻底清除时由管理员另行确认并手动删除；本项目不提供自动清空数据命令。
