# 配置参考

日常管理使用面板设置；安装级配置由向导生成，保存于 `/etc/guangyue-personal.json`。路径中的 `personal` 是历史名称，当前产品名称为广月面板。配置可能包含数据库凭据，不能公开上传或复制另一服务器的随机密钥。

0.41.0 起，默认 Docker 与可选原生部署都采用专属网关。部署身份另存于 root 私有的 `/etc/guangyue-deployment.json`，不要编辑它来切换后端。新安装操作见 [一键安装](INSTALL.md)，已有站点维护见 [更新与回退](UPDATES.md)。

## 站点与持久化

| 字段 | 默认或用途 | 约束 |
| --- | --- | --- |
| `edition` | `lite` / `pro` | Lite 使用 SQLite；Pro 主站需要 PostgreSQL 与 Redis |
| `role` | Lite 为 `standalone`，Pro 为 `controller` | 当前令牌接入不要求重装或改成历史 `business` 角色 |
| `private_gateway` | 新一键部署为 `true` | 与受保护部署身份一致，不用于手动迁移入口 |
| `site_id` | `default` | 小写字母开头、字母数字下划线，最多 40 字符；运行后不可直接改身份 |
| `database.driver` | Lite：`sqlite`；Pro 主站：`postgres` | 历史轻量业务站使用 SQLite |
| `database.dsn` | PostgreSQL URI | 保密；远程库使用 TLS 校验 |
| `database.max_connections` | Pro：8 | 2–64，包含独占控制连接 |
| `redis_url` | `redis://` / `rediss://` | 缓存凭据；故障回查数据库 |
| `state_dir` | `/var/lib/guangyue` | 业务状态、主密钥、核心配置、初始账号、TLS 与钱包密文 |

## 入口与代理核心

| 字段 | 默认或用途 | 约束 |
| --- | --- | --- |
| `listen` | `127.0.0.1:19100` | 面板 HTTP API，不直接暴露公网 |
| `internal_listen` | `127.0.0.1:19101` | HY2 内部鉴权，仅本机或容器共享网络空间 |
| `public_url` | `https://panel.example.com` | 完整 URL，影响 CSRF 与订阅地址 |
| `vless_host` | 节点域名 | 客户端服务器地址，与 Reality SNI 区分 |
| `hy2_host` | 节点域名 | 必须被 HY2 证书覆盖 |
| `reality_sni` | `www.cloudflare.com` | 默认 TLS 目标名称，不应解析到本机 |
| `reality_target` | `www.cloudflare.com:443` | 对应真实 TLS 目标 |
| `reality_private/public` | 初始化生成 X25519 密钥 | 私钥保密；公钥可进入客户端链接 |
| `short_id` | 随机 16 位 hex | Reality 客户端参数 |
| `stats_secret` | 随机令牌 | HY2 统计 API 凭据，不可公开 |
| `cert/cert_key` | `/var/lib/guangyue/tls/current/*.pem` | 由证书系统成对切换，不单独覆盖其一 |
| `xray` | `/opt/guangyue-personal/bin/xray` | 固定受管核心 |
| `hysteria` | `/opt/guangyue-personal/bin/hysteria` | 指定节点路由补丁版 |
| `mihomo` | `/opt/guangyue-personal/bin/mihomo` | 上游机场节点桥接核心 |
| `web_dir` | `/opt/guangyue-personal/web` | 前端构建产物 |
| `dev` | `false` | 仅本地开发模拟，不得用于生产 |

服务 wrapper 固定使用受管路径。任意移动程序、状态或配置，需要同步服务定义与安全检查，属于源码部署变更，不是修改单个字段即可完成。

## 端口与暴露范围

新一键部署不需要 TCP 80 申请证书：TLS-ALPN-01 使用 TCP 443。已有 Nginx 的 HTTP 80 保留，既有 HTTPS / HTTP3 不能继续占用本项目的 TCP / UDP 443。

