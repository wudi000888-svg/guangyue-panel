# 运维、备份与排错

## 服务与资源

```bash
sudo systemctl status guangyue guangyue-xray guangyue-hy2 nginx
curl -fsS http://127.0.0.1:19100/api/health
sudo systemctl show guangyue guangyue-xray guangyue-hy2 -p MemoryCurrent -p MemoryHigh -p MemoryMax
sudo ss -lntup
```

Lite 面板 Go 软内存目标为 48 MiB；systemd 面板限制为 MemoryHigh=96M / MemoryMax=160M，Xray 与 HY2 各 128M / 256M。这些是控制参数，不是总占用承诺。代理桥接进程属于面板 cgroup，导入大量机场资源时需要观察整体内存。内存不足可能被 OOM 终止。

Pro 面板软内存目标 192 MiB，MemoryHigh=256M / MemoryMax=384M；Redis 实例 maxmemory=64 MiB / MemoryMax=128M，PostgreSQL shared_buffers=128 MiB、40 个连接上限。数据库实际内存随连接和查询变化。

需要调整时用 `systemctl edit guangyue` 添加 override，再 daemon-reload 和 restart；不要改发布包内 unit 作为持久配置。先降低公共采集并发、数量、测速频率，再评估提高限额。不要盲目开启无限并发。

## 常规与无日志

常规模式可用 `journalctl -u guangyue -u guangyue-xray -u guangyue-hy2 --since '15 minutes ago'` 查看运行诊断。导出日志前移除订阅令牌、IP、用户名、上游 URL 等信息。

无日志模式控制本应用和受管核心的记录路径，仍保留用户、配额、凭证及必要业务状态。系统、第三方代理与自行配置的 Nginx 日志不受此保证覆盖。标准安装模板关闭 Nginx access/error 输出；排错时由管理员临时启用受限日志并在结束后恢复。

## 升级

只从可信 Release 下载固定版本，核对压缩包与内层 SHA256SUMS，按安装指南获取固定 Xray/Mihomo。不要跳过包校验或在运行时直接覆盖二进制。

```bash
# 在新版本解压目录执行：
sudo python3 deploy/upgrade.py --bundle "$PWD"
# 通过后安排短维护窗口：
sudo python3 deploy/upgrade.py --bundle "$PWD" --apply
```

升级先停止本项目三个服务，离线备份状态、主密钥、应用、配置与 units，再更新程序、前端和核心。验证生成配置、启动服务并检查健康；失败会恢复旧目录并尝试重新启动。不会改 Nginx 站点、系统 BBR、其他服务或防火墙。

**备份不是断电原子事务。** 升级期间不要重启主机。回滚失败、磁盘 I/O 错误或异常断电时，使用保留的目录人工恢复，并逐项验证。跨版本数据库迁移以变更日志为准，不保证任意降级。

Pro 升级还会保存站点专属 `postgres.dump`（含 schema）与便携备份；失败时先恢复数据库 schema，再启动旧应用。仅还原状态目录不能回滚 Pro 数据库。Lite→Pro 操作参见[版本迁移](EDITIONS.md)。

### 人工回滚

`/root/guangyue-backups/upgrade-<时间>/` 包含 `state/`、`app/`、`config.json`、`units/`。停止三个服务，先把当前目录另存，然后将备份复制回原路径。状态目录递归归属 `guangyue:guangyue`、权限 0700，配置归属 `root:guangyue`、权限 0640。复制服务文件，daemon-reload 后启动，并验证 Nginx、面板、两种协议与用户权限。

不要只回滚数据库而保留不匹配的 `master.key`。不要公开上传备份，里面含解密密钥与全部凭证。

## 完整备份和恢复

管理员在面板下载完整备份，放到加密存储。备份包含数据库、主密钥和必要状态，是敏感资产。便携备份中的 SQLite 数据库目前有 64 MiB 恢复上限（Pro 导出也使用该格式）；超出时需要先制定迁移方案，不要强行修改归档。

恢复必须离线：

```bash
sudo systemctl stop guangyue guangyue-xray guangyue-hy2
# 先保存当前 /var/lib/guangyue 和 /etc/guangyue-personal.json。
# 将备份放到 guangyue 可读的受限目录，例如 /var/lib/guangyue-restore，权限 0700。
sudo runuser -u guangyue -- /opt/guangyue-personal/bin/guangyue \
  -config /etc/guangyue-personal.json -restore /var/lib/guangyue-restore/backup.tar.gz
sudo runuser -u guangyue -- /opt/guangyue-personal/bin/guangyue -prepare
sudo systemctl start guangyue guangyue-xray guangyue-hy2
```

程序先验证归档、数据库与密钥，在临时目录校验后替换，I/O 错误会尝试回退；不是跨文件断电原子提交。恢复后核对管理员登录、用户数量、私有/公共订阅隔离、凭证、额度和节点出口。确认后删除临时恢复文件。

