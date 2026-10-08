# 加密货币支付实施设计

状态：**设计完成，支付能力未启用；本文不是链上支付已实现的说明。**

资料核验日期：2026-10-08。本次只编写设计，没有接入真实钱包、配置私钥、扫描链、部署合约或发送交易。下面的表结构、接口、状态和代码均为待实施方案，不是现有生产接口。启用前须重新核验发行方资料与合约元数据，并完成本文的分阶段验收。

## 1. 产品范围与默认选择

用户从已有套餐订单选择“加密货币”，再选择**资产与网络**，获得专属收款地址、应付数量、二维码和支付截止时间。系统依次显示“等待付款 → 已发现转账 → 等待网络确认 → 已收款，正在开通 → 已开通”。管理员从现有交易模块查看异常款、退款与链同步状态，不新增一套与订单割裂的管理中心。

首批链适配器覆盖 Ethereum `1`、BNB Smart Chain `56`、Arbitrum One `42161`、OP Mainnet `10`、Base `8453`。支持范围以第 3 节**精确合约白名单**为准，不按币名自动识别，不承诺每条链都存在发行方原生 USDT。Base 首批只开放原生 USDC。

实施默认值：

- 新安装和升级均 `enabled=false`；现阶段界面仅“加密货币支付 · 未启用 / 规划中”，不得展示可付款地址、成功测试标记或模拟收款二维码。
- 推荐低手续费网络的原生 USDC；Ethereum USDT/USDC 可选；Binance-Peg 与 USDT0 必须显示资产形态，分别启用。
- 使用每张 invoice 独立、永不复用的收款地址。报价用精确数量，不通过金额尾数猜订单。
- 只接受白名单 ERC-20 的最终确认 `Transfer`；原生 ETH/BNB 仅用于手续费，不是本期支付资产。
- 先实现“直接支付已有订单”，不顺带开放加密货币钱包充值、兑换、提现、跨链桥或自动原路退款。
- 默认达到链的最终确认条件后才开通套餐。交易广播成功、钱包截图、浏览器回跳、单个 Webhook、`latest` 或 mempool 均不是收款凭证。

## 2. 与现有系统衔接

当前代码入口可作为重构锚点，但实施时应以最新版本重新核对：

| 现有对象 | 当前用途 | 本设计的处理 |
| --- | --- | --- |
| `commerce_orders`、`commerce_orders.go` | 固定售价与套餐快照，管理开通、结算旧授权和退款 | 保留权益流程；将付款状态与权益状态分离，所有支付渠道竞争同一笔订单的资金归属 |
| `payment_providers` | 独立配置，敏感配置由 Vault 加密 | 新增未来的 `evm_crypto` 适配器；链和资产配置规范化，不能把钱包助记词塞进 `secret` |
| `payment_attempts` | 渠道尝试、CNY 分金额、幂等请求 | 保留 CNY 订单金额；一笔 crypto attempt 对应一张 invoice，不存 token 原始数量到 `amount INTEGER` |
| `payment_events` | 按提供商事件号、载荷哈希去重 | 仅作为通知入口审计；另建链事件事实表，不能用提供商 ID 或载荷哈希代替链事件唯一性 |
| `payment_receipts`、`payment_receipts.go` | 新支付内核按商户账号域和上游交易号建立唯一收款，持久恢复 | 复用“事件与资金事实分离”的原则；crypto 的事实身份改为链事件键，不按多个网关拆出多份收款 |
| `payment_refunds`、`payment_refunds.go` | 独立退款责任、审批、平台处理结果 | 延伸为按精确链资产和原子数量退款；链最终确认不能用平台 API 返回成功替代 |
| `money_transactions.event_key`、`postMoney` | 站内 CNY 余额与成对分录，同事务记账 | 保持现有余额语义；新增分资产外部收款账本，真正变更站内余额时才调用 `postMoney` |
| `commerce_outbox` | 已有持久队列表结构 | 为发货补业务唯一键和消费幂等表，重启和重复消费不能再次开通 |
| `confirmExternalOrder` | 经 HTTP 测试请求调用订单动作 | 提取可在业务事务中使用的付款归属服务；不要让“收款提交后再调一次 handler”成为唯一发货保障 |

必须保持或补齐的集成边界；本轮正在升级的法币支付内核已有部分对应能力，实施 crypto 时应复用并验收，而非另建旁路：

1. 将订单级唯一付款归属用于**余额、易支付、其他 Webhook 和 crypto 所有渠道**。只在 crypto 表里去重，无法阻止用户在另一渠道同时支付同一订单。
2. 外部款不冻结站内余额。当前 `validateCommerce` 已将冻结订单核对限制为未绑定在线支付的 `provisioning` 订单，crypto 必须保持这一语义，不能要求外部支付订单也有对应 `held`。
3. 不能把链上 `uint256` 数量强转成 Go `int64`、SQL `INTEGER` 或 JavaScript `Number`。现有账本是 CNY 分，并非多币账本。
4. 取消订单、报价到期或关闭渠道不会抹去已收到的款项。继续使用新支付内核“先记录唯一收款、再判断履约或退款”的原则，不能恢复成“已到期就拒绝回调”。
5. 必须保留站内余额退款和外部收款退款的区别。crypto 先选择退款方式并登记唯一退款责任，不能既退余额又发币；未广播、未最终确认的链上退款不能标记“已退款”。

## 3. 网络与资产白名单

### 3.1 首批候选清单

以下是本次从发行方、项目方和 BNB Chain 官方资料核验出的**设计候选**。所有条目初始停用。`decimals` 是启动验收的预期值，本文没有通过真实 RPC 验证链上返回值；正式启用时必须核对。

