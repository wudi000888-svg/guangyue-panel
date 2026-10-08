# 加密支付接口与交互契约

版本：1.1 · 钱包接口已实现，付款协议待实施。金额、资产白名单、最终性和退款规则见[完整技术设计](../../CRYPTO_PAYMENTS.md)。

## 当前钱包接口

以下路径均以 `/api/commerce/crypto/admin` 开头，仅本站 owner 可用，拒绝子站代理身份，响应 `Cache-Control: no-store`。写入验证当前管理员密码；创建、分配、状态、确认及恢复使用 `operation_id`，更新现有钱包同时带 `revision`。敏感备份和助记词查看不把响应写入普通重放存储。前端使用独立请求层，不以助记词或备份口令生成通用缓存键。

| 方法与路径后缀 | 已实现行为 |
| --- | --- |
| `GET /wallets` | 钱包公开元数据、模式、启停、备份确认、恢复状态和索引；不返回助记词 |
| `POST /wallets/hot` | 浏览器官方 Wallet Core 4.8.4 生成 `mnemonic/xpub/path/first_address`，后台独立验证后 Vault 加密；热模式明确 `risk_ack` |
| `POST /wallets/xpub` | 校验 EVM 收款分支并建立只读钱包，拒绝重复的实际收款分支 |
| `GET /wallets/{id}/addresses` | 按 `after` 游标每页最多 200 条读取已分配手动地址；GET 不分配新索引 |
| `POST /wallets/{id}/addresses` | 事务锁定分配索引并保存地址，同请求重试返回原地址；未备份确认、暂停或待恢复核对时不分配 |
| `POST /wallets/{id}/status` | 修改继续分配的启停状态；不删除钱包、密钥或历史地址 |
| `POST /wallets/{id}/backup` | 当前管理员密码和独立 `backup_password` 生成可移植加密备份，包含全部地址记录和索引 |
| `POST /wallets/{id}/backup/confirm` | 明确确认已保存导出的备份；热钱包只有确认后才可分配 |
| `POST /wallets/{id}/reveal` | 当前管理员密码验证后临时返回助记词，供外部工具手动操作；无交易签名或广播 |
| `POST /wallets/restore` | 独立口令解密完整备份，校验钱包身份与记录；已有分支取较高索引，新分支暂停并标记 `recovery_required` |
| `POST /wallets/{id}/recovery` | `recovery_ack:true` 和经管理员核对的 `next_index`，不得低于已知高水位；完成后仍暂停，需另外启用 |

备份外层为 `{format:"guangyue-crypto-wallet-v1", data:<base64>}`，data 解码为包含 salt、nonce 和 ciphertext 的 JSON；固定 scrypt N=32768/r=8/p=1 派生 AES-256-GCM 密钥，32 字节 salt、12 字节 nonce，AAD 为 format。口令至少 12 个字符且不同于当前管理员密码。备份不能代替外部最新分配记录：恢复旧文件不会自动得知备份之后的索引，跨部署恢复必须停止旧站并核对高水位。

手动地址不绑定订单或资产，不声明已收款；钱包列表不显示 scanner、广播或自动支付能力已启用。面板快照覆盖钱包密文与记录，仍需对应 `master.key` 解密。端到端使用流程见[钱包管理](../../CRYPTO_WALLETS.md)。

## 后续付款协议（未实现）

以下通用规则、API 表、钱包身份绑定、invoice 和结算状态均为后续付款设计；不应与上面的已实现钱包路由混为一套上线能力。

## 通用规则

- 所有付款、配置和任务均归属于当前付款权威站点；站点身份由服务端配置确定，不接受客户端指定租户绕过权限。管理接口归入 `/api/commerce/crypto/admin/*`，成员接口也留在 commerce 本地请求域。后端拒绝子站代理身份调用资金接口，不能只依靠前端 `localOnly`。
- 当前 `commerceAPI` 主要处理 GET/POST。新增独立 `cryptoAPIRoute` 在普通分发前校验权限并路由，管理写入使用 POST，与已有交易接口一致；不得将网络调用放进现有全局锁区间。
- 成员只能读取、付款或申请处理自己的订单/invoice；管理员仅在本站管理。配置变更、批准资金库、启用收款和退款审批要求管理员二次认证。业务站令牌不具备此权限。
- 写入使用稳定 `operation_id` 和服务端请求指纹；同身份、同操作、同参数返回原结果，改参数复用键返回 409。invoice/配置具有 `revision`，过期版本更新返回 409。
- Token 原子数量、汇率分子/分母、对外 CNY 最小单位均为规范十进制字符串；链 ID、索引、时间和 revision 使用有明确范围的整数。时间使用 Unix 秒，响应包含 `server_time`；禁止前端 `Number` 金额计算及科学计数法。
- 错误新增机器码，保留现有 `error` 人类文案字段兼容前端。示例：`{"error":"收款源尚未配置","code":"address_source_not_ready","retryable":false,"request_id":"<请求标识>"}`。请求标识不包含密钥或支付地址的私有描述符。

