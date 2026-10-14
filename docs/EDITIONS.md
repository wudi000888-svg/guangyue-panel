# Lite 与 Pro：如何选择

Lite 适合独立站点和子站；Pro 适合需要集中管理多个站点的主站。两版共享代理协议、套餐规则和用户界面，区别主要在默认角色、数据库和任务预算。

新安装均使用 [一键向导](INSTALL.md)：选择 Lite / Pro，填写域名，部署方式默认 Docker，也可选择原生 systemd。证书、核心及 Pro 数据库自动准备，无需先填写数据库密码或证书路径。

## 版别对比

| 项目 | Lite | Pro |
| --- | --- | --- |
| 默认角色 | `standalone`，独立运行 | `controller`，主站入口 |
| 建议起步配置 | 1 核 / 1 GiB RAM | 2 核 / 2 GiB RAM |
| 持久数据 | SQLite WAL，1 个连接 | PostgreSQL 14+，默认 8 个连接 |
| 缓存 | 有界进程缓存 | Redis 7+，站点隔离键，最长 60 秒 |
| 任务执行槽 / 队列容量 | 1 / 64 | 4 / 256 |
| 同时进行的重型线路检测 | 1 | 1，优先保护业务流量 |
| 群站接入 | 生成令牌供 Pro 导入，保留本地管理 | 导入令牌，管理最多 64 个 Lite / Pro 子站 |
| 备份 | 便携 SQLite 归档 | 同格式便携导出；升级另存 PostgreSQL dump |
| 新部署入口 | 专属网关，固定 TCP / UDP 443 | 专属网关，固定 TCP / UDP 443 |

这些是默认预算，不是吞吐量、总内存或用户容量承诺。Pro 的四个任务槽用于轻量任务交错执行，不会同时开启四个大带宽测速。代理流量仍由 Xray / Hysteria 转发，更换数据库不会直接提高线路速度。

每站仍受节点、出口及桥接端口限制。扩容前先观察实际内存、CPU、链路与核心负载；Docker 容器限额与原生 systemd 限额不同，见 [运维指南](OPERATIONS.md)。

## 将子站接入 Pro

1. 在已安装的子站中生成配对令牌，可设置有效期或永久有效。
2. 在 Pro 的 **群站管理** 中粘贴令牌，建立连接。
3. 按需设置管理权限、资源预算与节点挂载，再通过套餐向用户授权。

当前 `gys_` 令牌携带地址与站点身份；旧 `gyp_` 令牌需要补填地址。无需注册文件、预建分组或单独“独立站点”入口。撤销令牌或移除连接保留子站本地数据和独立运行能力。完整权限、计量与节点路径见 [子站指南](BUSINESS-SITES.md)。

0.24.0 已将历史独立接入记录并入统一子站列表，只迁移主站目录，不自动修改远端角色。更早的主动拉取式业务站仍有 [历史维护路径](BUSINESS-SITES-LEGACY.md)；新接入推荐主站与子站均使用当前正式版本。

## Pro 的数据库边界

新一键安装自动准备独立基础设施：Docker 使用私有网络中的 PostgreSQL / Redis，不发布宿主数据库端口；原生使用本机专属 PostgreSQL cluster（TCP 25433）与 `guangyue-redis`（TCP 26380），仅监听 IPv4 回环。

应用使用非超级用户和独立数据库。Redis 有密码、64 MiB 数据上限及 128 MiB 服务上限，使用 `allkeys-lru`，不持久化可重建缓存。凭据保存在受保护文件中；不要把连接字符串贴进 issue、终端截图或 Git。

`site_id` 必须在各站点唯一，以小写字母开头，最长 40 字符，例如 `site_sg`。每站保留独立主密钥和状态目录，不可直接复制另一站的生产密钥。

### 高级原生部署与外部数据库

以下属于手动原生路径，不是一键安装的额外前置步骤。现有 Nginx 部署先阅读 [旧安装指南](LEGACY_INSTALL.md)。

