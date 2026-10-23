# 运维、备份与排错

先确认部署方式，再选择维护命令。0.41.0 起的新一键安装默认 Docker，可选原生专属网关；之前的 Nginx 原生站点继续沿既有路径更新。不要对 Docker 部署运行一套原生服务重装命令，也不要用安装脚本覆盖正在使用的数据。

| 任务 | 对应说明 |
| --- | --- |
| 新安装、只读预检、失败续装 | [一键安装](INSTALL.md) |
| Docker 项目、镜像与数据边界 | [Docker 部署](DOCKER.md) |
| 在线更新、兼容回退与未完成操作 | [更新指南](UPDATES.md) |
| 旧 Nginx 入口、Certbot 与 PROXY protocol | [Nginx 指南](NGINX.md) |
| 套餐、节点选择和计量 | [套餐指南](PLANS.md) |

## 日常健康检查

先通过公网 HTTPS 检查 `/api/health`，核对运行版本、版别与站点身份，再测试实际代理。健康接口成功只说明控制面就绪，不能证明云安全组放行 UDP 或上游出口正常。

### 原生专属网关

```bash
sudo systemctl status guangyue-gateway guangyue guangyue-xray guangyue-hy2
curl -fsS http://127.0.0.1:19100/api/health
sudo systemctl show guangyue-gateway guangyue guangyue-xray guangyue-hy2 \
  -p MemoryCurrent -p MemoryHigh -p MemoryMax
sudo ss -lntup
```

旧 Nginx 部署用 `nginx` 替代 `guangyue-gateway`，另外运行 `sudo nginx -t`。原生 Pro 可检查 `guangyue-redis`、`pg_lsclusters` 及回环数据库端口。

### Docker

通过受管适配器查询当前登记项目，不打印配置凭据：

```bash
sudo python3 -I - <<'PY'
import sys
sys.path.insert(0, '/opt/guangyue-updater')
import docker_deploy
print(docker_deploy.compose('ps'))
PY
```

面板与核心在容器内运行，宿主上的 `systemctl status guangyue-xray` 不是它们的状态。不要改受管 Compose、换成可变 `latest` 或运行全局 `docker system prune`；项目身份与配置摘要改变时，维护器会拒绝自动覆盖。

## 监控与告警

以下检查均为只读，可接入现有监控系统（cron、Uptime Kuma、Prometheus blackbox 等）。告警只说明“需要人工查看”，不要据此自动重启、删除标记或回滚。

### 健康接口

`GET /api/health` 无需登录，在维护冻结期间仍然放行。返回 HTTP 200 与 JSON，常用字段：

| 字段 | 含义 |
| --- | --- |
| `ok` / `version` / `edition` | 控制面就绪、运行版本、`lite` 或 `pro` |
| `site_id` / `role` | 站点标识与角色（`standalone`、`controller`、`business`） |
| `maintenance` / `background_frozen` | 版本操作或维护冻结尚未完成；长时间为 `true` 时检查更新服务 |

```bash
# 外部探测：经网关 / Nginx，同时验证证书与域名
curl -fsS https://panel.example.com/api/health
# 本机探测：绕过入口，只看控制面（原生；Docker 面板不发布此端口）
curl -fsS http://127.0.0.1:19100/api/health
```

外部探测失败而本机探测成功，通常是网关、证书、DNS 或安全组问题。健康接口不覆盖 UDP 443、Reality 握手或上游出口，仍需定期用真实客户端验收。

### Docker 容器健康状态

0.42.0 起 Compose 为容器增加健康检查：面板容器在容器内请求 `127.0.0.1:19100/api/health`，网关容器运行只读的 `guangyue-gateway -wait-ready` 就绪检查，Pro 的 PostgreSQL 使用 `pg_isready`。旧安装在下一次更新时自动补齐。

```bash
sudo docker ps --filter label=org.guangyue.install-id \
  --format 'table {{.Names}}\t{{.Status}}'
sudo docker inspect --format '{{json .State.Health}}' <容器名>
```

