# 客户端选择与实时监控

## 为设备选择客户端

管理员和成员可在 **客户端中心** 先选择系统，再选择 CPU 架构。Apple Silicon 使用 ARM64，Intel/AMD 桌面设备通常为 x64，Android 按设备实际架构选择。目录中的 Linux DEB 包适用于 Debian/Ubuntu；其他包格式到官方发布页选择。

| 客户端 | 面板订阅格式 | 使用边界 |
| --- | --- | --- |
| Clash Verge Rev、FlClash、Clash Meta for Android | Mihomo | 以客户端当前内核的 VLESS/HY2 支持为准 |
| v2rayN | Base64 / Raw | 使用 HY2 需选择支持该协议的核心 |
| v2rayNG | Base64 / Raw | 面板目录仅标注 VLESS 支持 |
| Shadowrocket | Base64 / Raw | iOS/iPadOS 从 App Store 安装，费用和地区由商店决定 |

客户端中心使用对应项目图标、官方来源及明确版本安装包，不把同名不同架构的文件混为一个链接。面板提供连接配置，不能替客户端开启 TUN、安装驱动或解决操作系统的安全提示。

下载目录由 `frontend/src/data/clients.json` 维护，采用固定版本而非可变的 `latest` 跳转。目录版本不代表上游最新版本，也不保证外部链接永远可达。管理员可运行 `python3 scripts/check-client-links.py` 核实资产；更新目录后按[中继说明](CLIENT-DOWNLOADS.md)同步后端快照。

官方下载来源：[Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev/releases)、[FlClash](https://github.com/chen08209/FlClash/releases)、[v2rayN](https://github.com/2dust/v2rayN/releases)、[v2rayNG](https://github.com/2dust/v2rayNG/releases)、[Clash Meta for Android](https://github.com/MetaCubeX/ClashMetaForAndroid)、[F-Droid](https://f-droid.org/packages/com.github.metacubex.clash.meta/)、[Shadowrocket](https://apps.apple.com/us/app/shadowrocket/id932747118)。如开启[站点下载中继](CLIENT-DOWNLOADS.md)，GitHub 固定安装包可由本站服务器提供下载。

## 查看用户当前连接与流量

管理员打开 **实时监控**，可搜索用户、按当前速率排序并分页；选择一位用户查看最近五分钟上传/下载曲线。进入子站远程管理后，可查看该子站的快照。

| 指标 | 统计含义 |
| --- | --- |
| VLESS 活跃会话 | 已认证的代理会话，多路复用载体计一条 |
| HY2 活跃连接 | 已认证的 QUIC 连接，一条可承载多个 TCP/UDP 流 |
| 上传/下载速率 | 两次核心累计原始字节差 ÷ 实际采样间隔，不应用计费倍率 |
| 曲线缺口/未知 | 初始预热、核心重启、计数回退、读取失败或样本过期，不以零补齐 |

连接数不是设备数、去重 IP 数或网页请求数。实时速率用于观察当前负载；套餐配额与账单使用持久计量，不能用曲线替代结算。

后台复用约 5 秒采样，浏览器读取缓存，不随每个管理员请求重复启动 Xray CLI。超过 15 秒间隔重新预热。历史只保存在进程内存，最多 60 帧、约 20,000 个用户点，人数较多时窗口缩短；服务重启清空，无日志模式也可使用临时监控。

## 权限与版本边界

`GET /api/monitor` 仅管理员可访问，不返回连接目标、IP、订阅令牌或代理凭据。管理令牌可读取子站快照，只读仪表盘凭据不能读取用户监控。

监控反映当前站点本机核心，不把旧业务协议的累计账单当作全站实时连接统计，也不自动合并不同站点的同名用户。VLESS 会话数依赖随安装包提供的 Xray 扩展；仅有原版核心时可以读取流量，但连接数可能未知，不能以“在线 IP 数”替代。[扩展源码和构建说明](../core-patches/README.md)随项目和安装包提供，HY2 使用原生 `/online` 计数。
