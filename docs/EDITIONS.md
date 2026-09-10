# Lite 与广月面板 Pro

两版共享业务规则、Nginx 443 分流和代理核心。Lite 适合一个 VPS 的低资源部署；Pro 适合管理多个独立 VPS 站点，提供连接池、Redis 短缓存与持久化任务。升级 Pro 不改变节点协议、订阅令牌或固定出口策略。

0.16.0 默认群站结构为 **主控站＋业务站**，完整安装和授权步骤见[业务站指南](BUSINESS-SITES.md)。主控使用 PostgreSQL/Redis；业务站使用本地 SQLite，不需要为每个 VPS 安装数据库服务。Release assets 分别提供 Lite、Pro 压缩包。

## 版本选择

| 项目 | Lite | Pro |
| --- | --- | --- |
| 产品名 | 广月面板 Lite | 广月面板 Pro |
| 建议起步配置 | 1 核 / 1 GiB RAM | 2 核 / 2 GiB RAM |
| 持久数据 | SQLite WAL，1 个连接 | PostgreSQL 14+，默认 8 个连接 |
| 缓存 | 有界进程缓存 | Redis 7+，站点隔离键，最长 60 秒 |
| 任务执行槽 / 队列容量 | 1 / 64 | 4 / 256 |
| 同时进行的重型线路检测 | 1 | 1，优先保护业务流量 |
| 多 VPS | 每台单独登录 | 统一管理最多 64 个业务站 |
| 备份 | 便携 SQLite 归档 | PostgreSQL 导出为相同便携格式；升级另存 pg_dump |

以上为默认预算与功能边界，不是吞吐量或总内存保证。四个任务槽允许等待、配置和轻量任务交错执行；不会同时开启四个大带宽测速。每个站点仍保留既有节点、出口和桥接端口限制。扩大规模优先增加独立站点，再依据实际内存、链路和核心限制扩容单站点。Pro 管理操作的性能提升来自减少全量轮询、连接复用及缓存；代理流量仍由 Xray/Hysteria 转发，不能把数据库升级理解为线路提速。

## 首次安装 Pro

先按[安装指南](INSTALL.md)完成依赖、DNS、证书、版本包校验和核心获取。以下命令在解压后的固定版本目录运行：

```bash
# 只读说明；加 --apply 创建独立的本地基础设施。
sudo python3 deploy/infrastructure.py
sudo python3 deploy/infrastructure.py --apply

sudo python3 deploy/install.py --bundle "$PWD" --edition pro --site-id site_sg \
  --infrastructure-file /etc/guangyue-infrastructure.json \
  --panel-domain panel.example.com --node-domain node.example.com \
  --cert /etc/letsencrypt/live/panel.example.com/fullchain.pem \
  --key /etc/letsencrypt/live/panel.example.com/privkey.pem
# 预检查通过后，原参数加 --apply。
```

基础设施工具使用发行版安全维护的软件包，建立名为 `guangyue` 的 PostgreSQL cluster（TCP 25433）和 `guangyue-redis` 服务（TCP 26380），只监听 IPv4 回环；不会重配已有 PostgreSQL cluster 或 Redis 实例。APT 可能初始化发行版默认数据库服务，按需自行管理该空服务。工具拒绝占用端口和重复受管配置；部分失败时保留诊断现场，不覆盖已有凭据。

PostgreSQL 使用独立非超级用户和独立数据库。Redis 有密码、64 MiB 数据上限和 128 MiB 服务上限，使用 allkeys-lru，不持久化可重建缓存。随机凭据写入 `/etc/guangyue-infrastructure.json`（0600）；面板安装后配置文件为 root:guangyue 0640。不要把连接字符串放进 issue、命令输出或 Git。

使用外部数据库时，自己准备一个 0600 JSON 文件，结构如下；将占位符替换为真实值。PostgreSQL 必须允许为本站创建 schema；生产远程数据库使用可信 CA 与 `sslmode=verify-full`，Redis 使用 TLS (`rediss://`)。只允许服务器私网或受限网络访问数据库。

```json
{
  "database": {
    "driver": "postgres",
    "dsn": "postgres://panel:REPLACE_ME@db.example.com/guangyue?sslmode=verify-full",
    "max_connections": 8
  },
  "redis_url": "rediss://:REPLACE_ME@cache.example.com:6379/0"
}
```

升级脚本使用 `pg_dump` / `pg_restore`；客户端主版本至少与数据库主版本相同。`site_id` 在各站点必须唯一，例如 `site_sg`、`site_us`。每站点保留独立主密钥与状态目录，不可直接复制另一站点的生产密钥。

## 已有 Lite 升级 Pro

在计划维护窗口前先准备基础设施。此步骤不改变在用代理核心：

