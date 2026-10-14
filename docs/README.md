# 广月文档导航

第一次使用请从[一键安装](INSTALL.md)开始；已部署站点请从[更新与回退](UPDATES.md)或[运维](OPERATIONS.md)开始。新安装默认 Docker，也可选择原生 systemd；公网 TCP / UDP 443 固定，域名须直连服务器且关闭 CDN。

项目介绍：[简体中文](../README.md) · [English](../README_EN.md) · [فارسی](../README.fa_IR.md) · [العربية](../README.ar_EG.md) · [Español](../README.es_ES.md) · [Русский](../README.ru_RU.md) · [Türkçe](../README.tr_TR.md)。面板界面目前支持中英，详细指南主要为中文；有英文版本的条目在下表单独标出。

## 安装与日常运行

| 任务 | 指南 |
| --- | --- |
| 准备服务器、检查域名、第一次安装 | [一键安装](INSTALL.md) |
| 管理默认 Docker 部署、数据和容器 | [Docker](DOCKER.md) |
| 选择 Lite / Pro，理解独立站和主站 | [版本区别](EDITIONS.md) |
| 备份、恢复、排查运行故障 | [运维](OPERATIONS.md) |
| 检测版本、更新与回退 | [版本管理](UPDATES.md) |
| 系统配置与服务接入 | [系统配置](CONFIGURATION.md) |
| 调整侧边栏入口 | [功能入口](SIDEBAR.md) |
| 配置国家访问规则 | [访问控制](SITE-ACCESS.md) |

## 套餐、用户与支持

| 任务 | 指南 |
| --- | --- |
| 了解管理员和成员的操作流程 | [使用手册](USER_GUIDE.md) |
| 配置节点授权、倍率、额度与有效期 | [套餐与权益](PLANS.md) |
| 上架套餐、处理订单、退款和工单 | [交易与服务](COMMERCE.md) · [English](COMMERCE_EN.md) |
| 配置 SMTP、验证邮件与通知 | [邮件](EMAIL.md) |
| 提供对应平台的客户端与下载中继 | [客户端中心](CLIENT-DOWNLOADS.md) |
| 查看用户流量与连接数 | [实时监控](LIVE-MONITORING.md) |

## 子站与网络资源

| 任务 | 指南 |
| --- | --- |
| 用令牌连接子站、管理授权、版本和服务 | [子站管理](BUSINESS-SITES.md) |
| 区分直连节点与中转 IP，设置共享预算 | [节点挂载与资源预算](NODE_MOUNTS.md) |
| 了解可达性检测的证据与边界 | [使用手册：节点与 IP 质量](USER_GUIDE.md) |
| 维护已有 Nginx 部署 | [历史 Nginx 共存方式](NGINX.md) |
| 维护旧安装路径 | [历史安装说明](LEGACY_INSTALL.md) |

## 加密收款与钱包

| 任务 | 指南 |
| --- | --- |
| 创建或导入钱包、启用支持链与币种 | [钱包管理](CRYPTO_WALLETS.md) |
| 查看订单地址、余额、Gas 与管理员归集 | [收款与归集](CRYPTO_TREASURY.md) |
| 了解钱包页面与费用交互 | [钱包体验](WALLET_EXPERIENCE.md) |
| 核对当前接口与交付边界 | [接口契约](projects/crypto-payments/CONTRACTS.md) |
| 了解签名组件的构建来源 | [签名构建](CRYPTO_BUILD.md) |

先按当前操作指南配置，再验收自己的 RPC、资金地址和渠道。只读报价、主网广播、最终到账与套餐开通是不同步骤；公共 RPC 或行情可用，不等于生产支付已经验收。

## 开发与交付

| 任务 | 指南 |
| --- | --- |
| 修改代码或文档，提交问题与 PR | [贡献指南](../CONTRIBUTING.md) |
| 了解组件、数据与运行路径 | [架构](ARCHITECTURE.md) |
| 检查发布资产与流程 | [发布](RELEASE.md) |
| 私密报告安全问题 | [安全政策](../SECURITY.md) |
| 核对依赖许可与对应源码 | [第三方声明](../THIRD_PARTY_NOTICES.md) |
| 查看界面设计源 | [Penpot](designs/penpot/README.md) |

## 设计与历史验证记录

以下内容用于理解设计演进与特定时点的测试，**不是最新版功能清单、配置指南或生产容量证明**。请核对文件中日期、版本、测试环境和未实现项。

- [版本验证记录](VALIDATION.md)：按记录内版本和场景理解测试结果。
- [支付长期设计](CRYPTO_PAYMENTS.md)：包含已实现部分、候选链和后续设计；操作以钱包和归集指南为准。
- [支付项目设计](projects/crypto-payments/README.md)与[验收计划](projects/crypto-payments/VALIDATION.md)：区分方案要求和已完成验证。
- [Pro 实施清单](PRO-IMPLEMENTATION.md)与[控制器实施清单](CONTROLLER-IMPLEMENTATION.md)：保留历史里程碑。
- [0.30.0 技术债](TECH-DEBT-0.30.0.md)：特定版本的审计记录，不代表当前全部问题仍存在。
