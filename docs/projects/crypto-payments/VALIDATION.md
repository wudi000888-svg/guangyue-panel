# 加密货币支付与资产管理验证记录

适用版本：**0.37.0** · 更新：2026-10-09。记录区分代码实现、模拟 RPC 业务测试、真实 PostgreSQL、浏览器 fixture、本地 EVM 执行和正式网络运营；不能用其中一项替代其他项。

## 当前已取得的证据

| 验证层级 | 已执行内容与结果 | 能证明 / 不能证明 |
| --- | --- | --- |
| 钱包基础 | `crypto_hd_test.go`、`crypto_wallet_test.go` 已有公开 HD 向量、权限、备份、并发、恢复和快照测试；本轮钱包相关 SQLite race 回归通过 | 能证明本地派生、已知索引与持久边界；不能证明旧备份自动获知其他部署的地址使用 |
| SQLite 收款业务 | `go test -race ./internal/controlplane -run '^TestCrypto(Payment\|Invoice\|Wallet)' -count=1` 通过，15.85 秒 | 临时 SQLite、双 HTTP RPC fixture 下报价、invoice、收款、最终性、履约及恢复行为 |
| SQLite 资金执行 | `go test -race ./internal/controlplane -run 'TestCrypto(Sweep\|Signing\|EVM\|Estimate)' -count=1` 通过，8.967 秒；非 race 定向套件也通过 | 签名意图、事务、租约、nonce、Gas/Token 执行与失败恢复；RPC 链证据仍为 fixture |
| PostgreSQL 业务 | 实际 PostgreSQL 17.11，运行 `TestPostgresBehaviors/(crypto_payment_\|crypto_invoice_\|crypto_sweep_)` 的 race 套件通过：9 个顶层组、14 个子例，6.91 秒 | PostgreSQL 真实数据库行为和独立连接竞争；链仍由两个 httptest RPC 模拟 |
| 末批边缘场景 | 及时上链延迟发现、停用后历史核对、错误网络留审、链冻结阻断确认/履约、免费订单及恢复门、invoice 分配相关 SQLite + PostgreSQL race 定向测试通过，11.618 秒 | 对应新增资金边界已有数据库断言，不只是 HTTP 200 |
| 归集完整性补充 | `TestCryptoSweepIntegrity*` 的 SQLite 定向测试通过，1.837 秒；真实 PostgreSQL 的 `TestPostgresBehaviors/crypto_sweep_integrity*` 同两项通过，2.570 秒 | 缺失明细/资金地址、签名 nonce、加密报价、伪造最终状态/凭据/区块均被拒绝；合法完成、失败/结束、快照跨库复制及旧热钱包升级通过 |
| 支付模块总开关 | 最终全仓检查中的 3 项 `TestPaymentModule*` SQLite 测试及真实 PostgreSQL 对应分组通过；包含这 3 组的完整 `TestPostgresBehaviors` 单独复验亦通过，93.673 秒 | 首次管理接管默认关闭、owner 显式重开且不被后续请求覆盖、业务代理限制；关闭后阻止新资金意图，已有收款继续结算，已批准提取继续完成 |
| 前端构建与单元 | 最新生产 build、TypeScript、3578 条文案覆盖检查及 **170 项测试通过** | 精确金额、状态、操作标识恢复、资金请求本地域隔离和构建；不是链上执行证明 |
| 桌面/手机浏览器 | Chromium 桌面及 390px 手机的真实组件/完整订单页，拦截 API fixture 通过，无 JavaScript 错误 | 证明交互、显示、轮询与失联恢复；浏览器 fixture 未连接真实付款账户 |
| 本地 EVM | **`TestCryptoAnvilPaymentGasAndTokenExecution` 已通过**；无 fork 的本机 Anvil，详见下节 | 真实 EVM 执行、签名广播和 receipt 闭环；不能证明独立 RPC 供应商、主网资产或 L2 最终性 |
| 首轮全仓本机检查（历史） | 配置真实 PostgreSQL、Redis 后的首轮 `scripts/check.sh` 通过：Go 全仓 race/vet、当时 166 项前端测试/构建、61 项部署测试和发行树检查；可调用 Go 漏洞为 0，发行源码与历史泄露扫描通过 | 这是后续支付模块、完整性及前端补充前的历史结果；最终复验结果见下一行 |
| 最终全仓本机检查 | 补充完成后的 `scripts/check.sh` 已退出 0：配置真实 PostgreSQL、Redis，Go 全仓 race/vet 通过，控制面测试 317.939 秒；26 个前端测试文件共 170 项通过，3578 条文案覆盖及 TypeScript/生产构建通过，61 项部署测试及 554 文件发行树检查通过 | 证明本次最终代码的本机集成检查通过；Linux 安装升级及发布安装包仍由 GitHub CI 核对，不表示生产站已经升级 |