## API 清单

| 方法与路径 | 输入 | 结果 / 权限 |
| --- | --- | --- |
| `GET /api/commerce/crypto/assets` | 无 | 登录后返回可用网络、资产、精度、`purposes:["order"]` 与暂停原因；未启用 `enabled:false` |
| `POST /api/commerce/crypto/admin/wallet-binding-challenges` | `account, chain_id, purpose` | 管理员；返回签名消息、挑战 ID、有效期；不创建收款源 |
| `POST /api/commerce/crypto/admin/wallet-bindings` | `challenge_id, signature, operation_id` | 管理员；验证当前账户控制、单次消费挑战，建立绑定候选 |
| `POST /api/commerce/crypto/admin/address-pools` | `mode, public_config, pairing_credential?, operation_id` | 管理员；创建待验收池；服务凭证加密且不回显，不接受私钥字段 |
| `POST /api/commerce/crypto/admin/address-pools/{id}/checks` | `revision, operation_id` | 管理员；返回异步检查任务；派生样本不是可付款地址，测试检查不等同主网批准 |
| `GET /api/commerce/crypto/admin/health` | 有界分页/过滤 | 管理员；钱包/池/链/收款/转出分层状态和检查证据 |
| `POST /api/commerce/crypto/admin/config` | `revision, enabled_assets, pool_id, treasury_bindings, operation_id` | 管理员二次认证；CAS 保存，按实际验收结果决定是否允许启用 |
| `GET /api/commerce/orders/{id}/crypto-invoices/current` | 无 | 订单本人/管理员；返回 `preparing / invoice / none` 恢复视图，当前/最近付款单含终态、`can_pay` 与历史引用；不派生地址 |
| `POST /api/commerce/orders/{id}/crypto-invoices` | `asset_id, payment_source, operation_id` | 订单本人；冻结报价，原子建立 attempt/invoice/地址；存在待确认款时拒绝静默换网 |
| `GET /api/commerce/crypto-invoices/{id}` | `If-None-Match` 可选 | 本人/管理员；金额、付款/权益/额外款三层状态、组合视图版本、证据与下一步 |
| `POST /api/commerce/crypto-invoices/{id}/tx-hints` | `chain_id, tx_hash` | 本人；只排队查询，不能提交金额或 `paid=true` |
| `POST /api/commerce/crypto-invoices/{id}/refund-requests` | 理由、方式、同链退款地址（如适用）、`operation_id` | 本人；建立待审批申请，不签名、不直接完成退款 |
| `POST /api/commerce/crypto/admin/reviews/{id}/resolve` | 审核动作、证据、revision、`operation_id` | 管理员二次认证；经过共同资金与售后状态机，不直接修改 `paid` |
| `POST /api/commerce/crypto/admin/backfills` | 链、有限区块范围、理由、`operation_id` | 管理员；异步只读重扫，沿用原事实和 claim 管道 |

第三方 `POST /api/payments/crypto-hints/{provider}` 是后续可选通知入口：签名、限流、重放核验与持久 hint，不开放认款权限。它不接收用户身份，不替代 scanner，初期可以不启用。

现有 `/api/commerce/orders/pay` 的 Checkout 结果拟扩为 `type:"redirect" | "crypto_preparing" | "crypto_invoice"`。余额与免费开通保留原行为；crypto 类型返回持久准备请求或已提交的 invoice，不在 `checkout_url` 塞钱包 URI。`orders/pay` 和专用 invoice 创建接口调用同一创建服务、共用操作指纹与订单级准备锁。付款方式必须声明 `purposes`，防止钱包充值页错误列出一期 crypto。

## 钱包绑定

签名挑战冻结 `challenge_id / actor_id / site_id / account / chain_id / origin / purpose / nonce / expires_at / binding_revision`。默认 5 分钟，nonce 由服务端安全随机生成，只能消费一次。消息明确说明用途是身份或资金库绑定，不是转账授权。

EOA 校验签名恢复出的账户并比对挑战上下文；智能账户仅在已验证的专用适配器支持时启用，不能套用 EOA 恢复。拒绝签名返回可重试状态，不清除已保存的收款配置。账户或链切换需要重新确认候选绑定，不修改已批准资金库。