```bash
# 在已校验、解压的 Pro 版本包中执行。
# 默认只读；--apply 建立独立的本机数据库和缓存。
sudo python3 deploy/infrastructure.py
sudo python3 deploy/infrastructure.py --apply
```

历史基础设施工具可能随 APT 包初始化发行版默认数据库服务；管理员需核对其用途。0.41.0 新原生向导使用自己的依赖准备流程，不接管已有 cluster 或 Redis。端口、受管配置或凭据已存在时拒绝覆盖，失败现场保留供诊断。

手动使用外部数据库时，准备权限为 `0600` 的 JSON 文件，并替换占位符：

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

PostgreSQL 角色必须可以创建本站 schema。远程数据库使用可信 CA 与 `sslmode=verify-full`，Redis 使用 TLS，仅允许受限网络访问。升级需要 `pg_dump` / `pg_restore`，客户端主版本不得低于数据库主版本。

## 从 Lite 迁移到 Pro

迁移需要维护窗口和完整备份，不能把“切换版别”当作普通版本更新。以下命令适用于高级原生维护；Docker 不支持用这些参数原地更换版别、数据库或后端，应另行验证迁移方案。

在已校验的 Pro 版本包目录中，先准备基础设施，再检查和执行迁移：

```bash
sudo python3 deploy/infrastructure.py --apply
sudo python3 deploy/upgrade.py --bundle "$PWD" --edition pro --site-id site_sg \
  --infrastructure-file /etc/guangyue-infrastructure.json
sudo python3 deploy/upgrade.py --bundle "$PWD" --edition pro --site-id site_sg \
  --infrastructure-file /etc/guangyue-infrastructure.json --apply
```

服务停止后保存旧程序、状态、主密钥、配置与服务定义，再导入 SQLite 数据、ID、凭据、配额、令牌和绑定关系。目标 PostgreSQL schema 已有业务数据或源主密钥不匹配时拒绝覆盖。迁移后实时数据以 PostgreSQL 为准，原 `panel.db` 只是旧副本。

如果启动验证失败，恢复旧 Lite 文件和服务；已导入的新 Pro schema 保留供诊断，不会自动清空。重试前确认该 schema 不属于任何运行站点，再由管理员处理失败迁移的专属 schema，或选择新的空 `site_id`。不得清空共享数据库。

后续 Pro 升级沿用原数据库与站点 ID，失败时恢复本次专属 schema dump 与旧文件。普通升级不能更改站点身份和数据库地址。Pro → Lite 不自动降级，需先验证便携备份与容量，再离线恢复。

## 任务与故障行为

测速、质量检测、订阅更新、来源抓取和公共采集均显示排队、运行、完成、失败或取消。任务持久化，重启后重新检查资源版本与发起者权限，相同目标去重。外部资源任务允许至少一次执行和重试，不承担支付“恰好一次”语义。

来源更新错峰执行，资源忙时退避，失败保留原节点。取消仅停止本次执行，仍启用的自动策略会在下次到期继续。常规模式保留最多 100 条、24 小时任务历史；无日志模式保留结果约 60 秒供界面读取并清理，不清除必要业务计量。

Redis 故障回查数据库；PostgreSQL 故障阻止管理写入，控制器停止。已有核心配置仍保留，但不能保证数据库离线期间的新鉴权或即时额度变更。恢复后由部署适配器重启并应用当前配置，缓存的在线状态不等于实时可用。

## 安装后验收

通过公网 HTTPS 检查 `/api/health`，确认 `edition`、`product` 和站点 ID；服务检查按 [运维指南](OPERATIONS.md) 选择 Docker 或原生命令。登录后核对账号、套餐、订阅节点、出口绑定与任务状态，并用真实客户端分别验证 VLESS TCP、HY2 TCP / UDP 和流量计量。数据库与缓存就绪不能替代节点连通测试。

开发测试使用专属数据库角色，通过私有环境变量文件设置 `GY_TEST_POSTGRES_DSN` / `GY_TEST_REDIS_URL`。`go test -race ./...` 会创建并删除随机测试 schema，不能授予真实生产数据权限。