`Status` 中的 `(unhealthy)` 仅作提示：重启策略与更新流程的健康判定不变，Docker 不会因此自动重启容器。

### 日志

| 部署 | 命令 |
| --- | --- |
| 原生专属网关 | `sudo journalctl -u guangyue-gateway -u guangyue -u guangyue-xray -u guangyue-hy2 --since '1 hour ago'` |
| 原生维护执行器 | `sudo journalctl -u guangyue-updater -u guangyue-gateway-cert --since today` |
| Docker 容器 | `sudo docker logs --since 1h <容器名>`（容器名见上方 `docker ps`；日志驱动 `local`，每容器 2 × 5 MB 轮转） |
| Docker 维护执行器 | `sudo journalctl -u guangyue-updater --since today` |

可对 `-p err` 级别或 `Gateway:`、`Upgrade failed`、`Backup pruning` 等关键字建立告警。无日志模式下应用记录减少属预期行为；导出日志前按下文“日志与隐私”脱敏。

### 备份与磁盘

每次版本更新或回退会在 `/root/guangyue-backups/` 新建一个快照（原生 `upgrade-*`，Docker `docker-upgrade-*`）。0.42.0 起，更新成功提交后自动保留最新 5 个快照；下列快照始终保留，不计入删除：

- 本次操作刚创建的快照；
- 当前运行版本的最新快照，以及紧邻的上一个版本的最新快照；
- 仍被未完成更新 / 恢复记录引用的快照。

更新失败或恢复时不清理。只处理维护器创建的 root 私有目录，不触碰手工放入该目录的其他文件。被清理的版本会从回退列表消失。保留数量可通过 root 配置 `/etc/guangyue-personal.json` 的 `"backup_retention": N`（1–100）调整；原生 CLI 更新可用 `upgrade.py --keep-backups N --apply`，该值会写入同一配置。值无效时跳过清理并在日志中记录 `Backup pruning skipped`，不会影响更新。

```bash
sudo du -sh /root/guangyue-backups/* | sort -h
df -h /root
# 只列出按当前策略可清理的快照，不删除
sudo python3 -I /opt/guangyue-updater/backup_retention.py
# 确认后执行；可用 --keep N 临时覆盖保留数量
sudo python3 -I /opt/guangyue-updater/backup_retention.py --apply
```