普通 OKX 连接结果不能填充 `branch_xpub`。绑定账户签名不能证明另一收款分支可转出资金；不从签名派生种子、索取助记词或无限 Token allowance。管理员断开浏览器钱包不暂停已经批准的后台收款源。

## HD 地址源协议

| 字段 / 方法 | 约束 |
| --- | --- |
| `pool_id / revision / signer_id` | 不可变池版本及资金控制方，不以显示名称标识真实分支 |
| `descriptor_hash / origin_path / branch_xpub` | 明确 BIP-32/secp256k1/EVM 算法和导出层级；服务分配模式可不导出 xpub |
| `owner_deployment_id / owner_site_id / chain_id` | 分支专属付款权威，签名器记录独占归属。导入相同实际分支不能靠换 pool ID 绕过 |
| `DescribePool` | 返回批准的分支身份、能力、revision、高水位和恢复条件；不含私钥 |
| `ReserveAddress` | 输入稳定 `allocation_request_id`、所有者、池版本及请求指纹；同键同参数恒返回原预留 |
| `GetReservation` | 响应丢失后查询原预留，不能改用新键获取另一个地址 |
| `ListReservations / Checkpoint` | 有界分页，返回外部已使用索引与持久预留；用于旧备份恢复核对 |

外部分配响应绑定 `allocation_id / request_id / request_fingerprint / owner / pool_revision / descriptor_hash / index / address / created_at`，通过已认证服务及凭证验证。受限配对凭证只有描述、预留、查询能力，不具备签资金交易权限。内部连接使用 mTLS 或等效认证；服务地址/链 RPC 按网络用途实施 SSRF、TLS、DNS、重定向和超时限制，不能允许任意用户指定 URL。

本站流程：**持久申请 → 服务持久预留 → 验证响应 → 地址和 invoice 同事务提交 → 返回客户**。中途失败保留请求及预留；未提交地址可以成为废弃 tombstone，永不回收。已提交 invoice 的重试返回原 invoice。恢复需先核对外部预留和使用高水位，缺失证据停止新分配。

同一订单只允许一个进行中的持久创建请求。尚未形成 invoice 时，创建接口可返回 HTTP 202 与 `type:"crypto_preparing"`、原请求标识和可查询状态；不返回未提交的收款地址。刷新后的 `current` 同样返回该准备请求及原操作标识、冻结选择，后台继续原预留流程；客户端不能因为暂时没有 invoice 就重新申请。请求成功形成 invoice 后，同键重试返回它，改参数复用键明确冲突。

`current` 优先返回进行中的准备请求或活动 invoice；没有进行中记录时返回最近关联 invoice 及有界 `history_refs`，包括已收款、到期、取消与退款终态，明确 `can_pay=false`。无任何记录才为 `phase:"none"`。新的报价或付款单必须由用户明确发起，并检查无冲突准备请求及待确认款，不能由刷新/轮询自动创建。

本地 watch-only 模式将索引分配和 invoice 放同一事务。硬化层级须在签名器完成，面板只派生导出分支以下非硬化子级；根 xpub、账户通用公钥和普通地址不能被默认为正确收款分支。可支出验证与密钥恢复备份独立验收，不能仅验证“能算出地址”。

## 付款单与状态

以下是返回结构的字段模板，含占位符，不是已存在接口的真实响应，也不是可付款数据：

```json
{
  "type": "crypto_invoice",
  "invoice": {
    "id": "<付款单标识>",
    "order_id": "<订单标识>",
    "revision": 1,
    "view_revision": "<组合视图版本>",
    "asset_revision": 1,
    "finality_policy_revision": 1,
    "chain_id": 31337,
    "asset_id": "<测试资产标识>",
    "token_contract": "<测试合约地址>",
    "address": "<测试 EVM 地址>",
    "decimals": 6,
    "payment_decimals": 2,
    "order_amount_minor": "100",
    "expected_atoms": "1000000",
    "received_final_atoms": "0",
    "observed_pending_atoms": "0",
    "payment_state": "awaiting_payment",
    "entitlement_state": "not_started",
    "extra_funds_state": "none",
    "payment_source": "wallet",
    "quote_expires": 1800001800,
    "server_time": 1800000000,
    "next_action": "pay"
  }
}
```

金额仅示意字段类型，不表示当前汇率。真实响应另包含冻结的资产名称、网络、数量格式、费用、二维码数据和帮助信息。地址只能由服务端已提交记录返回。资产/确认版本变化不能改写旧付款单；已收到款的旧单继续监听。

