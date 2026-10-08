# 加密货币支付与提取接口契约

适用版本：**0.37.0** · 2026-10-09。以下为本版实际接口；未列出的 OKX 绑定、成员 crypto 充值、链上退款和外部签名器接口均未实现。

## 请求域、权限与金额

- 路径均位于 `/api/commerce/crypto`；下文 `admin/...` 仅当前站点 owner 可用。后端独立拒绝子站代理资金操作，前端也不会为 commerce 请求附加选中子站身份。
- 成员只能读取自己的 invoice、为自己的有效订单发起付款；owner 可只读查看其他成员 invoice。余额、配置、钱包和提取仅 owner 可访问。
- 设置保存、钱包敏感写入、提取确认/重试/结束需要当前管理员 `password`。只读余额、invoice 查询和提取预览不要求再次输入密码。响应禁止缓存，普通钱包 DTO 不包含助记词、私钥或 RPC 凭证。
- 写入使用 `operation_id`，需要重试时保留同一个操作标识。同键参数冲突、状态不允许或版本已变化返回明确错误，不默默分配新地址或新 nonce。钱包/配置更新使用对应 `revision`；invoice 不存在早期设计中的通用 `revision` 字段。
- 所有 Token 数量、原生币数量、Gas price 和费用均为十进制整数字符串。汇率 `cny_per_token` 是十进制字符串。时间是 Unix 秒，网络数字 ID、索引和 revision 是整数。不得把原子数量转为 JavaScript `Number`。
- 套餐标价和已有支付 attempt 仍用 CNY 分；invoice 用独立 Token 原子数量，两者不能直接相加。状态错误通常以 `{error: "…"}` 返回，前端保留已有成功查询结果。

## 已有钱包与地址接口

下表路径前缀为 `/api/commerce/crypto/admin`。

| 方法与后缀 | 请求要点 / 返回 |
| --- | --- |
| `GET /wallets` | `{items: CryptoWallet[]}`，含模式、启停、备份/恢复状态、revision、`next_index` 等公开元数据 |
| `POST /wallets/hot` | `name,mnemonic,xpub,path,first_address,engine_version,password,operation_id,risk_ack:true`；官方 Wallet Core 4.8.4 生成，服务器独立核对后加密 |
| `POST /wallets/xpub` | `name,xpub,path,password,operation_id`；返回只读钱包，不保存外部私钥 |
| `GET /wallets/{id}/addresses?after=N` | `{wallet,items,next_after}`，按索引每页最多 200 条；GET 不分配地址 |
| `POST /wallets/{id}/addresses` | `password,operation_id,revision,label`；手动分配，返回 `{wallet,address}`；不创建订单 |
| `POST /wallets/{id}/status` | `password,operation_id,revision,enabled`；停用仅停止新地址分配，不删除历史 |
| `POST /wallets/{id}/backup` | `password,backup_password`；返回可移植加密备份，不缓存敏感响应 |
| `POST /wallets/{id}/backup/confirm` | `password,operation_id,revision`；确认已导出并保存备份 |
| `POST /wallets/{id}/reveal` | `password`；仅热钱包临时返回助记词，前端限时并在离开时清除 |
| `POST /wallets/restore` | `password,backup_password,backup:{format,data},operation_id`；新恢复分支暂停并要求核对索引 |
| `POST /wallets/{id}/recovery` | `password,operation_id,revision,next_index,recovery_ack:true`；索引不得低于已知高水位，核对后仍需启用 |

备份 `format` 为 `guangyue-crypto-wallet-v1`，使用独立口令加密。新站恢复必须核对备份之后的实际分配记录；同一分支不能交给多个独立部署同时分配。地址接口现在同时可读取手动地址和订单分配地址，invoice 的地址不能再用于其他订单。

## 收款配置与可选资产

`GET /admin/settings` 返回：

