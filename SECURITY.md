# 安全政策 / Security policy

## 维护范围

安全修复面向[最新正式版](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)。目前没有单独的 LTS 维护线；旧版用户应先在备份后升级，或说明问题能否在最新正式版复现。若问题阻止安全升级，请在私密报告中说明版本和受影响部署方式。

本项目尚未声明独立安全审计、SLA、组织多租户隔离或热钱包资产安全保证。测试与扫描是工程检查，不等于第三方安全认证。

## 私密报告

优先使用仓库 **Security → Report a vulnerability**；该入口仅在维护者启用 GitHub 私密漏洞报告后可见。若入口不可用，请创建**不包含漏洞细节的联系请求**，等待维护者提供私密通道后再发送详情。不要在公开 issue 或 PR 发布利用方法、真实受害数据或凭证。

私密报告请包含：

- 面板版本、Lite/Pro、Docker/原生安装，以及独立站/主站/子站角色。
- 受影响功能、所需权限和可能的影响。
- 使用合成账号与数据的最小复现步骤；预期与实际行为。
- 已采取的临时缓解措施，以及能否在最新正式版复现。

**禁止公开上传**密码、私钥、助记词、`master.key`、数据库、完整备份、订阅链接或令牌、支付/邮件/RPC 凭据、客户数据和未检查的生产日志。截图同样需要脱敏。发现凭证泄露应立即轮换或撤销相应凭证；删除文件或改变仓库可见性不能撤销已经泄露的秘密。

## 部署与资金边界

生产部署需要可信 HTTPS、受限内部端口、独立服务账号和受保护的配置、密钥及备份。不要跳过域名、证书、端口或发布包校验，也不要将开发模式暴露到公网。已有站点使用[更新与恢复流程](docs/UPDATES.md)，避免通过重新安装或删除状态目录清除故障。

内置热钱包的密钥材料和签名能力处于同一服务器信任域。加密落盘不能防止服务器失陷后的签名滥用；仅用于小额运营资金，定期转出并私下保管恢复材料。外部 xpub 不含私钥，不能在后台签名提取。公共 RPC 的可用性、双来源核对和手续费上限不构成无条件的资金安全保证。详见[资金管理边界](docs/CRYPTO_TREASURY.md)。

配对令牌、订阅凭据和付款渠道凭据有不同权限，应分别撤销或轮换。无日志模式不是匿名或合规认证，必要业务记录仍会保存，第三方服务也可能记录流量。可选公共代理不应承载敏感业务。

## 工程检查

构建使用锁定依赖和固定代理核心来源，发布包包含组件许可及对应源码材料。CI 执行测试、依赖漏洞检查、源码和提交历史秘密扫描；GitHub 秘密扫描也已启用。扫描无法覆盖所有误用、逻辑漏洞或运行环境风险。

发布工作流核验精确主线提交、完整资产和 SHA256 后发布正式版。目前未配置独立离线签名，SHA256 不提供发布者身份认证。安全修复应附覆盖权限、失败关闭、重复请求及信息泄露的回归测试，并说明升级与数据迁移影响。

## English summary

Security fixes target the [latest stable release](https://github.com/wudi000888-svg/guangyue-panel/releases/latest); there is no separate LTS line. Use **Security → Report a vulnerability** if available. Otherwise, open a contact request without technical vulnerability details and wait for a private channel. Never publish live credentials, wallet secrets, subscriptions, customer data, databases or unreviewed logs. Include the version, edition, deployment mode, site role and a minimal reproduction with synthetic data.

The project does not claim an independent security audit. A panel-managed hot wallet shares the server's trust boundary and should hold only small operating balances. Checksums and automated scans do not guarantee publisher identity or deployment security.