`revision` 是 invoice 写入 CAS 版本；`view_revision / ETag` 是完整响应的组合版本，覆盖 invoice、当前订单权益、额外款/退款责任及链确认视图。任一业务层变化都使组合版本变化，不能只按 invoice 行版本返回 304。每次条件查询先验证当前权限；初期可使用普通 200 轮询，启用 ETag 前须为前端请求层增加 304 处理，保留已获响应而不解析空 JSON 或显示请求失败。倒计时使用服务端时间校正，不把每秒时钟变化当作付款状态变更。

| 状态层 | 允许值与含义 |
| --- | --- |
| `payment_state` | `awaiting_payment / partial / confirming / settled / expired / cancelled / review_required / refund_pending / refunded`；查询网络故障不是支付状态 |
| `entitlement_state` | `not_started / queued / provisioning / active / blocked / revoked`；已收款不代表已开通 |
| `extra_funds_state` | `none / review_required / refund_pending / refunded`；超付不覆盖套餐已生效主状态 |
| 配置钱包 | `unbound / awaiting_signature / verified / account_changed / disconnected` |
| 收款源 | `unconfigured / pairing / checking / ready / degraded / revoked` |
| 运行开关 | 新 invoice、自动结算、履约和签资金交易分别暂停；读查询与历史责任保留 |

invoice 生命周期的 `confirming`、规范 receipt epoch 与最终转账事实不是同一状态表。到期时已存在及时上链款仍继续确认；取消和到期不删除款项，也不自动生成新地址。

## UI / UX 约定

管理员沿用“系统设置 → 交易与接入”，本期先展示钱包与地址管理，默认内置热钱包，可选外部 xpub。以后接入支付时再增加“选择地址来源 → 逐链验证 → 明确启用”步骤；可选 OKX 绑定或隔离服务不能成为创建本期钱包的硬性前置条件。状态分层显示，没有通过完整支付验收前不向成员展示 crypto 付款地址或二维码。

成员仍从套餐和我的订单进入，使用 `CryptoCheckoutPanel`，先选择网络/资产及付款来源，再展示数量、地址和确认进度。钱包模式 ERC-681 码，交易所模式纯地址码，均提供复制和明确网络标注；成员不用理解节点组、HD 或 xpub。

| 场景 | 交互要求 |
| --- | --- |
| 创建或查询失败 | 显示正在准备/暂时无法更新，恢复原请求；不能暗示钱已丢失或要求重复付款 |
| 少付且有待确认补款 | 同时展示最终确认数、待确认数和差额；避免再次催付同一补款 |
| 确认中 | “已发现转账，等待网络确认，请勿重复付款”；倒计时结束不终止及时款核验 |
| 已收款未开通 | 收起付款动作，单独显示开通进度；履约失败可恢复，不生成第二份购买 |
| 已开通且超付 | 主状态“套餐已生效”，独立显示多付责任和处理入口 |
| 过期/取消 | 收起二维码和立即支付，保留历史地址、交易证据和求助，不复用地址 |
| 退款 | 申请→审批→资金处理中→最终已退款；填写 tx hash 或勾选框不能直接完成 |

刷新先查订单恢复视图，接续持久准备请求或恢复当前/最近 invoice，不能依赖组件内存幂等键。默认每 10 秒串行查询，可见性恢复立即刷新；隐藏页面停止前台轮询，后台监听继续。倒计时按服务端时钟校正，不逐秒读屏；状态反馈 `role=status`，错误 `role=alert`，不抢焦点。移动端支持 320px 宽度、触控和完整地址复制，二维码不能是唯一入口；复用现有月庭主题、双语与组件样式，不增加一级菜单。

## 可恢复错误

| HTTP / 机器码 | 处理 |
| --- | --- |
| 400 `invalid_descriptor / invalid_amount / unsupported_account` | 修正输入或选择已支持账户，不能降级为未校验收款 |
| 401 / 403 `unauthorized / forbidden` | 清理会话或显示权限限制，不转发给选中子站 |
| 409 `idempotency_conflict / revision_conflict / active_invoice_conflict` | 查询原结果或当前 revision 后明确操作，不自动改参数重试 |
| 409 `address_source_not_ready / recovery_required` | 管理员完成配置/恢复；成员看到暂停新付款，历史单继续查 |
| 429 `rate_limited` | 按服务端退避重试，不产生新操作键 |
| 503 `allocation_unavailable / rpc_quorum_unavailable` | 停止新地址或自动结算，保留历史责任；不回退到重复地址/低确认 |
