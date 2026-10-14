<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

<div align="center">

<img src="docs/assets/banner.svg" alt="广月面板 Guangyue Panel：一键部署、套餐运营、IP 与子站管理" width="960" />

# 广月面板 · Guangyue Panel

**从一个域名开始，把节点、套餐与子站放进同一个工作台。**

[![最新版本](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel?color=2563eb)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

[开始安装](docs/INSTALL.md) · [下载最新版](https://github.com/wudi000888-svg/guangyue-panel/releases/latest) · [使用手册](docs/USER_GUIDE.md) · [文档导航](docs/README.md)

</div>

广月是基于 Go + Vue 3 的自托管代理服务与网络资源管理面板。准备一台符合要求的服务器和一个已解析的域名，即可部署 VLESS Reality/Vision 与 Hysteria 2，随后在面板中管理套餐订阅、出口 IP、独立子站、订单工单与收款。

适合从单站起步的代理服务运营者、需要集中管理多个独立站点的管理员，以及需要为成员分配出口 IP 的团队。Lite 用于独立运营，Pro 扩展为主站控制台；两者均可由交互脚本安装。

## 快速开始

### 安装前准备

- **域名必须已经解析到这台服务器，不能开启 CDN 代理。** Cloudflare 选择“仅 DNS”，关闭橙色云朵。
- 使用有 root / sudo 权限的 **Linux amd64（x86_64）完整 systemd 服务器**。安装预检查接受 Debian 12/13、Ubuntu 22.04/24.04；原生 VM 安装测试目前覆盖 Ubuntu 22.04/24.04，系统白名单不等于每种服务器配置都已验收。
- **公网 TCP 443 与 UDP 443 必须空闲并放行**，包括云安全组和主机防火墙。脚本固定使用 443，不会自动迁移现有 HTTPS 服务；已有 Nginx 的 HTTP 80 服务可以保留。
- **默认 Docker，可选原生安装。** Docker 当前仅发布 IPv4，请只配置 A 记录，不要配置 AAAA；需要 IPv6 时选择原生双栈。Docker 要求 Linux 5.4 或更新。
- 建议 Lite 从 1 核 / 1 GiB、Pro 从 2 核 / 2 GiB 起步，并为镜像、数据和备份留空间。这是安装建议，不是并发人数或带宽承诺。

### 运行安装脚本

将下面整段复制到服务器终端：

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

向导会让你选择 Lite / Pro、绑定域名和部署方式，检查系统、资源、DNS、端口以及已有 Nginx / Docker 配置，然后展示安装计划供确认。运行组件和 Docker 镜像来自所选正式 Release 并校验文件摘要；无需创建镜像仓库账号。证书通过 TCP 443 的 TLS-ALPN-01 自动签发和续期。

安装后，按脚本提示读取私有初始账户文件 `/var/lib/guangyue/initial-owner.json`，登录并修改管理员密码，再用真实客户端测试 VLESS 与 HY2。按需配置注册、套餐上架和收款渠道；外部支付与邮件服务需要你自己的有效配置。

`--check` 可执行只读预检查，`--resume` 可恢复中断的安装。已有站点请使用[在线更新与回退](docs/UPDATES.md)，不要用新装流程覆盖数据。详细要求、失败恢复和部署限制见[安装指南](docs/INSTALL.md)。

## Lite 与 Pro 怎么选

| | Lite | Pro |
| --- | --- | --- |
| 默认用途 | 独立站，也可作为子站 | 主站，集中管理子站 |
| 数据库 | SQLite | PostgreSQL |
| 缓存与任务 | 有界进程缓存、持久化任务队列 | Redis 缓存、持久化任务队列 |
| 本站功能 | 节点、IP 池、套餐、用户、订单与工单 | 同 Lite，并增加集中管理能力 |
| 连接方式 | 生成子站令牌供主站导入 | 粘贴子站令牌，建立连接 |
| 安装方式 | Docker / 原生 systemd | Docker / 原生 systemd |

安装不需要注册文件，也不需要先创建站点分组。**子站生成令牌，Pro 主站粘贴导入。** 令牌可设置期限或永久有效，直到撤销。接入后子站保留本地管理员、账户、数据库和独立服务；授权可以撤销。首次被接管的子站默认关闭支付模块，本地管理员仍可按需启用。详见[版本区别](docs/EDITIONS.md)与[子站管理](docs/BUSINESS-SITES.md)。

## 能做什么

| 场景 | 能力 |
| --- | --- |
| 节点与订阅 | VLESS Reality/Vision、HY2；Raw / Base64 / Mihomo 订阅、复制与二维码；按平台提供官方客户端入口与可选站点中继下载 |
| 套餐运营 | 按来源节点组或组内指定节点授权，额度与有效期、零元上架、限购、套餐排队；套餐内节点倍率按 0.5 一档，最高 10× |
| 订单与支持 | 站内余额、兑换码、订单、退款申请 / 取消 / 审批 / 拒绝、图片工单、管理员内部备注与站内通知 |
| IP 资源 | HTTP CONNECT / SOCKS5 出口、授权上游订阅、来源自动更新、质量检测、DNS 与 IPv6 策略；公共资源入口默认隐藏，可按当前浏览器偏好显示 |
| 子站管理 | 令牌接入、权限与服务操作、共享预算和冷却恢复、节点挂载、子站 IP 出口、受支持的版本检测与更新回退 |
| 运行监控 | 用户流量曲线、VLESS 会话与 HY2 连接采样、服务状态、任务进度与重启恢复 |
| 邮件与支付 | SMTP 邮件、易支付 V1、Stripe Checkout、支持链和白名单资产的加密收款；外部渠道需配置并验收 |
| 钱包管理 | 内置 HD 热钱包 / 外部公钥导入、永久订单地址、余额与有余额地址、Gas 预估、管理员确认归集 |
| 日常工作台 | 中英文界面、亮暗主题、移动端、可调整侧边栏、国家访问规则、备份恢复与版本管理 |

### 套餐决定成员能用哪些节点

管理员在套餐内选择本地、子站或公共节点组，也可以选择组内的指定节点。成员看到自己的当前套餐、剩余时间与额度、订阅入口和客户端，不必理解节点组与混合订阅的内部规则。

购买另一套餐后，原套餐剩余有效期暂停，按权益队列恢复。退款需管理员审核：当前套餐按剩余额度或时间计算，排队订单当前按原订单金额退回，其他权益的恢复顺序保留；具体规则见[套餐](docs/PLANS.md)与[订单和退款](docs/COMMERCE.md)。当前套餐的节点、倍率和协议修改会同步服务端授权；额度、有效期和排队套餐保留快照。客户端缓存的订阅仍需要刷新。

### 子站节点与子站 IP 是两条不同路径

- **挂载子站节点：** 客户端直接连接子站节点，流量不过主站。
- **使用子站 IP 出口：** 客户端连接主站节点，主站中转到子站 IP 出口。

两类资源分别管理并标明来源。子站共享预算耗尽时按配置暂停授权，冷却结束后恢复；节点组成员关系保留。配置流程见[节点挂载与预算](docs/NODE_MOUNTS.md)。

### 加密收款与钱包

用户选择加密货币支付时分配独立、永久保存的订单地址。当前自动收款和归集覆盖 Ethereum、BNB Smart Chain、Plasma 与 Solana 的受支持 USDT / USDC 资产，以合约或 mint 白名单为准；配置 L2 不代表已支持自动结算与归集。

余额与 Gas 查询是只读操作。**只有管理员确认归集才支付手续费**；自动手续费支持仅用于真实订单地址，所选白名单稳定币余额须严格大于 0.1。EVM 只补不足差额，Solana 使用固定 SOL 资金地址代付；超过原批准费用上限时暂停并要求人工核对。详细操作和链差异见[钱包管理](docs/CRYPTO_WALLETS.md)与[订单收款和归集](docs/CRYPTO_TREASURY.md)。

内置热钱包适合小额运营资金。助记词虽加密保存，签名权限仍在同一服务器，请私下备份并定期取走资金；外部 EVM xpub 可用于地址派生和查询，资金转出由外部钱包完成，不等于拥有服务器签名能力。

## 使用边界

- 当前直接提供的入口协议为 **VLESS Reality/Vision 与 Hysteria 2**。可导入的上游格式取决于解析器与 Mihomo，不等于新增同名原生入口。
- 当前按单站控制器部署，不承诺同站多主高可用、组织级租户隔离、SLA 或独立安全审计。Pro 的数据库与缓存不会改善一条本身拥堵的网络线路。
- CI 包含隔离 Linux 安装、更新恢复和协议验证；这不等于大规模生产容量报告或你的商户与主网交易已验收。上线前请测试真实客户端、备份、邮件和收款流程。
- RPC、行情与质量检测源可能限流或不可用。未知结果不会当成零费用；公开页面可达不等于账号、播放、支付或区域业务全部解锁。
- 本仓库提供 **7 种语言的项目介绍**；面板界面当前支持 **简体中文和英语**。详细手册以中文为主，部分提供英文；翻译介绍不代表整套界面和手册已完成对应语言本地化。

## 按任务查文档

| 你想做什么 | 从这里开始 |
| --- | --- |
| 安装与选择版别 | [一键安装](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Lite / Pro](docs/EDITIONS.md) |
| 配置套餐、销售与用户订阅 | [使用手册](docs/USER_GUIDE.md) · [套餐](docs/PLANS.md) · [订单与支付](docs/COMMERCE.md) |
| 连接子站、挂载节点与分配出口 | [子站管理](docs/BUSINESS-SITES.md) · [节点挂载](docs/NODE_MOUNTS.md) · [实时监控](docs/LIVE-MONITORING.md) |
| 配置邮件、加密收款与归集 | [邮件](docs/EMAIL.md) · [钱包](docs/CRYPTO_WALLETS.md) · [收款与归集](docs/CRYPTO_TREASURY.md) |
| 运维、故障排查与更新 | [运维](docs/OPERATIONS.md) · [版本更新](docs/UPDATES.md) · [系统配置](docs/CONFIGURATION.md) |
| 参与开发与安全报告 | [贡献指南](CONTRIBUTING.md) · [架构](docs/ARCHITECTURE.md) · [安全](SECURITY.md) |

完整索引见[文档导航](docs/README.md)。设计资料见 [Penpot 设计源](docs/designs/penpot/README.md)与[界面设计方案](docs/designs/penpot-courtyard-system.md)。

## 开源与贡献

欢迎提交可复现的问题、改进建议与 Pull Request。报告问题请提供版本、版别、部署方式、站点角色和复现步骤，勿上传真实订阅凭据、私钥、助记词或完整生产配置。功能和文档修改的验证要求见[贡献指南](CONTRIBUTING.md)，安全问题使用[私密报告流程](SECURITY.md)。

广月面板自 0.15.0 起采用 **LGPL-3.0-only**。原 fake-ui MIT 声明保留在[历史许可证](licenses/guangyue-legacy-MIT.txt)，并附 [GPL-3.0 文本](licenses/GPL-3.0.txt)。自定义 Hysteria 补丁采用 MIT，Xray 为 MPL-2.0，Mihomo 为 GPL-3.0；组件归属与对应源码见[第三方声明](THIRD_PARTY_NOTICES.md)。

项目介绍的组织方式参考 [3x-ui](https://github.com/MHSanaei/3x-ui)，多语言内容按广月实际能力编写；既有文档结构曾参考 Sub2API，未使用其品牌或截图。完整 Release 提供运行包、安装器、Docker 镜像归档、校验清单与依赖信息，详见[发布说明](docs/RELEASE.md)。如果项目对你有帮助，欢迎 Star，或贡献一次实际安装反馈。
