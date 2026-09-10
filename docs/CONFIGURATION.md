# 配置参考

生产配置 `/etc/guangyue-personal.json` 由初始化命令和安装器生成。不要从别的服务器复制随机密钥。字段名保留现有部署兼容性，磁盘路径中的 `personal` 是历史兼容名称，产品定位已经是广月面板。

| 字段 | 默认/意义 | 注意 |
| --- | --- | --- |
| `edition` | `lite` / `pro` | Pro 主控必须配置 PostgreSQL 与 Redis；业务站使用 SQLite |
| `role` | Lite: `standalone` / Pro: `controller` 或 `business` | 由安装器确定，不支持普通升级改变角色 |
| `controller_url` | 业务站主控 HTTPS 根地址 | 公网、可信证书、禁止重定向 |
| `enrollment_token` | 安装注册文件写入 | 首次同步后失效，长期令牌在本地加密状态中 |
| `site_id` | `default` | 小写字母开头、字母数字下划线、最多 40 字符；生产启用后不可直接修改 |
| `database.driver` | Lite/业务站: `sqlite` / 主控: `postgres` | 禁止用 Lite 连接 PostgreSQL |
| `database.dsn` | PostgreSQL URI | 密码仅存私有配置；远程库使用 TLS 校验 |
| `database.max_connections` | Pro: 8 | 范围 2–64，包含独占控制连接 |
| `redis_url` | `redis://` 或 `rediss://` | 缓存实例凭据；故障降级为数据库查询 |
| `state_dir` | `/var/lib/guangyue` | 数据库、主密钥、核心配置、初始密码、TLS |
| `listen` | `127.0.0.1:19100` | 面板 HTTP，仅回环 |
| `internal_listen` | `127.0.0.1:19101` | HY2 内部鉴权，仅回环 |
| `public_url` | `https://panel.example.com` | 完整 URL，影响 CSRF 和订阅 |
| `vless_host` | `node.example.com` | 客户端服务器地址，不是 Reality SNI |
| `hy2_host` | `node.example.com` | 必须被 HY2 证书覆盖 |
| `reality_sni` | `www.cloudflare.com` | 默认 TLS 伪装目标名称 |
| `reality_target` | `www.cloudflare.com:443` | 对应真实 TLS 目标 |
| `reality_private/public` | 初始化生成 X25519 密钥 | 私钥保密，公钥进入客户端链接 |
| `short_id` | 随机 16 位 hex | Reality 客户端参数 |
| `stats_secret` | 随机令牌 | HY2 统计 API，不能公开 |
| `cert/cert_key` | 安装时设为 `tls/current/*.pem` | 对应证书必须成对更新 |
| `xray` | `/opt/guangyue-personal/bin/xray` | 固定受管核心 |
| `hysteria` | `/opt/guangyue-personal/bin/hysteria` | 必须是指定节点路由补丁版 |
| `mihomo` | `/opt/guangyue-personal/bin/mihomo` | 上游机场节点桥接核心 |
| `web_dir` | `/opt/guangyue-personal/web` | Vite 构建产物 |
| `dev` | `false` | 本地开发模拟，不得用于生产 |

生产 systemd wrapper 固定使用默认路径，不支持通过此表任意移动所有服务。改路径需同步更新 wrapper、unit 和所有安全检查，属于源码部署变更。

## 端口

| 端口 | 用途 | 暴露 |
| --- | --- | --- |
| TCP 80 | ACME 与 HTTPS 跳转 | 公网 |
| TCP 443 | Nginx TLS 分流 | 公网 |
| UDP 443 | HY2 | 公网 |
| TCP 10443 | Nginx 内部 HTTPS / PROXY protocol | 回环 |
| TCP 18443 | 默认 Xray Reality | 回环 |
| TCP 19100 | 面板 API | 回环 |
| TCP 19101 | HY2 鉴权 | 回环 |
| TCP 19185 | Xray 管理/统计 | 回环 |
| TCP/UDP 19186 | 节点 DNS 网关 | 回环 |
| TCP 25433 | Pro 独立 PostgreSQL cluster | 回环 |
| TCP 26380 | Pro 独立 Redis | 回环 |
| TCP 19199 | HY2 统计 | 回环 |
| TCP 21000–21255 | 按需出口桥接监听 | 回环 |

内部端口不能映射到公网。没有鉴权的诊断与控制接口依赖回环边界；同机不受信任的进程仍是风险。

## 面板内设置

站点品牌、语言、来源更新、公共采集策略、节点 DNS 和日志模式保存在状态数据库，不应直接用 SQL 修改。使用 UI/API 以保留校验和核心同步。

普通订阅与公共订阅使用独立路由和令牌。连接二维码与分享 URI 都是秘密；日志、备份和截图必须同等保护。