```text
{
  revision, enabled, wallet_id, invoice_minutes,
  chains: [{id, name, chain_id, has_rpc, has_rpc_backup,
            native_symbol, enabled, finality_verified}],
  assets: [{id, chain_id, name, symbol, contract, decimals,
            enabled, cny_per_token, rate_updated_at,
            rate_expires_at, payment_decimals}]
}
```

`POST /admin/settings` 提交同结构及 `password,operation_id`，RPC 可写 `rpc_url,rpc_backup_url`，空值保留已存配置，返回不回显已存 URL/密钥。链、合约与资产精度来自固定白名单；不能通过提交新的合约扩展资产。`invoice_minutes` 为 15–120，默认 30。启用资产的手动汇率须有效，截止在未来 30 天内；已有 invoice 保留原始快照。

只有 Ethereum（`ethereum`/1）和 BSC（`bsc`/56）能启用自动收款/提取。Arbitrum（`arbitrum`/42161）、OP（`op`/10）、Base（`base`/8453）可配置查询 RPC，但 L2 自动资金操作有服务器硬限制，不能由 `finality_verified` 手工覆盖。

`GET /options` 返回 `{enabled, assets:[...]}`。只列配置中已启用的资产；每项包含上面的资产公开字段以及 `chain_name,available,unavailable_reason`。可用性受钱包、链、RPC、最终性与汇率有效期约束。读取选项不创建 invoice、不消耗地址。

| asset_id | 资产身份 | 精度 | 本版自动操作 |
| --- | --- | --- | --- |
| `ethereum-usdt` | USDT · Ethereum | 6 | 可启用 |
| `ethereum-usdc` | 原生 USDC · Ethereum | 6 | 可启用 |
| `bsc-usdt` | Binance-Peg USDT · BNB Smart Chain | 18 | 可启用 |
| `bsc-usdc` | Binance-Peg USDC · BNB Smart Chain | 18 | 可启用 |
| `arbitrum-usdt0` / `arbitrum-usdc` | USDT0 / 原生 USDC · Arbitrum | 6 | 禁止 |
| `op-usdt0` / `op-usdc` | USDT0 / 原生 USDC · OP | 6 | 禁止 |
| `base-usdc` | 原生 USDC · Base | 6 | 禁止 |

合约值以服务端白名单 DTO 为准。不能仅凭 symbol 或相同 EVM 地址推断资产与链相同。

## 订单付款单

| 路径 | 行为 |
| --- | --- |
| `POST /invoices` | `{order_id,asset_id,operation_id}`；为当前成员订单生成独立地址，201 返回新 invoice，同操作重放 200 返回原 invoice |
| `GET /invoices?order_id=...` | `{items: CryptoInvoice[]}`，恢复该订单付款单；成员限本人，owner 可查看其他成员 |
| `GET /invoices/{id}` | 返回 invoice 及最多 100 条最近 `transfers` 证据，权限同上 |
| `GET /admin/invoices` | owner 的本站付款单列表；当前按创建时间倒序最多返回 200 条 |

订单创建与读取不分配地址；仅上述 POST 分配。免费订单不能创建 invoice。已选择其他支付渠道的订单不能直接覆盖。相同资产已有当前付款单时，用新操作标识再次创建返回 409，客户端应 GET 恢复已有地址；原操作标识重放仍返回原记录。

用户确实选择另一资产/网络时才允许创建新 invoice，旧 invoice 标为 `superseded`，地址永久保留。任一旧地址已观察到转账后拒绝切换；每个订单最多 12 张 invoice。换网络不会延长原订单已冻结的付款截止时间。

实际 invoice DTO：

```text
id, order_id, attempt_id,
chain_id, chain_name, asset_id, symbol, asset_name, contract,
decimals, payment_decimals, address,
expected_atoms, received_atoms, confirmed_atoms,
remaining_atoms, overpaid_atoms,
state, message, created, expires,
rate_source, cny_per_token, rate_updated_at, rate_expires_at,
qr_uri, order_state, receipt_state, scan_error, last_scan, finalized_block
```