| 端口 | 用途 | 新部署边界 |
| --- | --- | --- |
| TCP 443 | HTTPS、Reality 分流、ACME | 公网；Docker 仅 IPv4，原生双栈 |
| UDP 443 | HY2 | 公网；必须在安全组与防火墙放行 |
| TCP 18443 | 默认 Xray Reality | 回环 / 容器内部 |
| TCP 19100 | 面板 API | 回环 / 容器内部 |
| TCP 19101 | HY2 鉴权 | 回环 / 容器内部 |
| TCP 19185 | Xray 管理与统计 | 回环 / 容器内部 |
| TCP / UDP 19186 | 节点 DNS 网关 | 回环 / 容器内部 |
| TCP 19199 | HY2 统计 | 回环 / 容器内部 |
| TCP 21000–21255 | 按需出口桥接 | 回环 / 容器内部 |
| TCP 25433 / 26380 | 原生 Pro 的 PostgreSQL / Redis | 仅本机 IPv4 回环 |
| TCP 5432 / 6379 | Docker Pro 的 PostgreSQL / Redis | 私有容器网络，不发布宿主端口 |

历史 Nginx 部署另外使用公网 TCP 80 完成 HTTP challenge / 跳转，以及回环 TCP 10443 接受 PROXY protocol。说明见 [Nginx 指南](NGINX.md)，不要把旧端口表套用到新网关。

内部诊断与控制接口依赖网络和权限边界，不应映射公网；同机不受信任的进程仍需单独评估。

## 面板内设置

品牌、注册、来源更新、公共采集、DNS、套餐和日志模式保存在业务数据库。通过 UI / API 修改以保留校验与核心同步，不直接用 SQL 调整授权。

当前系统设置按任务组织：

| 入口 | 内容 |
| --- | --- |
| 更多功能 | 未固定在侧边栏的功能入口 |
| 站点与注册 | 品牌、公告、开放注册及拼图验证 |
| 侧边栏配置 | 管理员与成员常用入口；拖入 / 移出后自动保存 |
| 下载与访问 | GitHub 客户端中继下载、按国家访问限制 |
| 交易与接入 | 套餐销售相关设置、支付与邮件接入 |
| 运行与网络 | 日志及部署支持的网络策略 |
| 我的账户 / 个人偏好 | 密码、邮箱、语言及外观 |

本地管理员和成员看到的入口不同，远程站点仅显示支持的设置。侧边栏调整自动保存，其他区使用对应保存按钮。加密货币支付和钱包管理有独立功能入口；套餐购买是否开放统一由 **套餐上架** 控制。

新站注册和国家 IP 访问限制默认关闭。独立站支付模块默认启用，外部渠道仍需配置；首次被主站接管的子站默认关闭支付。套餐能否购买由商品上架状态决定。注册拼图在服务端保存答案，支持鼠标、触控和键盘；成功后签发绑定客户端的一次性注册凭证。题目与凭证两分钟过期，错误尝试消耗当前题目，接口限制请求频率与未完成数量，不依赖第三方验证码账户。

## 子站令牌与历史配置

新连接在子站生成令牌、在 Pro 导入，不需要修改配置文件。`gys_` 令牌带站点地址和持久实例身份，令牌哈希、加密连接及暂停状态保存在数据库。`site-local.json` 用于将历史 `business` 子站转换为本地数据管理，须随状态目录备份；原配置与数据库类型保留。

以下字段仅用于历史主动拉取式业务站，不是新一键安装或当前令牌接入的要求：

| 字段 | 历史用途 |
| --- | --- |
| `controller_url` | 主控 HTTPS 根地址；要求公网、可信证书与禁止重定向 |
| `connect_token` | 首次连接用的一次性令牌，交换为长期业务凭据 |
| `enrollment_token` | 旧注册文件传入的一次性凭据，首次同步后失效 |

旧普通 / 公共订阅路由保留各自授权边界；当前成员订阅依据生效套餐提供可用节点。二维码、订阅链接、分享 URI 与连接令牌都应按秘密处理，截图和备份同样需要保护。
