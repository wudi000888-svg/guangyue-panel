# 一键安装

准备一台服务器和已解析的域名，运行向导即可部署面板与默认 VLESS / HY2 节点。默认 Docker，也可选择原生 systemd；面板与节点可共用一个域名，公网端口固定为 TCP / UDP **443**。

0.41.0 起，程序、代理核心、证书及 Pro 数据库自动准备，不必填写证书路径或数据库密码。已有站点使用 [更新与回退](UPDATES.md)，不要重新安装覆盖数据；高级旧 Nginx 路径见 [旧部署指南](LEGACY_INSTALL.md)。

## 安装前

**域名必须提前解析到服务器，CDN / Cloudflare 橙云必须关闭。** 安装器会检查，但仍需自己放行云安全组与防火墙。

1. 准备有 root / sudo 权限的 Linux amd64 完整服务器，公网 TCP **443** 与 UDP **443** 均空闲。安装器不会停掉其他程序、迁移已有 HTTPS 站点或改用其他公网端口。
2. 将一个域名解析到本机，也可分别设置面板域名和节点域名。Cloudflare 等服务使用 **仅 DNS**，关闭 CDN / 橙云代理。Docker 当前仅 IPv4 公网，域名只能有 A 记录；需要 AAAA / IPv6 时选择原生双栈，并修正不可达的 AAAA。当前面板网关支持最长 63 字符的域名，这不是 DNS 的通用上限。
3. 在云安全组和主机防火墙放行 TCP 443、UDP 443，保留 SSH 端口。自动证书使用 TLS-ALPN-01，经 TCP 443 申请和续期，不接管 HTTP 80。

空白服务器、已有 Nginx HTTP 80 且 443 空闲的服务器属于本次一键路径。已有 Nginx 配置声明 443、停机 Docker 容器仍映射 443，也会被检测；安装器不会删除配置或停止这些项目。

| 项目 | 范围 |
| --- | --- |
| 系统预检查白名单 | Debian 12 / 13、Ubuntu 22.04 / 24.04，systemd 完整服务器 |
| 架构 | amd64 / x86_64；当前不提供 ARM64 安装资产 |
| Docker 内核 | Linux 5.4 或更新，Docker Engine 与 Compose 插件 |
| 公网 IP | Docker 仅 IPv4；原生支持 IPv4 / IPv6 双栈 |
| 内存建议 | Lite 1 核 / 1 GiB；Pro 2 核 / 2 GiB，另为其他程序留余量 |
| 可用空间预检查 | 原生至少 2 GiB；Docker 至少 4 GiB，另留数据、镜像、日志与备份空间 |
| 测试边界 | 发布 CI 包含 Ubuntu 真实 VM 安装与回退验证；实际结果看该 Release 关联 CI。Debian 白名单不等同完成 Debian 原生 VM 验收 |

## 运行向导

将脚本下载到当前用户的私有临时文件，再执行。以下整段可复制到服务器终端：

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

向导询问 Lite / Pro、面板域名、节点域名（回车同域）、部署方式（回车默认 Docker），以及是否同意 Let’s Encrypt 条款。检查系统、资源、时间、APT 锁、443、Nginx / Docker 配置、DNS 和下载通道后展示计划，确认后才准备面板服务。

启动脚本缺少完整 Python 3 或 CA 信任根时，只补齐缺失启动依赖；APT 模拟发现需升级或移除既有包则停止。原生后端只安装必要依赖，不全量升级系统。新安装默认不改宿主 BBR、qdisc 或 UDP sysctl，保留已有 Nginx 配置和 HTTP 80 响应。

版本只在开始时选择一次，要求正式 Release 八项资产完整，不选择 draft、预发布或未上传完整版本。验证 SHA256、限制官方 HTTPS 重定向与解包路径；Docker 镜像也从本次 Release 归档加载并固定镜像 ID，不需要 GitHub 或镜像仓库账号。SHA256 检测损坏，不替代对发布账号的信任，也不声称有独立离线签名。