| Chain ID / 网络 | 前台必须显示的名称 | Token 合约地址 | 预期 decimals | 发行 / 跨链形态 | 来源 |
| --- | --- | --- | --- | --- | --- |
| `1` Ethereum | USDT · Ethereum | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 6 | Tether 在 Ethereum 发行的 USD₮ | [Tether 支持协议][tether] |
| `1` Ethereum | USDC · Ethereum | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | 6 | Circle 原生 USDC | [Circle 合约表][circle] |
| `56` BNB Smart Chain | USDT · BNB Smart Chain · Binance-Peg | `0x55d398326f99059fF775485246999027B3197955` | **18** | Binance-Peg USDT，非本设计认定的 Tether 原生发行 | [BNB 官方资产目录][bnb-assets]、[Binance 抵押资产页][binance-peg] |
| `56` BNB Smart Chain | USDC · BNB Smart Chain · Binance-Peg | `0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d` | **18** | Binance-Peg USDC，非 Circle 原生 USDC | [BNB 官方资产目录][bnb-assets]、[Binance 抵押资产页][binance-peg] |
| `42161` Arbitrum One | USDC · Arbitrum One | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` | 6 | Circle 原生 USDC | [Circle 合约表][circle] |
| `42161` Arbitrum One | USDT0 · Arbitrum One | `0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9` | 6 | USDT0；该地址列于项目部署表，不能再并列创建一个“旧 USDT”重复资产 | [USDT0 部署][usdt0-deploy]、[部署 API][usdt0-api] |
| `10` OP Mainnet | USDC · OP Mainnet | `0x0b2C639c533813f4Aa9D7837CAf62653d097Ff85` | 6 | Circle 原生 USDC | [Circle 合约表][circle] |
| `10` OP Mainnet | USDT0 · OP Mainnet | `0x01bFF41798a0BcF287b996046Ca68b395DbC1071` | 6 | USDT0 OFT 体系，不等同于历史桥接 USDT | [USDT0 部署][usdt0-deploy]、[部署 API][usdt0-api] |
| `8453` Base | USDC · Base | `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913` | 6 | Circle 原生 USDC | [Circle 合约表][circle] |

**明确不纳入首批：**

- Base 上任意名为 USDT、USDT0 或类似名称的代币：本次核验的 Tether/USDT0 官方清单没有为 Base 提供可批准的 USD₮/USDT0 Token 条目，因此不填写推测地址，不显示支付选项。
- Arbitrum 的 USDC.e、OP 的历史桥接 USDC、Base 的 USDbC，以及 OP 的旧桥接 USDT：与表中资产不同，默认不接受；若未来确需支持，必须新增明确的桥接资产 ID、官方桥映射来源和审核记录。
- BEP-2、TRC-20、其他同名代币、测试网代币，以及 USDT0 的 OFT Adapter、OFT、Safe、Composer 地址：这些不是本表中的 ERC-20 收款 Token 合约。

Tether 的支持协议页可能列出“BNB Smart Chain”，但本次该段给的是 **XAU₮** 合约，不能据此推导 BNB 上常见的 USDT 就是 Tether 原生发行。USDT0 官方 API 的 `native` 分类表示该项目自己的部署分类，也不能直接转换为“发行方在该链原生发行”的产品文案。USDT0 有锁定 / 销毁 / 铸造及跨链消息验证信任假设；[项目架构说明][usdt0-model]应作为管理员启用前的资产说明。

### 3.2 白名单的持久化与启用检查

资产身份为 `(network_namespace, chain_id, token_contract_bytes20)`。`symbol` 只是展示，地址按 20 字节或统一小写规范化用于比较，前台用校验和地址展示。主网和测试环境数据库、密钥域、invoice ID 前缀完全隔离。

资产配置记录 `asset_id / revision / chain_id / contract / decimals / representation / issuer / source_urls / verified_at / approved_by / enabled_new_invoices`。链配置记录 Chain ID、创世区块 hash、L1 Chain ID、RPC 供应商标识和最终确认策略版本。合约迁移不能覆盖旧记录，旧 invoice 永久引用自己的资产、报价及策略快照。

正式启用向导必须执行：

1. 两个独立 RPC 供应商验证 `eth_chainId`、创世块和当前规范链一致；同一供应商的两个域名不算独立来源。
2. 在已确认区块上验证合约 `eth_getCode` 非空、`decimals()` 与审批值一致；校验项目公布地址、代理合约及升级机制。仅有 `symbol() == USDT` 无证明力。
3. 记录当前代码 / 代理实现元数据；代理升级或 decimals 改变触发暂停新报价和告警，不能不加分析地将所有代理升级视作假币，也不能自动接纳新实现。
4. 资产余额、transfer 行为、暂停 / 黑名单能力、手续费代币预算、限额、报价源、最终确认检查全部通过。
5. 管理员明确选择 Binance-Peg / USDT0 等非发行方原生形态。客户看到的名称、网络和合约必须与选择一致。

## 4. 地址、报价与用户支付

### 4.1 每张 invoice 的独立地址

一张 invoice 固定一个订单、一条链、一个 Token 合约和一个接收地址。优先使用隔离签名器预生成的地址池，或只导出分支扩展公钥的受控 HD 地址分配服务。面板只持有 watch-only 描述符、派生索引和地址，不持有种子或子私钥。

- 地址分配与 invoice 插入在同一数据库事务中完成；`(tenant_id, operation_id)` 返回原 invoice，断线重试不再次消耗地址。
- 已分配地址永不重新分给其他 invoice、用户或商户，即使 invoice 已到期、取消、删除展示或退款。保留历史地址 tombstone 和扫描起点。
- 默认按租户和链使用不同派生分支，避免跨链同地址误付被误归订单；不能让两个独立部署同时管理同一地址池。备份恢复也必须与签名器的分配高水位核对。
- 从正确链、正确合约转入该地址即可匹配；付款人可能从交易所、智能钱包、聚合器出款，不能强制 `tx.from == ERC20 Transfer.from`，也不能把 `from` 当作用户身份。
- 不使用“固定收款地址 + 29.001 / 29.002 的金额尾数”作为默认方案。小数舍入、交易所手续费、并发和重复汇款会碰撞。
- 可在后续增加经审计的支付路由合约，事件携带 invoice ID；这仍需接收资产实际到账、事件归属和唯一资金锁，不能仅凭路由事件文字发货。本期不部署此类合约。

### 4.2 精确金额与汇率快照

订单仍以 CNY 分计价。报价保存 `order_amount_minor`、币种、`rate_num/rate_den`、来源、获取时间、有效期、费用规则版本、token decimals、`expected_atoms` 和完整报价指纹。

定义 `rate_num/rate_den` 为“每一整枚 Token 对应多少 CNY 分”的最终报价率，则：

```text
expected_atoms = ceil(order_amount_minor × 10^decimals × rate_den / rate_num)
```

所有中间运算使用任意精度整数 / 有理数。Go 用 `math/big.Int`，JSON 用十进制**字符串**，前端只格式化字符串或 `BigInt`。每个事件数量须为 `[0, 2^256-1]` 的规范整数；invoice 上限另设，不因 ABI 容量很大而允许超大订单。禁止浮点、科学计数法入库、四舍五入后比较金额和跨资产直接求和。

正式报价源的选择独立配置，不假定 USDT/USDC 永远等于 1 USD。报价源过期、来源价差超阈值、严重脱锚或手续费超预算时停发新 invoice。冻结后的应付 token 数量不因收款确认期间汇率变化而改变。网络手续费通常由付款人另付，不擅自从应付金额扣除；交易所扣除提现费导致少付须显示补足额度。

同一订单只保留一张可继续付款的 crypto invoice。切换币种 / 网络前关闭旧报价但保留其收款监控；如果旧地址已有待确认转账，不静默换网，改为提示等待或人工处理。切换渠道仍由订单级资金唯一锁兜底。

### 4.3 二维码与手机流程

按 [ERC-681][eip681] 生成 Token `transfer` 请求，**始终显式包含 Chain ID**：

```text
ethereum:<TOKEN_CONTRACT>@<CHAIN_ID>/transfer?address=<INVOICE_ADDRESS>&uint256=<EXPECTED_ATOMS>
```

这里只给模板，不提供真实可支付示例。Token 合约是 URI 的目标；invoice 地址是 `address` 参数；不得把 `value` 当作 Token 数量或误生成 ETH 转账。URI、二维码、复制按钮和网络名称都必须来自同一服务端 invoice 快照。

不支持 ERC-681 的钱包保留“复制地址 / 复制数量 / 查看网络与合约”方案；说明用户需在钱包确认网络。WalletConnect / 浏览器钱包是后续独立功能；前端提交 tx hash 只能加速查找，不改变收款状态。

移动端一个步骤只呈现当前需要的选择：先选网络，再看明确的资产形态和应付数量。到期后收起“立即支付”按钮，但保留历史地址与转账查看，显示“请勿继续付款，已付款可查询处理状态”。任何时候均不展示未经核实的“已到账”。

## 5. 观察、确认与防重复的边界

### 5.1 可信事件的构成

Indexer 按区块扫描白名单 Token 的 `Transfer(address,address,uint256)` 日志，WebSocket / 第三方 Webhook 仅用于及时唤醒。确认至少检查：

1. RPC 实际链 ID、网络和白名单一致；合约地址、事件 topic、ABI 长度、收款地址及 amount 精确匹配。
2. 交易 receipt 存在且 `status == 0x1`，日志存在于该 receipt，`transactionHash/blockHash/blockNumber/logIndex` 一致，`removed != true`。
3. receipt 所在区块属于规范链，达到本 invoice 固定的最终确认策略。`eth_getTransactionByHash` 有返回不等于交易成功。
4. 至少两个独立 RPC 对规范区块 hash、receipt 内容和最终确认边界达成一致。配置三个供应商时采用 2/3 一致结果，隔离落后 / 分叉来源并告警；不足法定人数停止自动发货。
5. 只计入最终确认且归属此 invoice 的事件；gas 充值、自有归集转账、未知 Token、零额 spam 和错误链转账不能计入订单支付。

系统不信任客户端或商户 Webhook 提供的 `amount`、`decimals`、`paid=true`。第三方通知必须签名验真、限流、持久化并校验重放，但签名只证明消息来源；实际链收款还须由 Indexer 验证。`eth_getLogs`、receipt 与 block 数据使用供应商能力探测、分块、重试及完整性核对；BNB 官方公共 RPC 可能禁用 `eth_getLogs`，不能据此把空结果当作“没有付款”。

### 5.2 四层唯一性

| 层级 | 唯一键 / 约束 | 解决的问题 |
| --- | --- | --- |
| 链事件 | `(chain_id, tx_hash, log_index)` | 同一真实 Transfer 被多个扫描器、Webhook、回扫重复发现 |
| 归属 claim | 同一个链事件键只能有一个 invoice / tenant / order | 同笔款被两个 invoice、两个支付网关或两个租户认领 |
| 订单付款 | `(tenant_id, order_id)` 唯一资金归属，且 `attempt_id` 唯一 | 两次真实付款、两个渠道回调或多 invoice 同时开通一个订单 |
| 账本与发货 | 不变 `event_key`，以及 `fulfill:<tenant>:<order>` 唯一 outbox / consumer key | 事务重试、消息重投、进程崩溃导致重复入账和重复套餐生效 |

事件键必须包含链 ID，不能只用 tx hash；`logIndex` 不能省略，因为一笔交易可含多个有效 Transfer。第三方 provider ID、Webhook ID、invoice ID 都不能进入链事件的唯一身份，让相同事实换个来源就变成新款。

**一笔交易的多个日志不等于多笔订单。** 发往同一 invoice 地址的多个独立 Transfer 精确相加，invoice 达标只生成一次订单付款和发货事件。同笔批量交易如果实际转账到两个不同 invoice 地址，可以分别支付两个订单，但每条日志只能认领一次，不能让其中一条日志被两单重复使用。一条 Transfer 不拆给多个订单；本期也不把少付余额自动挪给另一张 invoice。

重复通知执行 `INSERT … ON CONFLICT` 后必须读取已有记录、比对不可变字段及归属。不能仅捕获任意 `unique` 字符串就返回“成功”：若现有事件归另一个订单、金额不同或原事务尚未成功，应记录冲突或读取已提交结果。

### 5.3 Reorg 与重放

保存观察区块 `hash / parent_hash / height`、receipt 原文摘要和确认依据。`removed=true` 是一个信号，不是唯一检测机制；每轮核对区块连接和扫描边界，断链时回退至共同祖先，旧观察标记 `orphaned`，不能删掉审计证据。

- 最终确认前不记“可发货收款”。浅分叉后 invoice 的待确认合计可减少，向用户显示“网络重新确认中”，不继续沿用旧合计。
- 同一 tx 被重新打包时，区块级 `logIndex` 可能改变。保留 observation 的 `block_hash`、receipt 内日志序号及旧新关联，重新验证规范 receipt；旧孤块事件不能参与合计。不要把旧、新日志键都记账。
- 若已入账的“最终确认”区块竟不再规范，立刻冻结该链的自动结算 / 发货，标记 `reconciliation_required`。不能把相同 tx 的新 logIndex 自动当作第二笔收款，也不能直接删除已存在的账本。人工审核通过有唯一键的冲正 / 恢复事件处理，对已开通权益按现有结算与撤权流程处置。
- 签名者重新提交同 nonce 的加价替代交易时只认最终规范 receipt。sender + nonce 可辅助关联，但不能代替链事件主键。

## 6. 最终确认和 L2 的 L1 策略

采用逐链可版本化的策略适配器，不使用“所有网络固定 12 个块”的统一规则。报价保存策略版本，运行时可以收紧不能静默降低旧 invoice 的确认要求。

| 网络 | 默认可结算条件 | 暂停 / 降级条件 |
| --- | --- | --- |
| Ethereum `1` | 两个独立 RPC 同意 receipt 及规范 block hash；收款块在共识 `finalized` 边界内 | finalized 停滞、来源分歧或标签能力不可靠时等待，不退回 latest |
| BNB `56` | 两个独立 RPC 同意 BSC **经济最终性**边界及 receipt；按 [BSC Finality API][bnb-finality] 验证 `finalized` 标签语义 | 不把 `eth_getFinalizedHeader` 的概率最终性回退结果冒称经济最终性；一期不自动切换成固定确认数 |
| Arbitrum One `42161` | `finalized` 覆盖收款 L2 块，关联批次已发布到 Ethereum 且该 L1 块 finalized；见 [Arbitrum finality / reorg][arb-tags] | Sequencer 软确认、仅 safe、批次未发布、L1 验证不足时不可结算 |
| OP Mainnet `10` | L2 `finalized` 且批次所在 Ethereum 块 finalized；保存 L1 依据 | unsafe / safe 仅展示进度，不能发货；L1 停滞则等待 |
| Base `8453` | L2 `finalized` 且对应 L1 batch finalized，策略单独登记并按 [Base 文档][base-finality] 验收 | Flashblocks / L2 inclusion 不作为支付完成 |

L2 适配器保存 `l1_origin`、**批次实际发布**的 L1 块及 finalized checkpoint。L1 origin 只是执行引用，不一定就是批次发布位置，不能仅凭 origin 已 finalized 就确认收款。生产能力要求能通过自有 / 独立索引器映射发布批次，或使用已验收的链节点 finalized 语义并交叉核验 L1；通用 RPC 若缺少可信映射，保持等待，不伪造关联。

Arbitrum 的 `finalized` 在存款确认意义上对应父链数据最终性；Rollup assertion 确认和 L2→L1 提款可用还存在挑战 / 结算过程，不能混称。OP / Base 的标准桥提款等待期也不等同于一笔 L2 支付必须等待的时间。普通套餐开通采用上表的存款确认等级；**把收入桥回 L1** 是单独的资金操作，按对应桥的完整结算 / 挑战条件执行，本期不自动桥接。

前台用“已发现 / 等待网络最终确认”，展示已用时间与正常情况下的估计范围，不承诺固定秒数。若未来提供小额快速开通，必须作为显式风险策略、限额和可撤回权益的独立版本，首期不开启。

## 7. 数据模型与迁移草案

以下是逻辑 SQL 草案。正式实施拆成按当时序号命名的增量迁移，适配项目 SQLite / PostgreSQL 持久层。不能把这段直接当作已执行迁移；生产代码须增加字段类型、金额编码、引用完整性和版本迁移测试。

```sql
-- 订单级资金锁：由所有支付渠道共用，不仅是 crypto。
CREATE TABLE order_fundings (
  tenant_id TEXT NOT NULL,
  order_id TEXT NOT NULL,
  attempt_id TEXT NOT NULL UNIQUE,
  source TEXT NOT NULL,                 -- wallet / epay / evm_crypto
  event_key TEXT NOT NULL UNIQUE,
  amount_minor INTEGER NOT NULL,        -- 订单币种的最小单位，现阶段 CNY 分
  currency TEXT NOT NULL,
  created INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, order_id)
);