`provider_id` 是 `crypto`，渠道代码为 `evm_crypto`。创建不走原来的 `/commerce/orders/pay` redirect 接口，不开启外部空白窗口，也不开放 `/commerce/wallet/topup` 加密充值。

| invoice `state` | 界面与付款行为 |
| --- | --- |
| `waiting` | 等待付款；报价与订单有效时显示地址、金额和 QR |
| `partial` | 已最终确认部分款项，显示 `remaining_atoms` 供补足；钱包 QR 也使用服务端剩余数量 |
| `confirming` | 已检测到未最终确认的付款；优先等待，不再显示可付款 QR 诱导重复转账 |
| `paid` | 链上款项已确认；是否开通必须另外看 `order_state` |
| `expired` | 付款报价结束；保留地址和记录，不提示继续向旧报价转账 |
| `superseded` | 已改用另一付款单；旧地址继续保留和核对 |
| `review_required` | 款项或证据需要处理；不自动冒充开通或退款 |

订单仍沿用 `pending/provisioning/completed/expired/failed/cancelled` 等既有状态，没有替换成 invoice 状态。只有 `order_state=completed` 才显示套餐已生效；超付或历史异常不能覆盖已生效事实。

`transfers` 每条含 `chain_id,tx_hash,log_index,contract,atoms,block_number,block_time,state,reason`，区分 `observed/confirmed/review_required/orphaned`。不在当前可识别资产清单内的转账显示原子数量和实际合约/链，不能套用 invoice 的精度。

交易所 QR 仅包含收款地址；钱包 QR 使用服务端 ERC-681 `qr_uri`。显示、复制和扫码不能在前端重新计算人民币汇率。网络错误保留已知付款状态；刷新用 GET 恢复，只有显式点击才 POST。前端仅将待定请求的公共操作标识保存在 sessionStorage，密码和密钥不进入该记录。

## 分页余额与固定 Gas 地址

`GET /admin/wallets/{id}/balances?chain_id=ethereum&after=N` 每页最多查询 10 个历史地址，返回：

```text
{
  wallet_id, chain_id, native_symbol, watch_only,
  operations_supported, unavailable_reason,
  funding_address, funding_path, funding_balance_atoms,
  checked_at, block_number, next_after,
  items: [{address_id, order_id, invoice_id, user_id, index, address, path,
           native_atoms, tokens:[{asset_id,symbol,decimals,balance_atoms}]}]
}
```

此处 `chain_id` 是配置字符串 ID，如 `ethereum`；不是 invoice 中的数字 1。失败不能转换成零余额。分页未结束时只能说“已查询地址暂无余额”，不能冒称全部历史地址为空。

热钱包固定资金地址与订单地址分支不同。`funding_balance_atoms` 和 `native_atoms` 使用 18 位原生币精度：Ethereum 用本链 ETH，BSC 用本链 BNB，L2 查询的 ETH 也各自独立。外部 xpub 只提供只读余额，不显示后台可提取。

## 提取预览、批准与后台任务

`POST /admin/wallets/{id}/sweeps/preview` 是不需密码的估算：

```json
{
  "chain_id": "ethereum",
  "asset_id": "ethereum-usdt",
  "destination": "<对应网络的 EVM 收款地址>",
  "min_atoms": "1000000",
  "max_gas_atoms": "10000000000000000",
  "address_ids": ["<可选：仅查询这些已分配地址>"]
}
```

`min_atoms` 是每个地址的该 Token 最低余额，不是任务总额；`max_gas_atoms` 是本任务手续费上限，使用 18 位原生币原子数量。未提供 `address_ids` 时扫描全部历史地址，但超过 1000 个会明确拒绝并要求缩小选择，不能默默取前 1000 个。

预览返回输入快照及 `checked_at,expires_at,block_number,native_symbol,funding_address,funding_balance_atoms,items,quote,can_submit,unavailable_reason`，金额含义如下：