迁移新服务器还需要单独迁移或重建系统配置、TLS 证书、Nginx 和域名。面板备份不等于整机镜像。

## 证书续期

详见安装指南。保留 Certbot timer 和 HTTP challenge 路由。每次更新校验证书涵盖的两个域名、剩余有效期、证书公钥与私钥匹配。部署钩子仅操作本面板证书。

如果 HY2 在续期后失败，查看证书部署退出状态和服务状态；脚本保留上一代并在失败时回退。手工恢复时必须成对恢复证书/密钥，然后 reload Nginx、restart HY2。

## 速度诊断顺序

1. 同一客户端、同一目标、相近时间分别测试直连 VLESS 与 HY2，避免把跨地区链路差异误认为面板速度差异。
2. 核对客户端实际选择节点与出口 IP，检查是否绑定了慢速 HTTP/SOCKS5/公共代理。
3. 观察 VPS CPU、内存、丢包、TCP 重传、上游限速以及云商带宽；并发测速本身也会竞争资源。
4. VLESS 使用 Vision；确认客户端没有丢失 `flow=xtls-rprx-vision`。Nginx stream 应仅分流，不应转成 HTTP WebSocket 链路。
5. Linux TCP 查看 `sysctl net.ipv4.tcp_congestion_control net.core.default_qdisc`；BBR 是否有利取决于内核与路径。HY2 的 QUIC 拥塞控制不等同于 Linux TCP BBR。
6. 本机同时运行 TUN、Fake-IP DNS 或另一个代理时，先排除二次代理、SNI 嗅探重定向和路由回环。可让测试客户端使用服务器真实 IP，并绑定实际联网网卡做对照（Xray `streamSettings.sockopt.interface`）；网卡名必须以本机实际配置为准。仅改服务器地址不能保证绕过 TUN。SSH 转发适合隔离协议问题，但不能代替公网直连验收。
7. 网络优化的开关和实际状态见 **系统设置 → 网络优化**，简易和专业模式均可使用。IPv4/IPv6 的 TCP 共用系统拥塞算法，HY2 使用独立 QUIC 算法；优化不改变节点 DNS 或 IPv6 安全策略。

### BBR 与 HY2 优化

0.19.1 起，Lite / Pro 主控 / Pro 业务站新安装默认启用两项优化；已有部署升级保留原系统参数和显式选择。

- **BBR**：设置 `net.ipv4.tcp_congestion_control=bbr`、`net.core.default_qdisc=fq`，必要时加载 `tcp_bbr`。作用于新建 TCP 连接，不替换运行中网卡的 qdisc，不保证所有线路提速。
- **HY2**：采用 `congestion.type=bbr`、`bbrProfile=standard`、`ignoreClientBandwidth=true`，避免客户端声明带宽强制限速或切换 Brutal。保持 2 MiB 流窗口 / 5 MiB 连接窗口和 256 流并发。UDP 接收/发送上限至少 8 MiB，已有更大值不下调，也不按上限预分配内存。
- **关闭**：恢复面板接管前的系统值，管理员后续从系统外修改的值不强行覆盖。此前已启用 BBR 的主机关闭面板开关后可能仍为 BBR，界面显示实际算法。HY2 关闭后取消显式策略：不声明客户端带宽时仍是上游默认 BBR，声明带宽时可使用 Brutal。
- **生效**：HY2 修改会短暂重启面板和 HY2，保留订阅凭据。页面自动核对配置与服务启动时间；尚未加载的配置不会标为已生效。仅切换 BBR 不重启代理核心。
- **权限与恢复**：参数保存在 `/etc/sysctl.d/99-zz-guangyue-network.conf`，原值和恢复记录在 root 专属 `/var/lib/guangyue-updater/`。固定的 `guangyue-network.service` 执行白名单操作；Web 进程保持非特权。安装、升级和网络操作使用同一部署锁。内核不支持、写入失败或服务未就绪会报错并尝试恢复。

不允许修改宿主机网络参数的容器可在安装命令添加 `--no-bbr --no-hy2-optimization`。业务站默认随安装启用优化；没有管理员网页的业务站可在本机以 root 使用相同维护工具调整：

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

## 备份保留和故障报告

安装/升级备份不会自动清理，管理员需安排保留策略并监控磁盘。删除前至少保留一个已验收版本和离线备份。

提交 issue 需要：版本、系统、部署方式、重现步骤、预期/实际结果、脱敏错误、相关测试。不要提供真实密码、私钥、订阅 URL、数据库、完整诊断包或客户信息。

## Plans and node rates (0.20.0)

See [the plans and accounting guide](PLANS.md) for permission groups, manual entitlements, weighted quotas, distributed periods, migration and downgrade restrictions.

## 账户与服务（0.21.0）

余额、兑换码、套餐订单与图片工单的启用步骤、权限、容量限制、群站同步和恢复保护见[账户服务指南](COMMERCE.md)。主控统一记账，业务站不保存资金或工单数据；新增迁移 005 后不支持直接回退至 0.20.x。