CREATE TABLE crypto_assets (
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL,
  contract TEXT NOT NULL,               -- 规范小写 0x + 40 hex
  decimals INTEGER NOT NULL,
  representation TEXT NOT NULL,        -- issuer_native / binance_peg / usdt0
  revision INTEGER NOT NULL,
  enabled_new_invoices INTEGER NOT NULL DEFAULT 0,
  doc BLOB NOT NULL,                    -- 来源、启用审批、验证元数据
  UNIQUE (chain_id, contract)
);

CREATE TABLE crypto_addresses (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  chain_id INTEGER NOT NULL,
  address TEXT NOT NULL,
  signer_pool_id TEXT NOT NULL,         -- 仅隔离签名器标识，无私钥
  derivation_index INTEGER NOT NULL,
  birth_height INTEGER NOT NULL,
  invoice_id TEXT UNIQUE,               -- 分配后永不复用
  UNIQUE (chain_id, address),
  UNIQUE (signer_pool_id, derivation_index)
);

CREATE TABLE crypto_invoices (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  order_id TEXT NOT NULL,
  attempt_id TEXT NOT NULL UNIQUE,
  asset_id TEXT NOT NULL REFERENCES crypto_assets(id),
  address_id TEXT NOT NULL UNIQUE REFERENCES crypto_addresses(id),
  operation_id TEXT NOT NULL,
  request_fingerprint TEXT NOT NULL,
  state TEXT NOT NULL,
  expected_atoms TEXT NOT NULL,         -- uint256 规范十进制字符串
  received_final_atoms TEXT NOT NULL DEFAULT '0',
  quote_expires INTEGER NOT NULL,
  confirmation_deadline INTEGER NOT NULL,
  version INTEGER NOT NULL,
  snapshot BLOB NOT NULL,               -- 不变金额、汇率、资产、确认策略快照
  created INTEGER NOT NULL,
  updated INTEGER NOT NULL,
  UNIQUE (tenant_id, operation_id)
);
CREATE UNIQUE INDEX crypto_one_open_invoice ON crypto_invoices(tenant_id, order_id)
  WHERE state IN ('awaiting_payment','partial','confirming');

