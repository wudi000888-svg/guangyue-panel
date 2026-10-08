# 加密货币收款与归集验证记录

适用版本：**0.38.0** · 更新：2026-10-09。分别记录业务 fixture、真实 PostgreSQL、浏览器、本地 EVM 和公共只读探测；本轮没有主网资产交易，也没有生产服务器升级。

## 本轮已执行证据

| 层级 | 结果与范围 |
| --- | --- |
| 新钱包规则 | SQLite race 与真实 PostgreSQL 的支持链、立即返回 Gas 地址、外部 xpub、备份描述防篡改和自动费用预算测试通过 |
| 一键启用与报价 | SQLite race 及真实 PostgreSQL 验证预检失败不改配置、重复请求不重复探测、owner/支付开关、并发编辑 CAS、源失联/过期/脱锚、旧 invoice 原报价和钱包网络限制 |
| 审查回归 | 混合“新鲜脱锚+另一币种过期”响应优先拒绝；无启用资产/账目完整性错误报告未就绪；报价过期仍可关闭收款，重新启用需有效报价 |
| 浏览器 fixture | 真组件桌面与 390px 手机通过：BNB-only、立即 Gas 地址、预检失败/重试、高级手动配置、密码清除、备份/恢复门；没有真实付款账户 |
| 归集浏览器 fixture | 分页与选择保持、25 秒轮询、后台/隐藏页面停查、换钱包取消旧查询、慢请求不并发、Gas 不足禁提交、目标/费用冻结确认、桌面/手机无溢出或运行错误 |
| 本地 Ethereum EVM | `TestCryptoAnvilPaymentGasAndTokenExecution`，Chain ID 1、6 位无价值测试 Token、自动费用上限，race PASS 4.951 秒 |
| 本地 BNB EVM | 同一执行测试，Chain ID 56、18 位无价值测试 Token、自动费用上限，race PASS 5.755 秒 |
| 全仓本机检查 | `scripts/check.sh` 退出 0：真实 PostgreSQL/Redis 配置，Go 全仓 race/vet（控制面 349.352 秒）、26 文件 181 项前端测试、3629 条文案/TypeScript/生产构建、61 项部署及 556 文件发行树检查通过；可调用 Go 漏洞 0，最终源码及 145 个 Git 历史提交泄露扫描通过 |
| GitHub 发行 | 由准确提交的完整 CI 和六组安装/升级/回退检查生成 Lite/Pro 包、校验和与 SPDX SBOM；结果由 Release/Actions 记录核对 |

## 内置公共服务只读核验