更新前的空间预检要求 `/root` 所在分区剩余约 3 倍于状态与程序目录的空间（另加 256 MB，Docker 为 512 MB），不足时拒绝更新；建议在接近该阈值前告警。自动快照不能替代管理员下载并离线保存的[完整备份](#完整备份与离线恢复)。

### 证书到期

面板仪表盘在证书 14 天内到期时提示。也可从外部检查实际下发的证书：

```bash
echo | openssl s_client -connect panel.example.com:443 -servername node.example.com 2>/dev/null \
  | openssl x509 -noout -subject -enddate
```

| 部署 | 续期状态检查 |
| --- | --- |
| 原生专属网关 | `sudo journalctl -u guangyue-gateway --since '2 days ago'`；HY2 证书同步看 `systemctl status guangyue-gateway-cert.path guangyue-gateway-cert.timer guangyue-gateway-cert.service` |
| Docker | 网关容器日志与健康状态；HY2 证书代际由面板容器运行器跟随 |
| 旧 Nginx | `sudo certbot certificates`、`systemctl list-timers certbot.timer` |

剩余有效期低于 14 天且仍未续期时，按下文“证书续期与 HY2”排查 DNS 直连与 TCP 443 可达性。

## 资源预算

以下是发布模板限制，不是实际总占用、吞吐量或用户容量承诺：

| 组件 | 原生默认 | Docker 默认 |
| --- | --- | --- |
| Lite 面板 | Go 软目标 48 MiB；MemoryHigh 96M / MemoryMax 160M | 面板与核心合计容器上限 512M |
| Pro 面板 | Go 软目标 192 MiB；MemoryHigh 256M / MemoryMax 384M | 面板与核心合计容器上限 768M |
| Xray / HY2 | 各 MemoryHigh 128M / MemoryMax 256M | 包含于上述面板容器 |
| 专属网关 | Go 软目标 96 MiB；MemoryHigh 128M / MemoryMax 192M | 独立容器上限 192M |
| Pro Redis | maxmemory 64 MiB；服务上限 128M | maxmemory 64 MiB；容器上限 128M |
| Pro PostgreSQL | shared_buffers 128 MiB；40 个连接上限 | 同参数；独立数据库容器 |

代理桥接进程也消耗面板的资源预算；大量导入、测速和多连接会增加内存与 CPU。先降低公共采集并发、节点数量或测速频率，再评估扩容。内存不足仍可能被 OOM 终止。

高级原生部署可用 `systemctl edit guangyue` 添加本机 override，再 `daemon-reload` / `restart`；保留变更记录并核对后续更新兼容性。不要直接改发布包模板作为持久配置。Docker 受管限额不能通过随意编辑 Compose 绕过维护器。

## 日志与隐私

原生常规模式可查看：

```bash
sudo journalctl -u guangyue-gateway -u guangyue -u guangyue-xray -u guangyue-hy2 \
  --since '15 minutes ago'
```

旧 Nginx 站点按需检查其日志。导出前移除订阅令牌、用户、IP、上游 URL、凭据和客户数据。

无日志模式控制本应用和受管核心的记录路径，仍保留用户、配额、凭证与必要业务状态；不覆盖操作系统、Docker 引擎、第三方代理或自行配置的 Nginx 日志。旧标准 Nginx 模板关闭 access / error 输出，排错时由管理员临时启用受限日志，结束后恢复。

## 更新与恢复原则

推荐使用面板 **版本与更新**。执行器根据部署身份选择 Docker、专属原生网关或旧 Nginx 适配器，下载正式版本并验证 SHA256、内层文件、版别和固定核心。不要在服务运行时直接覆盖二进制。

高级维护先获取可信的固定版本包，验证并解压到 root 专属目录，然后进行只读检查。原生可在该目录执行：

```bash
sudo python3 deploy/upgrade.py --bundle "$PWD"
# 确认备份和维护窗口后，再执行：
sudo python3 deploy/upgrade.py --bundle "$PWD" --apply
```

Docker 使用自己的更新适配器，详见 [Docker 指南](DOCKER.md) 与 [版本维护](UPDATES.md)，不要用上述原生 `--apply` 切换容器。

版本操作先暂停本项目服务与数据任务，保存状态、主密钥、程序、配置、服务 / Compose 和适用的 PostgreSQL dump，再准备配置并验证运行版本。失败时尝试恢复本次操作前快照；不会迁移其他 HTTPS 站点、更换公共端口或清空防火墙。

新部署的维护冻结由 `/var/lib/guangyue-maintenance/pending` 持久保存，重启后仍生效。操作未提交时仅放行健康检查，恢复器确认版本与健康状态后才解除冻结。**不要手动删除标记、安装回执或操作 journal 来强行恢复营业。**

跨文件和数据库恢复不是整机断电原子事务。升级中不要重启主机；磁盘 I/O 错误、异常断电或回滚失败时，保留私有现场，由管理员对照快照处理。Pro 不能只还原状态目录而忽略数据库；所有部署都不能把数据库与不匹配的 `master.key` 混用。

### 旧 Pro 的首次恢复工具升级

从 0.35.0 或更早版本首次升级到 0.35.1 或更新版本，使用已验证新包中的 `deploy/upgrade.py --bundle "$PWD" --apply`。不要只调用旧网页按钮或旧执行器的 `install-bundle`，因为旧 PostgreSQL 恢复模块需要在迁移前更新。

新命令获得部署锁并验证包后，确认没有活跃更新、恢复或网络操作，暂停闲置更新入口，原子预置新包的 `infrastructure.py`，再恢复 socket。旧模块与校验摘要保存在 root 私有的 `/var/lib/guangyue-updater/recovery/helper-infrastructure-*/`，文件和目录同步落盘。预置阶段不改 helper 版本、其他脚本、安装基线、记录或配置；只读检查不写入执行器。

锁繁忙或出现并发排队请求时拒绝维护，保留原记录交给执行器处理；不要清空 journal。后续升级失败仍保留新恢复模块，不覆盖回旧逻辑。完整步骤见 [旧 Pro 首次升级](UPDATES.md#旧-pro-首次升级到-0351-或更新版本)。

### 人工恢复现场

原生 `/root/guangyue-backups/upgrade-*/` 包含 `state/`、`app/`、`config.json`、`units/`，Pro 另有 `postgres.dump`；专属网关和维护器也有匹配备份。Docker 快照另外保存项目、镜像与 Compose 身份。优先使用对应恢复器，而非混拼不同时间的文件。

必须人工处理时，先停止本项目全部入口和服务，另存当前现场，再成套还原程序、密钥、状态、配置与适用数据库。原生状态目录归属 `guangyue:guangyue`、配置归属 `root:guangyue`，维持原权限；不得用全局 `chmod -R 777` 解决启动失败。恢复后逐项验证健康、管理员登录、用户授权、VLESS / HY2、出口与计量。

## 完整备份与离线恢复

管理员下载的完整备份包含数据库、主密钥和必要状态，等同敏感资产，应保存于加密的离线存储。便携 SQLite 恢复目前上限 64 MiB（Pro 便携导出也采用此格式），超过时先制定迁移方案，不强改归档绕过校验。

以下仅用于原生离线恢复；专属网关部署先额外停止 `guangyue-gateway`，旧 Nginx 入口应进入维护状态。Docker 必须按其容器与数据库边界制定离线恢复操作，不在宿主直接运行此程序。

```bash
sudo systemctl stop guangyue guangyue-xray guangyue-hy2
# 先另存当前状态、配置和适用的数据库。
# 将备份放到 guangyue 可读的私有目录，例如 /var/lib/guangyue-restore，权限 0700。
sudo runuser -u guangyue -- /opt/guangyue-personal/bin/guangyue \
  -config /etc/guangyue-personal.json -restore /var/lib/guangyue-restore/backup.tar.gz
sudo runuser -u guangyue -- /opt/guangyue-personal/bin/guangyue \
  -config /etc/guangyue-personal.json -prepare
sudo systemctl start guangyue guangyue-xray guangyue-hy2
# 专属网关完成配套状态核验后再恢复入口。
```

程序先在暂存目录验证归档、数据库、密钥与生成配置，替换失败时尝试回退。验收账号数量、套餐权益、订阅权限、凭证、节点出口和流量，确认后移除临时恢复文件。

迁移服务器还需成套迁移或重新准备系统配置、部署 / 更新记录、证书、域名和 Nginx（旧部署适用）。面板便携备份不等于整机镜像。

## 证书续期与 HY2

| 部署 | 续期方式 | 排查重点 |
| --- | --- | --- |
| 新专属网关 | 网关通过 TCP 443 自动 ACME TLS-ALPN-01 续期 | DNS 直连、TCP 443、网关状态及成对证书 |
| 新原生 HY2 | 固定证书 path / timer 通知受限协调器 | `guangyue-gateway-cert.service` 状态；验证后才重启运行中的 HY2 |
| Docker HY2 | 容器运行器监控证书代际 | 网关与面板容器、证书版本、核心状态 |
| 旧 Nginx | Certbot timer 与部署钩子 | 保留 HTTP challenge 路由、钩子与 Nginx 语法检查 |

证书必须覆盖节点域名且与私钥匹配。不要只覆盖运行中证书或私钥的一半。旧续期钩子失败保留上一代；手工恢复也必须成对处理，再校验 Nginx / 网关与 HY2。续期步骤见 [一键安装](INSTALL.md) 或 [旧证书部署](LEGACY_INSTALL.md#8-自动续期)。

## 速度与连通性诊断

1. 用同一客户端、同一目标、相近时间分别测试直连 VLESS / HY2，避免把跨地区链路差异当作面板性能差异。
2. 核对实际节点与出口 IP，确认没有意外绑定慢速 HTTP / SOCKS5 / 公共代理。
3. 观察 CPU、内存、丢包、重传、上游限速及云商带宽；并发测速本身也会争抢资源。
4. VLESS 确认 `flow=xtls-rprx-vision`。网关 / Nginx 应只分流 Reality，不能改成普通 HTTP WebSocket 反代。
5. HY2 检查 UDP 443 的云安全组和主机防火墙；HTTP 成功不能证明 UDP 成功，HTTP 出口通常也不支持 UDP。
6. TCP 查看 `sysctl net.ipv4.tcp_congestion_control net.core.default_qdisc`；BBR 收益取决于内核和链路，与 HY2 的 QUIC 算法不同。
7. 同机 TUN、Fake-IP DNS 或其他代理可能产生回环。用服务器真实 IP 并绑定实际联网网卡做对照；仅改服务器地址不能保证绕开 TUN。SSH 转发可隔离协议问题，但不能替代公网验收。

### 网络优化的适用范围

**0.41.0 新 Docker / 原生专属网关部署默认不修改宿主 BBR、qdisc 或 UDP sysctl。** 对应维护入口保留隔离部署边界，不通过网页自动调整宿主网络。

以下是 0.19.1 起旧 Nginx / 轻量业务站路径的行为，已有部署升级保留原参数与显式选择：

- **BBR**：设置 `net.ipv4.tcp_congestion_control=bbr` 与 `net.core.default_qdisc=fq`，必要时加载 `tcp_bbr`；不替换运行中网卡 qdisc，不承诺所有链路提速。
- **HY2**：`congestion.type=bbr`、`bbrProfile=standard`、`ignoreClientBandwidth=true`；2 MiB 流窗口、5 MiB 连接窗口、256 流并发。UDP 收发上限至少 8 MiB，已有更大值不下调，也不按上限预分配。
- **关闭**：尝试恢复接管前参数，不覆盖管理员后续外部修改。此前已有 BBR 时关闭面板开关仍可能是 BBR；HY2 未声明客户端带宽时仍采用上游默认 BBR，声明带宽时可能使用 Brutal。
- **生效**：HY2 变更会短暂重启面板与核心，保留凭据；页面核对配置和启动时间。仅 BBR 变更不重启代理核心，节点 DNS / IPv6 策略不变。
- **权限**：白名单 `guangyue-network.service` 操作参数，Web 保持非特权；参数文件 `/etc/sysctl.d/99-zz-guangyue-network.conf`，恢复记录在 root 私有更新器目录。安装、更新与网络调整共用部署锁，失败尝试恢复。

旧手动安装可用 `--no-bbr --no-hy2-optimization` 退出默认优化。没有管理员网页的旧业务站，可由 root 使用匹配维护工具调整；此命令不适用于新专属网关部署：

```bash
python3 - <<'PY'
import sys
sys.path.insert(0, '/opt/guangyue-updater')
import network
from common import deployment_lock
with deployment_lock():
    network.apply(True, True)  # BBR, HY2
PY
```

## 备份保留与故障报告

0.42.0 起，更新成功后自动保留最新 5 个升级快照及恢复所需的受保护快照，规则与调整方式见[备份与磁盘](#备份与磁盘)。手工删除快照前至少保留一个已验收版本及离线备份；删除后对应回退选项会减少。

提交 issue 时提供版本、系统、部署方式、重现步骤、预期 / 实际结果、脱敏错误和已做检查。不要上传真实密码、私钥、订阅 URL、完整数据库、钱包备份、未经检查的诊断包或客户资料。

历史 0.20.0 / 0.21.0 引入的套餐和账户能力仍持续演进；当前规则见 [套餐](PLANS.md) 与 [账户服务](COMMERCE.md)，降级以迁移清单和更新器判断为准。