表内正则中的竖线用于说明测试分组；可复制命令见后文。时长是该次执行记录，不构成性能指标。

## 本地 Anvil 已通过的闭环

使用 Foundry 1.8.5、Solidity 0.8.30，本机未 fork 的临时 Anvil。测试源码为 [`crypto_anvil_test.go`](../../../backend/internal/controlplane/crypto_anvil_test.go)，无价值 Token 源码为 [`LocalTestToken.sol`](../../../backend/internal/controlplane/testdata/crypto/LocalTestToken.sol)。在隔离 EVM 的白名单测试地址注入该 Token 的已部署字节码，不部署或修改任何公开网络的真实合约。

已通过的执行链路：

1. 用户确认 crypto 后自动生成 invoice 和独立地址。
2. 在本地 EVM 真实执行测试 Token 转入，读取真实 receipt，后台自动认款，订单到 `completed`。
3. 未给固定 Gas 资金地址充值时，预览显示所需金额并禁止提交。
4. 向固定 Gas 地址充入无价值原生测试币，再批准冻结报价。
5. 持久保存并广播一笔 Gas 补款与一笔 Token 提取，分别等待最终确认。
6. 任务到最终 `complete`，目标 Token 余额与批准数量精确一致，整库深度完整性检查通过。

测试使用两个 localhost URL 访问同一个 Anvil 节点，仅用于执行本版双客户端路径，**不构成两个独立供应商一致性的运营证据**。没有真实资产、用户钱包、主网交易或 L2 收款/提取验收。测试使用临时 EVM snapshot 并在结束时回滚。

可复现入口：

```bash
# 先启动仅绑定 loopback、无 fork 的一次性 Anvil；不得指向公开网络。
# 将 LocalTestToken 的 deployedBytecode 写入临时文件，内容以 0x 开头。
export GY_TEST_CRYPTO_RPC=http://127.0.0.1:19144
export GY_TEST_CRYPTO_TOKEN_CODE=/tmp/guangyue-local-token-bytecode.txt
cd backend
go test ./internal/controlplane -run '^TestCryptoAnvilPaymentGasAndTokenExecution$' -count=1 -v
```

可用 `forge inspect --use 0.8.30 --evm-version paris --root <临时构建目录> <LocalTestToken.sol路径>:LocalTestToken deployedBytecode` 编译测试字节码。只使用隔离测试源码和目录；该命令输出的是运行时字节码，不是部署交易字节码。

缺少两个环境变量时，此测试会跳过。`SKIP` 不能写成 Anvil 通过；本节记录的是已配置上述无价值执行环境后的实际 PASS。

## 已覆盖的收款与持久化场景

实现与测试文件：[`crypto_payments_test.go`](../../../backend/internal/controlplane/crypto_payments_test.go)、[`crypto_wallet_test.go`](../../../backend/internal/controlplane/crypto_wallet_test.go)、[`payment_crypto_boundary_test.go`](../../../backend/internal/controlplane/payment_crypto_boundary_test.go)。