## 固定版本与续装

下载同一个脚本后可使用：

```bash
sudo bash install.sh --version 0.41.0
sudo bash install.sh --version 0.41.0 --check
sudo bash install.sh --resume
```

`--check` 只做检查，不安装依赖、创建面板配置或准备服务；缺 Python / CA 时提示后退出。`--resume` 读取 root 私有回执，继续原计划、原版本，保留已经生成的主密钥、初始账号和数据。外部改动的 unit / Compose 及其他程序占端口时会拒绝覆盖。

无人值守入口为 `--plan /root/plan.json --yes`，JSON 需符合当前 schema，明确 `acme_agreed:true`，使用新 UUID，版别 / 后端仅允许 `lite|pro` 与 `docker|native`。它没有跳过 DNS、端口、证书或下载校验的生产参数；普通部署推荐交互向导。

## 首次登录与节点检查

安装完成会显示 HTTPS 地址和初始账号文件。只在自己的终端读取，不贴到公开工单或截图：

```bash
sudo cat /var/lib/guangyue/initial-owner.json
```

用户名 `owner`，密码随机生成；修改密码后初始凭据文件被删除。Lite 默认独立站，Pro 默认主站；安装后可生成子站令牌并导入主站，不需要注册文件。

开放注册、套餐上架及邮件接入由管理员配置。独立站支付模块默认启用，但外部支付渠道仍需有效凭据与实测；首次被主站接管的子站默认关闭支付。套餐能否购买取决于商品是否上架，安装脚本不会自动创建商户账户或完成收款配置。

本机健康只证明本机服务和证书可用，不能单靠 `ss` 证明云安全组允许 UDP。登录后获取管理员套餐的默认本地 VLESS / HY2 节点，分别验证客户端 TCP 连接、DNS、UDP 业务、出口 IP 与流量计量，再交给成员使用。普通 CDN 不转发 Reality / HY2。

## 更新、备份与排错

新原生和 Docker 站点沿自己的维护适配器更新 / 兼容回退，保留数据、密钥、地址历史与当前证书。回退不能低于安装基线；数据库不兼容时会被阻止。旧 Nginx 站点保留既有高级原生升级路径，无需迁移入口。详见 [更新说明](UPDATES.md) 及 [Docker 运行](DOCKER.md)。

更新事务未提交时保持维护冻结，只放行健康检查；root 持久标记 `/var/lib/guangyue-maintenance/pending` 会跨重启保留。恢复器验证当前应运行版本健康后才重新放行用户操作与后台任务，不应手动删除标记跳过恢复。

| 路径 | 内容 |
| --- | --- |
| `/var/lib/guangyue-installer/receipt.json` | root 0600 安装计划和阶段 |
| `/var/lib/guangyue-installer/installer.log` | 脱敏阶段日志，不输出凭据或子进程完整输出 |
| `/var/lib/guangyue` | 账户、主密钥、钱包加密数据和 TLS，需整体备份 |
| `/etc/guangyue-personal.json` | 受保护配置，可能含数据库凭据 |
| `/etc/guangyue-deployment.json` | root 私有后端身份，不应手动更换 backend |
| `/root/guangyue-backups` | 私有更新快照与回退记录 |

失败仅停止本次拥有的资源，保留私有现场供 `--resume`。不要删除状态目录“重试”、关闭 HTTPS 校验或注释端口检查。原生可查 `journalctl -u guangyue-gateway -u guangyue -u guangyue-hy2`；Docker 按 [Docker 指南](DOCKER.md) 检查已登记项目。不要公开完整配置、数据库、钱包备份或未检查的日志。

当前没有一键清空数据的生产参数。停用 / 移除程序前先保存完整备份，仅停止本项目并保留状态、配置、证书和备份。不卸载共享 Docker / 数据库包，不对其他项目运行全局 `docker prune`。
