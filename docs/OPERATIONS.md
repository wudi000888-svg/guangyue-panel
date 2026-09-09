# 运维、备份与排错

## 服务与资源

```bash
sudo systemctl status guangyue guangyue-xray guangyue-hy2 nginx
curl -fsS http://127.0.0.1:19100/api/health
sudo systemctl show guangyue guangyue-xray guangyue-hy2 -p MemoryCurrent -p MemoryHigh -p MemoryMax
sudo ss -lntup
```

面板 Go 软内存目标为 48 MiB；systemd 面板限制为 MemoryHigh=96M / MemoryMax=160M，Xray 与 HY2 各 128M / 256M。这些是控制参数，不是总占用承诺。代理桥接进程属于面板 cgroup，导入大量机场资源时需要观察整体内存。内存不足可能被 OOM 终止。

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

### 人工回滚

`/root/guangyue-backups/upgrade-<时间>/` 包含 `state/`、`app/`、`config.json`、`units/`。停止三个服务，先把当前目录另存，然后将备份复制回原路径。状态目录递归归属 `guangyue:guangyue`、权限 0700，配置归属 `root:guangyue`、权限 0640。复制服务文件，daemon-reload 后启动，并验证 Nginx、面板、两种协议与用户权限。

不要只回滚数据库而保留不匹配的 `master.key`。不要公开上传备份，里面含解密密钥与全部凭证。

## 完整备份和恢复

管理员在面板下载完整备份，放到加密存储。备份包含数据库、主密钥和必要状态，是敏感资产。SQLite 数据库目前有 64 MiB 恢复上限；超出时需要先制定迁移方案，不要强行修改归档。

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
6. 只在有对照测试时调整参数；本安装器不批量写入 sysctl、不提高不受控 UDP 缓冲，也不会关闭 IPv6 安全策略来换速度。

## 备份保留和故障报告

安装/升级备份不会自动清理，管理员需安排保留策略并监控磁盘。删除前至少保留一个已验收版本和离线备份。

提交 issue 需要：版本、系统、部署方式、重现步骤、预期/实际结果、脱敏错误、相关测试。不要提供真实密码、私钥、订阅 URL、数据库、完整诊断包或客户信息。
