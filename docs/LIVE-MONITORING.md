# 客户端与实时监控

## 客户端中心

导航中的“客户端中心”对管理员和成员均可见，管理员和成员均可按权限使用。先按系统筛选，再选择 CPU 架构。Apple Silicon 对应 ARM64；Intel/AMD 桌面设备通常对应 x64；Android 需核对设备架构。Linux 直链提供 DEB（Debian/Ubuntu），其他包格式打开官方发布页选择。

提供 Clash Verge Rev、FlClash、v2rayN、v2rayNG、Clash Meta for Android、Shadowrocket。Mihomo 系客户端使用面板的 Mihomo 订阅；v2rayN/v2rayNG/Shadowrocket 使用 Base64/Raw。v2rayNG 仅标注 VLESS；v2rayN 使用 HY2 时需选择兼容核心。iOS/iPadOS 入口为 App Store，费用与地区可用性以商店为准。

下载数据位于 `frontend/src/data/clients.json`。26 个入口于 2026-09-16 从官方 README、GitHub Release Assets、F-Droid 和 Apple 商店核实。直链指向明确版本，不随“latest”重定向改变架构；同时保留官方发布页供选择新版。运行 `python3 scripts/check-client-links.py` 可再次核实所有资产。官方来源：

- [Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev/releases/tag/v2.5.2)
- [FlClash](https://github.com/chen08209/FlClash/releases/tag/v0.8.98)
- [v2rayN](https://github.com/2dust/v2rayN/releases/tag/7.24.9)
- [v2rayNG](https://github.com/2dust/v2rayNG/releases/tag/2.2.6)
- [Clash Meta for Android](https://github.com/MetaCubeX/ClashMetaForAndroid) / [F-Droid](https://f-droid.org/packages/com.github.metacubex.clash.meta/)
- [Shadowrocket](https://apps.apple.com/us/app/shadowrocket/id932747118)

## 管理员实时监控

“实时监控”按用户展示 VLESS 活跃认证会话、HY2 已认证 QUIC 连接、上传/下载速率。VLESS 多路复用载体计一条会话；HY2 一条 QUIC 连接可以承载多个 TCP/UDP 流。这些数量不是设备数、去重 IP 数或网页请求数。

后台在原有约 5 秒流量采样中读取统计，浏览器只读缓存快照；管理员人数增加不会重复运行 Xray CLI。速率使用累计原始字节差除以实际采样间隔，不含计费倍率，且不修改配额检查点。初次采样、核心重启、计数回退、接口失败或超过 15 秒的间隔会显示未知并重新预热，不用零填补丢失数据。

可按用户名搜索、按当前速率排序、分页，选择单个用户查看最近五分钟速率曲线；曲线保留采样缺口。后台最多保留 60 帧和约 20,000 个历史用户点，用户量大时历史窗口缩短。数据仅在进程内存，不写数据库、日志或浏览器存储；无日志模式同样支持临时监控，重启清空。

`GET /api/monitor` 仅管理员可访问，不返回连接目标、IP、订阅令牌或代理凭据。独立站点管理权限允许读取该站快照，只读仪表盘凭据不允许读取用户监控。监控只覆盖当前站点本机核心，主控不将业务站上报的累计账单数据当作实时连接统计；业务站的聚合连接监控未包含在此版本。

## 升级

升级到 0.22.0 时安装包会同步更新带会话计数扩展的 Xray。[源码和构建说明](../core-patches/README.md)随包提供。旧 Xray 可继续提供流量数据，但连接数显示暂不可用，不能用其“在线 IP 数”替代。HY2 使用已有原生 `/online` 认证连接计数。无需数据库迁移。