```bash
sudo python3 deploy/infrastructure.py --apply
sudo python3 deploy/upgrade.py --bundle "$PWD" --edition pro --site-id site_sg \
  --infrastructure-file /etc/guangyue-infrastructure.json
sudo python3 deploy/upgrade.py --bundle "$PWD" --edition pro --site-id site_sg \
  --infrastructure-file /etc/guangyue-infrastructure.json --apply
```

升级停止本项目服务后保存完整旧目录、配置、密钥和 units；导入原 SQLite 数据及 ID、加密凭据、配额、令牌和绑定关系。目标 PostgreSQL schema 中已有业务数据则拒绝覆盖。源主密钥必须匹配。迁移后原 `panel.db` 仅是旧副本，Pro 的实时数据以 PostgreSQL 为准。

如果后续启动验证失败，安装器恢复旧 Lite 文件和服务；已导入的新 Pro schema 保留以便诊断，**不会自动清空**。再次迁移前，确认该 schema 不属于任何运行站点，然后由管理员删除失败迁移的专属 schema，或使用新的空 `site_id`。不得直接清空共享数据库。

后续 Pro→Pro 升级沿用已配置的数据库和站点 ID，不必重复传 edition。升级前生成专属 schema 的 PostgreSQL dump；失败时恢复 schema 与旧文件。站点 ID 和数据库地址不能通过普通升级命令更换。Pro→Lite 不自动降级；需要独立验证便携备份与容量后恢复到停机的 Lite 实例。

## 独立面板接入（兼容）

1. 在每台 VPS 安装 Pro，分别配置 HTTPS 面板域名和唯一 site_id。各站点自己维护 Nginx、证书、账号、节点与出口。
2. 目标站点 → **群站管理 → 独立面板接入 → 创建接入令牌**，选择“只读查看”或“站点管理”，有效期 1～365 天。令牌绑定创建它的管理员。
3. 主控面板 → **群站管理 → 独立面板接入 → 接入站点**，填目标 HTTPS 根地址和令牌。接入时校验 TLS、地址和站点身份；不得填写内网地址。
4. “检测”适用于两种令牌；只读令牌仅允许状态和仪表盘查询，不导出用户凭据或订阅。管理令牌支持进入目标站点操作用户、节点、资源和任务。
5. 顶部显示当前目标站点并可“返回本站”。切换会取消旧站点请求；节点配置只会应用于目标站点本机核心。
6. 备份、密码、站内信、群站令牌管理在对应面板直接执行。远程备份和递归转发不在网关允许范围内。远程订阅下载在目标站点新标签打开；复制链接和二维码可直接使用。
7. 撤销令牌或禁用创建者后，后续远程请求立即失效。移除接入记录不删除远端数据。

本节兼容接入属于独立站点联邦管理：没有共享用户目录、自动全局配额、负载均衡或跨地域自动故障转移。PostgreSQL advisory lock 防止相同 schema 被两个控制器同时接管；失去控制连接时进程停止，新请求与核心应用会校验控制状态。部署时仍需明确每个站点的唯一负责人，禁止同站点多主部署。SQL schema 是应用隔离边界；不可信租户应使用不同数据库角色、数据库或实例。

## 任务中心与故障处理

测速、质量检测、订阅更新、公共来源抓取与公共采集统一显示排队、运行、完成、失败和取消。任务存入 PostgreSQL/SQLite，重启后未完成工作重新入队，并重新检查资源版本与发起者权限；重复提交相同目标去重。外部资源检测是至少一次执行，允许重试，不能作为恰好一次支付/计费队列。

来源每天错峰更新，更新之间有调度间隔；资源繁忙退避，失败保留原节点。定时任务取消只停止本次执行，仍启用的自动策略会在下一次到期继续工作。常规模式保留最多 100 条、24 小时历史；无日志模式保留结果约 60 秒供 UI 读取，空闲时也会清理。原有业务计量和状态不受界面模式影响。

Redis 故障显示缓存降级并回查数据库；缓存不是持久队列。PostgreSQL 故障阻止管理写入，控制器停止；既有核心配置保留，面板无法保证数据库离线期间的新鉴权或配额变更。数据库恢复后 systemd 重启控制器，应用当前配置。不要将缓存的在线状态当作实时可用性保证。

## 验证与维护

```bash
curl -fsS http://127.0.0.1:19100/api/health
sudo systemctl status guangyue guangyue-redis
sudo pg_lsclusters
sudo ss -lntp
```

健康响应应包含 `edition: pro`、`product: 广月面板 Pro` 和本站 ID。登录确认原用户、两个默认直连节点、私有/公共订阅、出口绑定、任务状态。用真实客户端分别验证 VLESS TCP 和 HY2 TCP/UDP。数据库/Redis 的可用只证明控制面基础设施就绪，不证明节点网络连通。

开发测试需提供 `GY_TEST_POSTGRES_DSN` 和 `GY_TEST_REDIS_URL`（私有环境变量文件）；执行 `go test -race ./...` 会创建并删除随机测试 schema，使用专门测试数据库角色，不要赋予真实生产数据权限。