| 网络 | 两个独立运营来源 | 官方来源 |
| --- | --- | --- |
| Ethereum | eth.drpc.org、rpc.mevblocker.io | [dRPC](https://drpc.org/chainlist/ethereum-mainnet-rpc)、[MEV Blocker](https://mevblocker.io/) |
| BNB Smart Chain | bsc-rpc.publicnode.com、bsc.blockrazor.xyz | [PublicNode](https://bsc.publicnode.com/)、[BlockRazor](https://docs.blockrazor.io/transaction-submission/rpc/bsc/bsc-rpc-endpoint) |

四个来源已在公开网络只读检查链 ID、genesis、fresh finalized、USDT/USDC 精度、Token/native 余额、Transfer 日志读取、pending nonce、Gas price、原生转账及零 Token 转账模拟。不同最新高度使用已有较低最终高度的规范对齐，不放松 genesis、规范区块、receipt 或最终性检查。仅链 ID 成功不能代替这些检查。另直接调用生产 `cryptoSetupPreflight` 完成默认组合检查：Ethereum 11.14 秒、BSC 8.19 秒，全部通过。

候选 RPC 中有服务拒绝历史 genesis、日志读取、缺失最终状态或已限流，因此没有直接将常见 URL 列表当成可用默认。每个站点的一键启用仍完整预检；公共服务的限额、可达性和未来可用性不能由本次探测保证。高级可覆盖独立 RPC。

本机代理 DNS 返回 198.18 Fake-IP，产品正确拒绝；联合探测仅在测试进程内通过 Google DoH 取得真实公网 DNS 结果，继续使用原生产 transport 和公网校验。未更改系统 DNS 或产品 SSRF 规则。

生产 `fetchCryptoRates` 与 `cryptoApplyMarketRates` 实际请求/解析/应用通过，来源为 CoinGecko + Coinbase（1.28 秒）。CoinGecko 与 Coinbase 公共行情免密钥 GET 已成功，计算全程精确有理数/整数。双源有效时偏差超过 2% 或新鲜 USD 价格超出 0.95–1.05 拒绝新报价；暂不可用/过期来源不能掩盖另一新鲜脱锚证据。CoinGecko 使用源更新时间，Coinbase 绑定 HTTP Date/Age 并限定 300 秒；过期前 150 秒尝试刷新、失败退避 60 秒。

BNB 的 Binance-Peg USDT/USDC 使用基础资产参考价，未提供桥接资产独立脱锚行情。自动报价不提供兑换、保价或行情 SLA。

## 无价值 EVM 闭环

使用官方 Foundry 1.8.5、Solidity 0.8.30，两个仅绑定 loopback、无 fork 的临时 Anvil，分别 Chain ID 1/56。源码为 [crypto_anvil_test.go](../../../backend/internal/controlplane/crypto_anvil_test.go) 和 [LocalTestToken.sol](../../../backend/internal/controlplane/testdata/crypto/LocalTestToken.sol)。不访问或修改真实 Token 合约。

两次实际执行均验证：用户选择 crypto 自动分地址 → 无价值 Token 转入 → 规范最终 receipt 认款 → 套餐 completed → Gas 不足时仅显示预览 → 固定资金地址充值无价值原生币 → 自动估算冻结有限费用上限 → 密码批准 → 持久化签名及 nonce → 补 Gas 最终确认 → Token 提取最终确认。目标收到精确数量，来源 Token 清空；完成后再运行不产生第三笔交易，整库深度资金完整性检查通过。

两 localhost URL 访问同一个 Anvil，仅执行双客户端路径，不证明供应商独立性。本机 EVM 的 chain ID 56 不等于真实 BSC 共识运营验收。每次测试最后回滚临时 snapshot。

```bash
# 仅允许127.0.0.1上的无fork、无价值Anvil。
export GY_TEST_CRYPTO_RPC=http://127.0.0.1:19144
export GY_TEST_CRYPTO_CHAIN_ID=1
export GY_TEST_CRYPTO_TOKEN_CODE=/tmp/local-token-6.hex
cd backend
go test -race ./internal/controlplane -run '^TestCryptoAnvilPaymentGasAndTokenExecution$' -count=1 -v
```

Ethereum 编译 `LocalTestToken` 的 deployedBytecode；BNB 编译同文件 `LocalTestTokenBSC`，使用 Chain ID 56 的临时 Anvil、相应 localhost URL 和 `GY_TEST_CRYPTO_CHAIN_ID=56`。编译选择 Solidity 0.8.30、EVM paris。缺少 RPC/字节码环境变量时跳过，SKIP 不能写成 PASS。

## 保留的资金不变量

- 永久地址、同操作幂等、独立连接并发、高水位恢复；新钱包 `[56]`，历史迁移默认 `[1,56]`。
- 同链事件以 chain/tx/log 去重；少付可补足、最终确认才认款，重复扫描不重复开通。
- 原 invoice 保留报价、资产、地址和截止快照；停用新收款不丢弃历史认款。
- 跨渠道付款归属、晚付/错币/取消后款证据；不将管理员法币确认冒充 crypto 退款。
- 广播前持久化 raw/hash/nonce；不确定结果复查或重播原交易，不新签第二笔。
- 批准 quote 冻结目标、来源、金额和有限预算；不足 Gas/价格超上限停止新签名。
- 双 RPC 不一致、缺失 finalized、签名/receipt 篡改及链冻结不能降级绕过。
- 被管理子站默认关闭支付，本站 owner 明确重开后主站轮询不覆盖；关闭仅阻止新资金授权，历史到账及原批准任务继续。

完整检查使用 `bash scripts/check.sh`，真实 PostgreSQL/Redis 必须配置。SQLite 测试不因 DSN 自动变成 PG，PG 行为使用已注册的 `TestPostgresBehaviors/...`；服务未配置的 skip 须独立记录。前端浏览器夹具与截图保留在仓库外，不进入安装包。

## 未由本轮验收证明的能力

正式主网资产的受控首单、供应商长周期可用性、停机补扫和大地址池运营仍需独立证据。L2 自动收款、Gas 与归集强制关闭；需 L1 最终性/完整费用模型。OKX 身份连接、外部签名器、跨站共享分支、链上退款和自动兑换未交付。外部 xpub 可收款/查余额，面板不能替其签名转出。

热钱包仅用于小额并定期转走。迁移运行中的资金业务须保留数据库、对应 master.key、历史地址、invoice、扫描游标和持久 nonce/签名证据；助记词备份不能代替这些状态。