| 场景 | 已验证断言 |
| --- | --- |
| 金额与报价 | 6/18 位原子数量精确计算、可支付精度取整、非法输入、手动汇率期限；免费套餐不分配地址 |
| 地址分配 | 同操作幂等、独立数据库连接并发、重启恢复、只在实际付款请求分配、切换资产保留旧地址、同资产重复入口受限 |
| 权限与秘密 | 成员/owner 边界、他人订单只读准入、管理员重新验证、RPC 凭证不回显、配置操作重放、恢复门未完成不得分配 |
| 部分与最终付款 | 少付后补足、最终确认才认款、重复扫描不重复开通、未最终确认金额不冒充已确认金额 |
| 真实时间边界 | 截止前上链但截止后发现仍可按冻结报价处理；晚付、取消后款和缺失 receipt 不自动开通 |
| 配置与历史 | 关闭新收款配置不丢弃已有付款单及其历史核对 |
| 错币/错链 | 使用实际 chain/contract/atoms 保存待核对证据，不按 invoice 单位错算，不自动授予权益 |
| 重组 | 跨 invoice 的 receipt 日志重排不重复认款；最终性被推翻时冻结该链，阻断新确认及待履约开通 |
| 渠道边界 | 被替换的支付意图不能重新抢占订单；crypto 收款不能用法币手动退款确认冒充链上退款；恢复中的非 Stripe 退款不误调 Stripe |
| 数据库 | SQLite 与真实 PostgreSQL 分组都有业务断言，包含 invoice 独立连接并发；新记录纳入迁移、快照和完整性关系 |

这些测试证明的是当前实现选定的资金约束。早期设计中独立 `order_fundings`、通用责任账或统一 outbox 尚未整体交付，不能把当前复用收款/履约流程的测试改名为这些新模型已验收。

支付模块测试为 [`payment_module_test.go`](../../../backend/internal/controlplane/payment_module_test.go)。已在 SQLite 和真实 PostgreSQL 验证：只读令牌不会关闭支付；首次管理接管会关闭，管理员重新开启后，重复接管请求和重启不会覆盖其选择；成员和错误管理员密码不能修改开关。关闭模块会拒绝新购买、充值、兑换、invoice 与提取预览，以及支付渠道启用，但历史 invoice 可读、已收款订单继续履约、已批准提取仍只执行原来的两笔交易。

## 已覆盖的资金任务与 RPC 场景

实现与测试文件：[`crypto_sweep_test.go`](../../../backend/internal/controlplane/crypto_sweep_test.go)、[`crypto_evm_test.go`](../../../backend/internal/controlplane/crypto_evm_test.go)、[`crypto_sweep_integrity_test.go`](../../../backend/internal/controlplane/crypto_sweep_integrity_test.go)。

- 广播端返回 HTTP 503，但链已经接受 raw transaction；恢复后重播相同 raw/hash/nonce，不生成第二笔转账。
- 数据库先保存签名交易再广播；补 Gas 最终确认后才继续 Token 交易。
- 独立连接创建幂等、执行租约、报价一次消费、安全结束任务，以及不得丢弃待确认交易。
- 费用上限、Gas 资金不足时保留预览；冻结链、价格上涨超原报价时停止新签名。
- 持久签名 raw、nonce、批准目标/金额与 receipt 篡改检测，revert 不冒充成功。
- 启动及恢复核对批准报价、完整明细集、独立资金地址派生和持久签名；`confirmed/reverted` 必须匹配加密保存的最终 receipt 凭据，单独改状态或区块字段不能通过。
- 已完成任务的加密最终凭据可随快照跨数据库复制；合法失败及安全结束保持可恢复。旧版热钱包尚无资金地址记录时可升级，已有归集任务缺失该记录则拒绝。
- 两 RPC 的链 ID、genesis、区块、余额、receipt 不一致，以及缺失 finalized 时不自动降级放行。
- L2 自动操作强制拒绝；不能以配置勾选替代 L1 最终性证据。
- 不足 Gas 的 estimateGas/state override 回退和预算核对；Token 转账、补差额与补款交易费用分别计算。

本版资金交易采用 EIP-155 legacy 编码。双 RPC 的 gasPrice、estimateGas 取较大结果并保留约 20% 上限余量；`total_gas_atoms` 不重复加入补款本金。尚无 EIP-1559 type-2 与 L2 L1 数据费用模型的执行验收。

## 已覆盖的浏览器交互

本轮浏览器输出保留在仓库之外的本地验收目录，文件名含 `fixture`，明确标注为隔离数据，未使用生产密钥或广播真实交易。临时 Vite harness 已移出产品源码并关闭服务。