-- 事实表全局去重，不能用 tenant/provider 把同一链事件隔成多份。
CREATE TABLE crypto_transfers (
  chain_id INTEGER NOT NULL,
  tx_hash TEXT NOT NULL,
  log_index INTEGER NOT NULL,
  token_contract TEXT NOT NULL,
  from_address TEXT NOT NULL,
  to_address TEXT NOT NULL,
  amount_atoms TEXT NOT NULL,
  canonical_observation_id TEXT,
  state TEXT NOT NULL,                  -- observed / confirming / finalized / orphaned / disputed
  version INTEGER NOT NULL,
  PRIMARY KEY (chain_id, tx_hash, log_index)
);

CREATE TABLE crypto_observations (
  id TEXT PRIMARY KEY,
  chain_id INTEGER NOT NULL,
  tx_hash TEXT NOT NULL,
  log_index INTEGER NOT NULL,
  block_hash TEXT NOT NULL,
  block_height INTEGER NOT NULL,
  block_timestamp INTEGER NOT NULL,
  receipt_log_ordinal INTEGER NOT NULL,
  receipt_digest TEXT NOT NULL,
  canonical INTEGER NOT NULL,
  finality_evidence BLOB NOT NULL,
  UNIQUE (chain_id, tx_hash, log_index, block_hash)
);

CREATE TABLE crypto_transfer_claims (
  chain_id INTEGER NOT NULL,
  tx_hash TEXT NOT NULL,
  log_index INTEGER NOT NULL,
  tenant_id TEXT NOT NULL,
  invoice_id TEXT NOT NULL REFERENCES crypto_invoices(id),
  order_id TEXT NOT NULL,
  created INTEGER NOT NULL,
  PRIMARY KEY (chain_id, tx_hash, log_index),
  FOREIGN KEY (chain_id, tx_hash, log_index)
    REFERENCES crypto_transfers(chain_id, tx_hash, log_index)
);

-- 外部资产账本，不把 token atoms 混进 CNY 钱包余额。
CREATE TABLE payment_ledger_events (
  id TEXT PRIMARY KEY,
  event_key TEXT NOT NULL UNIQUE,
  tenant_id TEXT NOT NULL,
  invoice_id TEXT,
  order_id TEXT,
  kind TEXT NOT NULL,
  created INTEGER NOT NULL,
  evidence BLOB NOT NULL
);
CREATE TABLE payment_ledger_entries (
  event_id TEXT NOT NULL REFERENCES payment_ledger_events(id),
  asset_id TEXT NOT NULL,
  account TEXT NOT NULL,
  direction INTEGER NOT NULL CHECK(direction IN (-1, 1)),
  amount_atoms TEXT NOT NULL,
  PRIMARY KEY (event_id, asset_id, account)
);

