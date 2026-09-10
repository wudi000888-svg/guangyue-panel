# 架构与边界

广月面板使用 Go 控制面、Vue 3 前端和独立代理核心。Lite 采用 SQLite；Pro 采用 PostgreSQL + Redis，以独立站点为管理、数据和核心配置边界。[版本指南](EDITIONS.md)包含容量、安装、故障行为和群站语义。

Pro 群站采用[主控站＋业务站](BUSINESS-SITES.md)：主控管理用户、订阅、额度和出口；轻量业务站用本地 SQLite，主动 HTTPS 拉取配置并回传累计流量、报告。每站独立私钥与授权租约；旧独立面板接入保留为兼容入口。

## 代码边界

- `backend/main.go`：薄入口，调用控制面启动装配。
- `backend/internal/controlplane`：HTTP 处理器、应用业务服务和加密业务仓储。各功能按来源、用户、节点、订阅、质量、群站等文件组织，共享经过验证的事务与锁顺序。
- `backend/internal/domain`：账户、凭据结构和可用性规则，无 HTTP、SQL 或核心进程依赖。
- `backend/internal/persistence`：SQL 方言、连接预算、事务、迁移校验、便携快照与控制器独占租约。
- `backend/internal/jobs`：容量受限的持久队列、恢复、去重、超时、取消和保留策略。
- `backend/internal/cache`：本地/Redis 短缓存、站点键前缀、容量和降级。
- `backend/internal/httpapi`：跨站点请求契约、授权范围、受限传输与响应上限。
- `backend/internal/coreprocess`：systemd PID、核心重启和有超时的命令适配。
- `frontend/src/pages`：Vue Router 懒加载页面；`stores` 保存 Pinia 权限/偏好；`composables` 管理工作台、订阅和分页；`components` 提供列表、布局和对话框。

业务服务仍在同一控制面包内共享事务与资源锁，没有为每个 SQL 调用创建抽象仓储接口。后续扩展可沿现有基础设施和领域边界拆分，避免隐藏事务边界或复制两套业务逻辑。参考 Sub2API 的分层、路由、状态管理、缓存和后台调度实践；未引入其 API 中转业务或复制其业务源码。

## 数据与任务

SQLite 使用 WAL 和单连接。PostgreSQL 默认每站点 8 个连接；数据保存在 `gy_<site_id>` schema。迁移文件有顺序和 SHA256 记录，未知迁移与校验不符拒绝启动。AES-GCM 凭据的主密钥仅保留在站点文件中；数据库 dump 与主密钥必须配套保存。

PostgreSQL 是 Pro 持久化事实来源，Redis 只缓存仪表盘等短期结果。缓存键含站点、用户权限、时间范围和运行模式版本。Redis 故障视为未命中；无日志模式绕过仪表盘历史缓存。状态接口每 10 秒轮询轻量数据，全量资源约 60 秒刷新或在操作后刷新，页面离开停止请求。

后台任务保存在数据库并去重，重启恢复时重新验证权限和资源版本。自动来源更新沿用错峰和失败退避。重型测速/采集维持单任务资源预算，避免与业务流量争抢。配置 hash 排除计数和检测元数据；授权、节点路由或凭据变化产生新期望版本，成功应用后记录已应用版本。保留定期核心核对，防止重启或外部故障后永远不重试。

## 网络与核心

Nginx stream 负责 TCP 443 的 SNI 分流：面板/网站进入本地 HTTPS，Reality 进入 Xray；HY2 独立监听 UDP 443。VLESS 使用 Reality + Vision + TCP；HY2 使用 QUIC。Linux TCP BBR 不等同于 HY2 的拥塞控制。

[Hysteria 补丁](../core-patches/README.md)在鉴权成功后按不可变逻辑节点选择出口，未知节点拒绝连接；TCP 与 UDP 使用同一路由。TLS、QUIC 和拥塞控制继承固定上游版本。HTTP/SOCKS5 直出或 Mihomo 机场节点桥接由同一节点出口规则选择；固定业务出口不会因为测速结果自动漂移。

每节点可指定安全 DNS 策略和 IPv6 允许/阻断。DoH 通过选定出口，防止本机解析旁路；上游协议本身是否支持 UDP/IPv6 必须实测。私有与公共资源、节点和订阅使用独立分类与令牌，默认直连节点受保护。

## 权限与恢复

站点管理员和成员权限由后端强制执行，前端守卫只控制可见导航。Pro 管理网关使用短期可撤销令牌、严格路由范围、HTTPS 校验、公网地址校验、禁止重定向和请求/响应上限；不转发 Cookie。远端身份与注册时 site_id 不符则拒绝。站点 schema 不替代不同租户的数据库安全权限。

备份先形成一致快照，网络下载不会长期持有业务锁。恢复先在暂存目录验证密钥、加密集合、迁移和生成配置，再替换业务数据与文件，失败尝试回滚。跨文件与数据库恢复不具备断电原子性；升级专门保存数据库 schema dump，并保留人工恢复资料。[运维指南](OPERATIONS.md)说明限制与操作步骤。

## Plans and node rates (0.20.0)

See [the plans and accounting guide](PLANS.md) for permission groups, manual entitlements, weighted quotas, distributed periods, migration and downgrade restrictions.

## 账户与服务（0.21.0）

余额、兑换码、套餐订单与图片工单的启用步骤、权限、容量限制、群站同步和恢复保护见[账户服务指南](COMMERCE.md)。主控统一记账，业务站不保存资金或工单数据；新增迁移 005 后不支持直接回退至 0.20.x。