- 选择付款方式不分地址；显式请求失败后沿用操作标识；页面刷新恢复已有 invoice，不重新 POST，不开启外部支付空白窗口。
- 少付显示剩余 2.50 等精确数量；`confirming` 隐藏可付款 QR；`paid` 不显示套餐已生效，只有订单完成才显示开通。
- 钱包 QR 使用服务端付款数据，交易所 QR 仅含地址；网络/币种/合约与地址同屏可核对。
- 390px 页面与提取弹窗无横向溢出；桌面、手机、密码关闭/清除和只读钱包能力门有效。
- 余额 RPC 失败保留成功快照，BSC 大整数/18 位小数不发生 float 截断。
- Gas 不足时展示准确差额并禁确认；每 30 秒更新估算，输入密码后锁定报价；创建任务只提交 quote/password/operation_id。
- 广播后保留等待确认状态；安全结束需密码，不抹去已发生交易或费用。
- 管理员只读查看他人 invoice；未知资产显示原始 atoms/chain/contract，不套用当前套餐付款币的精度。

前端单元还覆盖所有新增 commerce 路径在选择子站后仍留在主站资金请求域、仅保存公共 invoice 操作标识的 sessionStorage 恢复，以及 task 汇总不一致时不能显示完成。

## 常用复验命令

以下是复验入口。执行结果必须另记 PASS/FAIL/SKIP；列出命令不等于已运行完整仓库检查。

```bash
cd backend
go test -race ./internal/controlplane -run '^TestCrypto(Payment|Invoice|Wallet)' -count=1
go test -race ./internal/controlplane -run 'TestCrypto(Sweep|Signing|EVM|Estimate)' -count=1
# 先给一次性 PostgreSQL 环境配置 GY_TEST_POSTGRES_DSN，再运行真实 PG 分组。
go test -race ./internal/controlplane -run '^TestPostgresBehaviors/(crypto_payment_|crypto_invoice_|crypto_sweep_)' -count=1 -v
```

```bash
cd frontend
npm test
npm run build
```

完整交付仍使用仓库根目录 `bash scripts/check.sh`，配合完整 Go race/vet、Lite/Pro 构建和安装升级检查。Pro 的 PostgreSQL/Redis 环境必须实际配置；普通 SQLite 测试不会因为设置了 DSN 自动变成 PostgreSQL 测试，需运行已注册的 `TestPostgresBehaviors/...` 分组。数据库服务未配置造成的 skip 必须列出。

## 尚待独立验收或实现

| 项目 | 当前边界 / 下一步 |
| --- | --- |
| 最终发行检查 | 最终代码的本机全仓检查已通过；由主任务继续核对 GitHub CI 安装升级、Lite/Pro 安装包、校验和、SBOM 与正式发布结果 |
| 正式 Ethereum/BSC 运营 | 未使用真实资产验收；需实际独立供应商、合约/精度、finalized 行为、受控首单、Token 净到账和手续费证据 |
| L2 | 收款、补 Gas、提取均强制关闭；需独立实现 L1 最终性和完整费用模型后再验收，不得拿本地 Anvil 当作通过 |
| 链上退款 | 自动/部分/额外款退款、凭证唯一核销与责任账未实现；不要求把未支持的退款行为伪装成本轮成功用例 |
| 钱包身份与外部签名 | OKX 绑定、MPC/智能账户、独立签名器和外部地址预留服务未实现 |
| 多站资金权威 | 无跨部署共享分支/中心 claim；现有 schema 唯一约束不能证明跨站全局唯一 |
| 市场汇率与资产扩展 | 实时汇率、兑换、原生币付款、跨链桥与任意 Token 接入未实现 |
| 长期恢复与规模 | 继续进行跨部署完整备份恢复、长时间停机补扫、大地址池吞吐与可观测性演练；钱包助记词备份不能替代运行数据库/nonce/扫描证据 |

正式运营出现最终性冲突、金额/nonce/签名证据不一致或恢复高水位无法核对时，保留历史事实并停止相关新资金动作。不得为了通过检查而降低确认标准，也不能用数据库回滚抹掉链上已经发生的交易。