-- 复用 commerce_outbox；先检查历史重复，再为新主题加确定的业务键。
ALTER TABLE commerce_outbox ADD COLUMN event_key TEXT;
CREATE UNIQUE INDEX commerce_outbox_event ON commerce_outbox(event_key)
  WHERE event_key IS NOT NULL;
CREATE TABLE commerce_consumed_events (
  consumer TEXT NOT NULL,
  event_key TEXT NOT NULL,
  processed INTEGER NOT NULL,
  PRIMARY KEY (consumer, event_key)
);
```

另需 `crypto_chain_state / crypto_scan_cursors / crypto_block_checkpoints / crypto_refunds / crypto_signer_jobs`。它们分别保存链开关及健康状态、分片扫描进度、共同祖先与规范区块 hash、退款责任和状态、签名请求及 nonce / 替换交易历史。扫描游标和退款不能只保存在内存。

数据库设计补充约束：

- `TEXT` 金额不是任意文本：应用层统一校验 canonical decimal、长度、`uint256` 边界；SQLite 不用 `CAST(... AS REAL)`，PostgreSQL 如增加 `NUMERIC(78,0)` 校验也不得把 Go/JSON 降为浮点。金额求和及分录平衡使用任意精度，按资产分别核对。
- 上面 `tenant_id + order_id / invoice_id / address_id / attempt_id` 关系在正式迁移中用复合唯一键和外键保证同租户；只检查 URL 参数不够。单站用户 ID 不能代替租户 ID。
- 一个网络/合约资产可以升级描述版本，但旧 invoice 保存不可变快照；改变 Token 合约必须创建新资产 ID。
- 当前 `payment_attempts.external_ref` 不填裸 tx hash：用 invoice / settlement 的唯一引用，一张 invoice 可收多笔交易，一笔交易也可含多个独立收款日志。
- 索引覆盖 `(chain_id,to_address)`、invoice 状态 / 到期时间、未确认事件、scanner 分片、outbox 下次执行时间。分页、重扫区间和单轮日志数量都要有上限。
- 新增 `payment_confirming` 等订单状态时，同时更新现有活跃订单唯一索引、状态校验、权限、定时过期、备份恢复与账本完整性检查；只改 JSON 文档会留下竞态。

## 8. 原子收款、订单归属与发货

### 8.1 事务边界

网络访问在数据库事务外完成，形成带版本的 `FinalityProof`；进入事务后锁链状态和 invoice，检查 proof 仍对应当前确认版本、链未暂停且事件不可变字段一致。Reorg 处理使用相同链级协调锁；使用带 fencing token 的单链领导者 / 数据库租约避免两个进程同时推进冲突确认边界。进程内 `a.mu` 不能替代数据库约束。

伪码：

```go
proof := observer.VerifyWithQuorum(ctx, chainID, txHash, logIndex)
// receipt/status/allowlist/canonical/finality 全部验完，不能接受浏览器传来的 proof。

withSerializableTx(func(tx Tx) error {
    chain := lockChainState(tx, proof.ChainID)
    require(chain.SettlementEnabled && proof.Epoch == chain.CanonicalEpoch)
    require(proof.NotStale() && proof.CheckpointStillAccepted(chain))

    event := insertOrLoadTransferAndObservation(tx, proof)
    requireImmutableFieldsEqual(event, proof)
    invoice := lockInvoiceByAssignedAddress(tx, proof.ChainID, proof.To)
    require(invoice.AssetContract == proof.Token && invoice.TenantOwnsAddress)

    claim := insertClaimOrReadExisting(tx, event.Key, invoice.ID)
    require(claim.InvoiceID == invoice.ID && claim.TenantID == invoice.TenantID)
    // 重复事件读取原 claim；不能把别人的 claim 当作本次成功。

    postAssetLedgerOnce(tx, "crypto:receive:"+event.Key,
        custody(+proof.Atoms), customerUnapplied(-proof.Atoms))
    // 上述 claim、最终事件、收款分录在一个 SQL 事务内成功或整体回滚。

    total := sumFinalizedClaimsWithBigInt(tx, invoice.ID)
    updateInvoiceConfirmedAmountCAS(tx, invoice, total)
    order := lockOrderOrNone(tx, invoice.TenantID, invoice.OrderID)
    if order == nil {
        markUnfulfillablePaymentForReview(tx, invoice)
        return nil // 订单丢失也保留到账与待处理资产，不回滚真实收款。
    }
    if funding := readOrderFunding(tx, order.Key); funding != nil {
        if funding.AttemptID == invoice.AttemptID {
            updateUnappliedExtraStatus(tx, invoice, total) // 仅处理新超付，不再发货。
            return nil
        }
        markAdditionalPaymentForReview(tx, invoice) // 另一渠道/报价先付款了。
        return nil
    }
    if !eligibleForAutomaticFunding(invoice, total) {
        markPartialOrReview(tx, invoice, total)
        return nil // 已收到的资产仍有账，少付/晚付不是丢弃收款。
    }
    if !order.CanAcceptPayment() || !order.SnapshotMatches(invoice) {
        markUnfulfillablePaymentForReview(tx, invoice)
        return nil // 收款事实仍提交，不能因订单已结束而一起回滚真实到账记录。
    }
    insertUniqueOrderFunding(tx, order.Key, invoice.AttemptID,
        "crypto:fund:"+order.Key, order.PriceMinor, order.Currency)
    postAssetLedgerOnce(tx, "crypto:fund:"+order.Key,
        customerUnapplied(+invoice.ExpectedAtoms), salesPending(-invoice.ExpectedAtoms))
    // 超付部分留在 customerUnapplied，不再自动购买第二份套餐。
    markAttemptPaidAndInvoiceFunded(tx, invoice)
    insertOutboxOnce(tx, "fulfill:"+order.Key, "order.payment_verified", order.Key)
    return nil
})
```

SQL 唯一冲突要回读和比较，错误不能吞掉。PostgreSQL 使用行锁 / SERIALIZABLE 和有限事务重试；SQLite 通过当前持久层支持的写事务串行化方式锁定，设置 busy 重试，不能照抄 `FOR UPDATE`。重试沿用原 eventKey、operationID、snapshot，不能临时换 ID 绕过去重。

金额例子：100 USDC 的 invoice 收到 60 + 40 两次转账，产生两条 `crypto:receive`，一条 `crypto:fund`，一个 `fulfill`。随后再收 20 USDC，产生第三条 receive 和 20 的未分配款；不产生第二个 fund / fulfill。使用 `money_transactions` 进行退款到余额时，事件键为 `crypto:refund-wallet:<refund_id>`，与退款责任表同事务写入，不能同时保留可执行的发币退款任务。

### 8.2 发货 outbox

worker 原子认领 outbox，保存 attempt / lease / fencing token。处理权益时，在更新订单 / 用户权益的同一事务中插入 `commerce_consumed_events('order-provisioner', event_key)`。已有事件就读取既有订单状态返回，不能再次执行 `assignPlan`。

因旧配额结算而需要等待时，outbox 只负责将订单**一次**送进持久的 `provisioning` 流程；后续仍由原有权益 reconciler 推进。它不必等待子站上线才完成消息投递，但订单不能提前显示“已开通”。若开通失败，订单保留收款事实并进入 `refund_required`，不能将外部款误转成站内可用余额。

数据库提交后、worker 消费前崩溃，重启继续消费；权益提交后、outbox 标记完成前崩溃，由消费唯一键去重。通知邮件 / 站内信另行 outbox，不让通知失败撤销收款或再次发货。

## 9. 状态与异常款策略

链观察状态、invoice 支付状态、订单权益状态分开储存。用户页面合成一个可读进度，管理员可展开证据。

```mermaid
stateDiagram-v2
  [*] --> awaiting_payment
  awaiting_payment --> confirming: 发现正确网络与资产的转账
  confirming --> partial: 最终确认数量不足
  partial --> confirming: 用户补款
  confirming --> funded: 最终确认足额且订单仍可付款
  funded --> provisioning: 唯一 outbox
  provisioning --> completed: 原有权益流程完成
  provisioning --> refund_required: 无法履约
  awaiting_payment --> expired: 付款窗口结束
  partial --> review: 窗口结束仍不足
  confirming --> review: 超时或证据有分歧
  expired --> review: 后续收到款项
  funded --> review: 额外到款
  refund_required --> refund_pending: 管理员批准
  refund_pending --> refunded: 指定退款方式最终完成