| 字段 | 精确语义 |
| --- | --- |
| `total_atoms` | 本次预计提取的 Token 原子数量 |
| `total_topup_atoms` | 向各来源地址补入的原生币差额总和；不等于实际已发生的手续费 |
| `total_gas_atoms` | 来源 Token 转账费用上限与 funding 补款交易手续费的总和；不另加补款本金 |
| `required_funding_atoms` | 固定 Gas 地址本次需要具备的总余额：补差额 + 补款交易自身费用 |
| `funding_shortfall_atoms` | 尚需向固定 Gas 地址充值的数量：`max(required_funding_atoms - funding_balance_atoms, 0)` |

每项 `items` 包含 `address_id,address,path,amount_atoms,gas_limit,gas_price_atoms,gas_fee_atoms,topup_atoms,funding_gas_atoms,funding_gas_limit`。Gas limit 也通过字符串返回。预览不进行链上广播。

资金不足或预算过低时仍返回可读预览，以 `can_submit=false` 和 `unavailable_reason` 禁止确认。允许提交时提供约 120 秒有效的服务端加密 `quote`，绑定管理员、钱包、链、资产、来源、金额、目标和预算；客户端不能改写其中字段。界面未输入密码时每 30 秒更新费用，输入密码后锁定当前报价，过期重新估算。

| 路径 | 请求 / 返回 |
| --- | --- |
| `POST /admin/wallets/{id}/sweeps` | **仅** `{quote,password,operation_id}`；返回持久 `CryptoSweepJob` |
| `GET /admin/sweeps?wallet_id=...` | `{items: CryptoSweepJob[]}`；当前最多返回最近 50 个任务，数据库保留历史 |
| `POST /admin/sweeps/{id}/retry` | `{password,operation_id}`；接续原任务、原签名交易；`failed/review_required` 不自动重新补款 |
| `POST /admin/sweeps/{id}/cancel` | `{password,operation_id}`；以 `can_cancel` 和服务端即时检查为准，不能丢弃未确认交易或活跃执行租约 |

任务含 `id,wallet_id,chain_id,asset_id,destination,state,error,created,updated,can_cancel,items`。**任务的 `chain_id` 是数字网络 ID**；前端需用配置的数字 ID 映射名称。任务项含 `id,address,amount_atoms,state,error,gas_tx_hash,sweep_tx_hash`。

任务状态为 `queued/running/waiting/complete/failed/review_required/cancelled`；项状态包含 `queued/gas_pending/sweep_pending/complete/failed`。广播哈希、已补 Gas 或成功提交任务都不表示 Token 已最终到账。只有规范 receipt、资产/金额/来源/目标和最终性验证完成后才显示完成。

同一钱包、同一链只允许一个尚未完成或结束的任务；nonce 和签名交易先落库再广播。不确定的广播结果复查原哈希或重播原 raw，不能新建 nonce 再转一次。安全结束只停止尚未执行的后续步骤，已产生手续费和历史记录保留。当前不支持后台归集外部 xpub，也不把此提取接口作为用户退款接口。

## 支付模块总开关与接管默认值

支付总开关由 `GET /api/commerce/settings` 的 `payment_module_enabled` 布尔值公布，`GET /api/state` 的 `system` 同步提供该值。owner 使用 `POST /api/commerce/settings` 的 `{settings:{...,payment_module_enabled},password}` 显式修改；省略此字段不会重置已有设置。首次使用管理令牌的真实管理请求原子写入“已接管”标记和关闭默认值，后续请求不覆盖管理员重新开启的值。业务 pull agent 必须先转为令牌自主管理模式才能开启。关闭时新购买、充值、invoice、支付启用与提取授权返回 403，历史查询、回调及已批准任务仍可处理。

## 明确不属于当前 API 的长期设计

OKX 挑战绑定、隔离签名服务、跨站共享地址池、市场实时报价、原生币付款、链上部分退款/原路退款、自动跨链，以及 L2 的自动收款/补 Gas/提取均待独立实现与验收。既有管理员退款批准或提供 tx hash，不能把这些能力变成已实现，也不能借用法币手工确认接口将 crypto 标为已退款。