```

该图是产品组合视图，`provisioning/completed` 属于订单，不要求 invoice 再复制一份权益状态。`funded → review` 表示额外款的处理标记，不能回退已经完成的订单付款或再次发货。

| 情况 | 默认动作 |
| --- | --- |
| 少付 | 按同一 invoice 地址累加正确资产的最终确认款，展示差额；截止前可补款。不自动降低售价或按模糊容差足额化 |
| 足额 | 符合报价窗口、最终确认及订单条件时获得唯一资金锁，再开通 |
| 超付 | 订单只按报价开通一次，多出部分建立异常款责任；可申请管理员审核退款，不自动赠送余额 |
| 截止前上链、截止后最终确认 | 已进入 `payment_confirming` 的订单保留确认窗口，按原报价处理；窗口独立于付款截止时间，不因 L1 正常确认较慢直接丢单 |
| 扫描中断，恢复时才发现截止前的付款 | 保存区块时间与原报价；若订单仍具备确认资格可恢复。若已经终态 / 已产生新订单或权益冲突，转人工核对，不绕过订单版本自动恢复 |
| 截止后上链 / 已取消后付款 | 仍认领并记收款账，进入 `late_payment` 审核；不自动复活原订单 |
| 另一网络 / 非白名单同名币 | 不计入本 invoice。只在对应资产与链具备可核验证据时建立误付案件；不许按美元估值直接凑单 |
| 同单另一渠道已经付款 | 新收到的款成为重复付款责任，不再次开通；由管理员选择退款途径 |
| 订单对应用户已停用或归档 | 收款可记事实，不自动给其他用户；进入履约 / 退款审核 |
| 地址收到原生 ETH/BNB | 记为非订单资产发现或手续费余额，不自动充值套餐 |
| Token 被发行方冻结 / 桥异常 | 停止相应资产新报价与归集，保留收款、退款责任和实际可动用性状态 |

付款是否在窗口内以**规范链区块 timestamp**与 quote 快照比较，处理明确的允许时钟偏差，不能用客户端时间或 Webhook 到达时间。报价 TTL 默认沿用订单 15 分钟；已检测到匹配付款可进入独立的确认窗口，例如 60 分钟。达到确认窗口上限只转人工 / 等待，不伪造退款或删除资产；链恢复后的处理规则和管理员动作都留审计。

### 9.1 退款

退款必须由用户申请、管理员批准；多付 / 晚付案件也需明确处理。先计算可退责任与已处理额度，CAS 预留退款额度，之后才生成付款任务。每个 `refund_id` 有唯一方式 `wallet_cny` 或 `onchain_same_asset`、资产、数量、目标地址和费用政策。

- 不能默认原路发给 ERC-20 `from`：那可能是交易所热钱包、路由合约或智能账户。用户提供同链兼容地址并确认，必要时验证地址控制权；交易所地址无法签名时走人工核验，不误承诺“原路”会到个人账户。
- 人民币退款金额按订单及批准规则确定，币数量按明确的原报价或退款报价政策确定并冻结；不可在审核后让浮动汇率悄悄改数量。
- 退款审查、权益撤销 / 配额结算、资金退款分阶段完成。链上退款在真实 receipt 成功并达最终确认后才是 `refunded`；广播失败 / gas 不足 / receipt 未确认分别显示。
- nonce 相同的加价替代交易属于同一退款任务；记录全部 tx hash，最多一笔规范成功退款。取消、重复管理员点击、worker 重试不能生成第二笔付款。
- 链上退款初期可由管理员隔离钱包人工执行，回填 tx hash 后由同一验证器核验；该 hash 不直接证明退款，须匹配已批准资产、金额、目标地址、来源钱包及最终性。

## 10. 签名、Gas 与资金归集

收款观察服务始终不需要私钥。面板配置只展示收款方案、资产、网络、手续费预算、归集状态与只读地址；禁止通过浏览器输入助记词、在数据库明文存储私钥或把种子放进面板备份。

签名器作为单独受限服务 / HSM / 受控钱包运行，mTLS 或等效认证，按租户、网络、合约、调用方法、目标地址及金额上限执行策略。冷库使用离线或多签管理；归集只发往预先批准的 treasury，不允许把管理员表单里的任意 calldata 直接送签。

EOA 收款地址转出 Token 需要该链原生 Gas：Ethereum / Arbitrum / OP / Base 用 ETH，BNB 用 BNB。Gas 加注与 Token 归集分开建任务，记录各自 nonce、预算、估算和最终 receipt；L2 成本还包含 L1 data fee，不照搬主网 gas 模型。

- 在 Token 最终到账之后才考虑按预算补 gas；垃圾 Token、零额日志、小额粉尘不能无限触发资助。
- 设置最低可支付金额、最低归集金额、每地址 / 租户每日 gas 上限与备用余额告警。余额不足不影响“已经收到付款”的事实，只影响可归集性。
- 每条链每个签名地址使用持久 nonce 管理器；签名 / 广播请求使用不可变 operationID。地址分配高水位和退款 / 归集任务与备份恢复协调，防止重新派生或重发。
- 首期只调用已白名单 Token 的直接 `transfer`；无需无限 allowance、approve 或 Permit2。若将来引入路由合约，另行审计授权边界。
- Tether Ethereum USD₮ 的 `transfer` 可能不返回标准 boolean；签名器 / 合约集成按 [Tether 集成说明][tether]使用兼容的安全调用，并以 receipt 与实际 Transfer 验证，不凭某个客户端库返回值认定成功。
- 自有地址之间的归集不是新收入；退款转出不是用户新订单。索引器维护托管地址角色，事件用途由实际方向与授权任务验证。

## 11. 群站隔离、停机恢复与运维

现有架构中套餐资金归主控站，子站负责节点和授权。crypto 第一阶段沿用此边界：主站记录用户订单与收款，挂载子站不重复收费，子站配对令牌不能配置钱包、调用签名器或认领付款。

未来独立商户并入同一个收款服务时引入稳定 `tenant_id`。每个租户的 provider、地址池、报价、订单、ledger、refund、outbox 都显式带租户；成员只能查询自己的 invoice，远程子站代理不转发资金操作。链事实可以全局共享，但 claim 必须全局唯一且所属地址唯一，防止不同 provider / tenant 重复消费同一转账。彼此独立的数据库无法靠本地唯一键互斥，因此禁止共享托管地址池；确需共享时必须使用同一个中心 claim 服务。

三个开关分开：

| 开关 | 作用 | 不能做什么 |
| --- | --- | --- |
| `accept_new_invoices` | 暂停新报价和新地址分配 | 不停止旧地址收款发现 |
| `settlement_enabled` | 因 RPC 分歧、reorg 或风控暂停自动认款 / 发货 | 不删除待核验交易，也不显示付款失败已退款 |
| `signing_enabled` | 暂停退款 / gas / 归集签名 | 不影响只读监控与订单查询 |

Scanner 以区块批次持久保存日志和游标，事务提交后才推进游标。WebSocket 断开、服务重启、被供应商限流都从已持久化位置补扫；固定窗口重叠回扫是补漏措施，不能替代从断点恢复。游标记录区块 hash，检测 reorg 后回到共同祖先。对 provider 的范围 / 数量上限分页，不把截断结果当完整扫描。

地址从分配前保守的 `birth_height` 开始扫描，过期地址继续监听。提供按链 / 区块区间 / invoice 的 backfill 命令，调用同一事件与 claim 管道，禁止“重新导入”时跳过去重。恢复旧备份后先暂停新 invoice、结算和签名，核对签名器分配高水位、最后支付 / 退款记录，再从安全检查点重扫；若恢复点之前的外部发货事实不完整，先重建已消费事件，不能直接发第二份套餐。

需要的运维指标：head / safe / finalized 高度与延迟、RPC 一致率与429/超时、扫描缺口、未确认最老交易、少付 / 晚付 / 超付数量、claim 冲突、资金账与托管余额差异、outbox 重试、gas 库存、签名任务年龄。日志默认脱敏用户标识和报价密钥；API密钥只留服务端。RPC URL 与 explorer URL 分开管理，对管理员录入的 RPC 做 SSRF 防护、TLS验证、重定向限制和超时；前端 explorer 链接必须来自官方域名模板。

## 12. 待实现接口与模块

以下接口仅是契约设计，不表示现在已上线；路径落位时遵循现有 commerce 鉴权和幂等机制。

| 接口 | 权限 / 输入 | 返回 / 行为 |
| --- | --- | --- |
| `GET /api/commerce/crypto/assets` | 登录；按本站支付配置 | 仅返回审核且可报价资产、网络、形态、费率说明与暂停原因；未启用返回 `enabled:false` |
| `POST /api/commerce/orders/{id}/crypto-invoices` | 订单本人；`asset_id, operation_id` | 原子创建 invoice / attempt / 地址分配，返回字符串金额、报价到期时间、URI 和等待状态 |
| `GET /api/commerce/crypto-invoices/{id}` | 订单本人或本站管理员 | 展示金额、观测 / 最终金额、付款及开通进度，支持 revision / ETag |
| `POST /api/commerce/crypto-invoices/{id}/tx-hints` | 订单本人；chain ID、tx hash，限流 | 只接受查找提示并排队，不能携带金额修改支付结果 |
| `POST /api/commerce/crypto-invoices/{id}/refund-requests` | 订单本人；理由、退款偏好 | 生成申请，不能直接签名或改变已支付状态 |
| `GET /api/admin/crypto/health` | 本站管理员 | 链同步、RPC一致性、余额对账、暂停原因 |
| `PUT /api/admin/crypto/config` | 本站管理员；版本 CAS + 敏感操作二次认证 | 只配置链 / 资产 / watch-only方案，禁止接收私钥 |
| `POST /api/admin/crypto/reviews/{id}/resolve` | 管理员；明确动作、证据、operationID | 经过同一资金锁 / 退款状态机处理异常，不直接写 `paid=true` |
| `POST /api/admin/crypto/backfills` | 管理员；有界区间与理由 | 异步只读重扫任务，实际认款仍经标准管道 |
| `POST /api/payments/crypto-hints/{provider}` | 第三方签名通知；限流与重放检查 | 持久化 hint 后快速响应，scanner 异步验证，不能直接开通 |

建议文件边界：

```text
backend/internal/payments/crypto/
  assets.go             // 不可变白名单和版本快照
  amounts.go            // 原子单位、报价有理数运算
  invoices.go           // 地址分配、报价、到期与付款资格
  observer.go           // logs/receipt/RPC quorum 与 backfill
  finality_eth.go       // Ethereum/BSC 策略分别实现
  finality_arbitrum.go
  finality_opstack.go   // OP/Base 分链配置与 L1 证明
  settlement.go         // claim、账本、order funding、outbox 一个事务
  refunds.go            // 审批责任、方式互斥、最终确认
  signer_client.go      // 受限协议，不包含冷私钥
```

`CryptoInvoiceResponse` 的原始数量、报价分子分母和 CNY 最小单位全部为字符串；客户端不能提交应付金额。服务器响应要包含 `asset_revision / finality_policy_revision / invoice_revision`，防止旧页面误用变更后的规则。签名器不复用面板公开 HTTP 路由，也不接受用户 tx hint。

## 13. 实施阶段与验收门槛

| 阶段 | 施工内容 | 完成标准 |
| --- | --- | --- |
| P0：支付内核 | 统一订单 funding 锁、收款 / 履约服务边界、outbox幂等、外部款与余额账区分 | 余额与易支付并发成功最多开通一次；在线开通不制造冻结余额；退款途径互斥 |
| P1：数据与离线观察器 | 新增迁移、整数金额类型、资产清单、fixture RPC、游标 / reorg / claims | SQLite 与 PostgreSQL 同一组并发和崩溃恢复测试通过；生产入口仍停用 |
| P2：无价值测试环境 | 独立测试网 / 本地链代币和签名器沙箱，地址分配与完整 invoice 流程 | 收款、少付补款、退款、归集、恢复演练可复现，主网资金与密钥不可访问 |
| P3：逐链只读验收 | 管理员以后配置正式RPC与watch-only地址，验证 Token、最终性与 L1 证据 | RPC能力、白名单、配额、finality、backfill和告警通过；观察与实际付款开关分离 |
| P4：受控正式收款 | 管理员明确启用某条链/某资产，限额上线 | 从首笔真实订单到最终收款、一次开通、资金对账形成完整证据；逐资产启用，不能一次全开 |
| P5：资金操作扩展 | 隔离签名器、受限归集、人工审核后链上退款 | nonce重试、替代交易、冷库白名单、Gas预算、退款唯一性通过后才启用签名 |

P3/P4/P5 是将来的实施与运营步骤；本轮没有执行。

必须覆盖的验收用例：

1. 同日志由轮询、WebSocket、两个provider和重复Webhook各投递100次：一条最终事实、一条claim、一条receive、一份对应发货。
2. 两个worker并发认领同事件到不同invoice / tenant：只能一个成功，另一个明确冲突；不能都返回已支付。
3. 同订单的两张历史invoice、易支付、余额几乎同时成功：最多一个 `order_fundings`；额外真实款保留责任，不静默丢失。
4. 同tx多个日志分别转60+40到同invoice：只开通一次；同tx真正向两张独立invoice各付足额：每条日志独立归属，不互相阻断或重复。
5. 假币同symbol、错误合约、错误链、revert receipt、伪造log、`removed`日志、测试币、零额日志均不自动发货。
6. 6位与18位精度、uint256边界、超大数量、非法小数、舍入向上、汇率过期 / 脱锚 / 修改报价源不会造成错计或溢出。
7. 临近截止付款、确认跨越截止、迟到Webhook、恢复后补扫、取消后付款、超付和连续补款都有确定归属与用户可读状态。
8. 浅reorg后数量回退；同tx重新打包且logIndex变化不能双计；最终性违背触发暂停与人工对账，不自动再次发货。
9. RPC 2/3一致时使用一致规范证据；1/3、超时、finalized回退 / 停滞、L2已出块但L1 batch未最终确认时不放行。
10. 在claim、账本、funding、outbox事务各位置注入崩溃：只有全部提交或全部回滚；重启后补齐未消费消息，不重复开通。
11. scanner区间超限、日志截断、WS断线、长时间停机、过期地址回款仍可回扫并去重；游标不能越过失败批次。
12. 无权成员、子站token、跨租户invoice、另一用户tx提示、伪造管理员回调均不能取得他人地址详情或修改款项。
13. 管理员重复退款、余额与链上退款竞争、交易所出款地址、替换交易、gas耗尽均不能造成双退或错误“已退款”。
14. 新旧备份恢复、地址派生高水位、已消费发货事件、签名nonce和退款记录核对通过；失去外部事实时默认暂停。
15. 用户端桌面 / 手机在网络断开、长等待和错误网络选择时仍能看到订单、地址、币种、状态与帮助；不依赖二维码作为唯一入口。

上线后的成功标准是可审计的不变量：**每条最终转账最多归一张invoice，每张invoice的有效款只合计一次，每个订单最多一次资金确认和一次对应权益发货；额外资金和退款均有独立责任记录。**

## 14. 官方资料与核验说明

发行 / 合约信息来自发行方或项目官方；BNB候选资产以官方资产目录和 Binance 抵押页交叉核对。区块浏览器只作查看与辅助校验，不能替代发行方白名单。官方网页未来可能更新，实施时须归档来源版本、核验时间、哈希及审批记录。

- [Circle：USDC contract addresses][circle]。本次标准网页reader跳到了无关资源，改读同一官方文档的 `.md` 正文，核验了四条原生 USDC 地址；不引用无关跳转内容。
- [Tether：Supported Protocols and Integration Guidelines][tether]，包括 Ethereum USD₮ 合约与旧版 ERC-20 transfer 返回值说明。
- [USDT0：Contract Deployments][usdt0-deploy]、[官方部署 API][usdt0-api]、[项目架构][usdt0-model]。Token 与 OFT/Adapter/Safe 分开记录；核验清单未给出 Base USDT0。
- [BNB Chain 官方 SDK 资产目录][bnb-assets]，明确 `BINANCE_PEG_USDT / BINANCE_PEG_USDC` 与18位精度；[Binance：B-Tokens collateral][binance-peg]提供托管抵押说明。候选精度仍需启用时真实链校验。
- [Ethereum：JSON-RPC][eth-rpc]，receipt / logs / block tags；[ERC-20][eip20]，Transfer 事件；[ERC-681][eip681]，交易请求 URI。
- [BNB：网络 RPC 与 chain ID][bnb-rpc]、[Finality API][bnb-finality]。概率与经济最终性不能混用。
- [Arbitrum：Finality][arb-finality]、[Finality and chain reorganizations][arb-tags]，父链数据最终性、`safe/finalized`和状态结算的区别。
- [OP Stack：Transaction finality][op-finality]、[Base：Transaction finality][base-finality]，L1批次确认、最终性及提款等待期的区别。

[circle]: https://developers.circle.com/stablecoins/usdc-contract-addresses.md
[tether]: https://tether.to/en/supported-protocols/
[usdt0-deploy]: https://docs.usdt0.to/technical-documentation/deployments
[usdt0-api]: https://docs.usdt0.to/api/deployments
[usdt0-model]: https://docs.usdt0.to/
[bnb-assets]: https://github.com/bnb-chain/bnbagent-sdk/blob/7a7a431a61b86279c503b443e98144fdee830a84/typescript/src/networks/assets.ts
[binance-peg]: https://www.binance.com/en/collateral-btokens
[eth-rpc]: https://ethereum.org/en/developers/docs/apis/json-rpc/
[eip20]: https://eips.ethereum.org/EIPS/eip-20
[eip681]: https://eips.ethereum.org/EIPS/eip-681
[bnb-rpc]: https://docs.bnbchain.org/bnb-smart-chain/developers/json_rpc/json-rpc-endpoint/
[bnb-finality]: https://docs.bnbchain.org/bnb-smart-chain/developers/json_rpc/bsc-api-list/#finality-api
[arb-finality]: https://docs.arbitrum.io/how-arbitrum-works/deep-dives/finality
[arb-tags]: https://docs.arbitrum.io/how-arbitrum-works/reference/finality-and-reorgs
[op-finality]: https://docs.optimism.io/stack/transactions/transaction-finality
[base-finality]: https://docs.base.org/base-chain/network-information/transaction-finality
