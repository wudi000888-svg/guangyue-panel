# Plan sales, orders and customer support

This guide covers the current Lite standalone and Pro controller workflows: publish plans, collect payments, review refunds and answer support tickets. Members see only their own records; administrators manage this site. Entries removed from the sidebar remain available under **System settings → More features**.

Internal balances work without an external payment provider. Online checkout requires merchant credentials; email requires an SMTP account. A successful installation does not configure either service for you.

## Enable the services

1. Create a plan with its nodes, quota, period, protocols, validity and price. Choose the local, sub-site or public source group first; narrow a group to individual nodes if needed.
2. In the administrator **Plan listings** page, create an offer with a plan version, CNY price, listing status and per-member purchase limit. **A zero-price offer can be listed.** A purchase limit of zero means unlimited purchases.
3. Confirm that the payment module is enabled in **System settings → Commerce and integrations → Online payments**. Changing that switch requires the current administrator password. Listed offers control whether a plan is purchasable; the old balance-sales flag is no longer required. Token-managed sub-sites default to payments disabled on first takeover; their local administrators can enable them deliberately.
4. Test as a member: choose a plan, create an order, confirm free activation or complete payment, check the active plan and import the subscription. Fund internal balances using codes or an administrator adjustment when testing balance payment.

Prices are integer CNY cents. Offers and orders retain their own price and entitlement snapshots. Edits to active plans propagate node selection, multipliers and protocol permissions; purchased quotas, validity and paused plan snapshots remain unchanged. See the [plan guide](PLANS.md).

## Online payments

Open **System settings → Commerce and integrations → Online payments**. Newly created methods are disabled by default. Hosted checkout supports **EPay V1 MD5** and **Stripe Checkout**. The generic JSON Webhook adapter is retained for existing integrations and does not offer a customer checkout. Prices and wallet balances use integer CNY cents.

On-chain plan checkout, receiving wallets, balances and collection live in the separate **Cryptocurrency** module. See the [wallet guide](CRYPTO_WALLETS.md) and [implementation guide](projects/crypto-payments/README.md). Internal balances and on-chain assets are separate accounts; crypto payments cannot be completed through the fiat manual-refund confirmation interface.

Secrets are write-only and encrypted in the site Vault. A blank secret during editing preserves the saved value. Disabling a method stops new checkouts but keeps existing callbacks functional. Removing a method with payment history archives it; a method without history can be deleted.

### EPay V1

1. Select EPay and enter a **public HTTPS gateway**, merchant ID and signing key. The gateway must support V1 `/submit.php` with MD5 signatures; an API offering only V2 RSA is not directly compatible.
2. Select `alipay`, `wxpay`, `qqpay`, or let the hosted cashier choose. Alipay and WeChat can be separate methods sharing the same gateway and merchant account.
3. Copy the saved callback URL: `https://panel.example/api/payments/webhook/<payment-method-id>`. GET and form POST are accepted. Merchant identity, signature, explicit successful trade status and amount are checked. Missing status never counts as payment.
4. Connection diagnostics check HTTPS reachability, **not merchant-key validity or channel readiness**. Complete the provider's test flow before enabling real collection.

### Stripe Checkout

1. Enter a `sk_test_…` or `sk_live_…` Secret key and **save with the method disabled**. The server verifies `/v1/account`, discovers the merchant account ID and returns the method's callback URL.
2. Create a Stripe Webhook endpoint for that exact URL and subscribe to:
   - `checkout.session.completed`
   - `checkout.session.async_payment_succeeded`
   - `refund.created`, `refund.updated`, `refund.failed`
3. Copy the endpoint's `whsec_…` signing secret into the method editor, then enable it. The API key and endpoint must use the same test/live environment. Enabling requires a signing secret.
4. Validate purchases, top-ups, duplicate delivery and refunds in test mode before configuring live collection. API diagnostics verify credentials and account identity; webhook delivery still requires a Stripe test event or test checkout.

The panel uses Stripe's hosted checkout and does not collect card details. API requests go only to Stripe's fixed HTTPS host with bounded timeouts and idempotency keys. Webhook verification uses the raw body, signature timestamp and test/live environment. Returning to the panel is never proof of payment.

### Wallet top-ups and recovery

Members enter an amount and select a payment method under **My wallet → Online top-up**. Funds belong to the **signed-in account**. An administrator viewing another member's wallet cannot accidentally top up that member through the administrator's own checkout. The verified receipt, balanced ledger entries and wallet credit commit in one transaction, with progress and balance refreshed automatically.

Online plan payments fund the order directly; they neither top up nor hold the internal wallet. The payment window is reserved synchronously on click. Browsers that block it use a same-page redirect instead. Existing attempts can be resumed from payment records, and interrupted requests reuse their operation ID. Return pages only query server state.

Callbacks are persisted before provisioning or crediting. Event IDs and payload hashes prevent replay, while a separate SQL unique constraint on **merchant account scope + external transaction reference** prevents one transfer from being credited across multiple payment methods. Distinct event IDs, concurrent requests and multiple application instances do not bypass this constraint. Each attempt retains an encrypted provider snapshot, so later configuration changes do not rewrite existing checkouts.

After restart, verified receipts resume without another provider callback. A payment received before the order deadline remains eligible even if recovery happens after that deadline. Late/cancelled payments, amount or currency mismatches, disabled users and entitlement conflicts retain actual received funds for refund handling. Unknown-order receipts enter manual review instead of disappearing. Upgrade recovery also imports previously paid attempts that had not completed provisioning.

### External refunds

Review the completed order in **Orders** first. The system fixes its refundable amount and handles the corresponding entitlement. After revocation and settlement, an external-payment order becomes `refund_pending` (**awaiting original-method refund**). This does **not** mean funds have been returned. Open **Online payments → Receipts and refunds** and select the actual receipt ID:

Refund requests are stored separately from fulfillment. A member request leaves the order `completed` and exposes `refund_status=requested`; the member can cancel before review, while an administrator can reject with a reason. Approval requires the request ID and revision. A unique pending constraint and database CAS prevent an old page from cancelling or rejecting a later request for the same order. Migration 017 backfills legacy `refund_requested` rows into this aggregate.

- **Stripe:** Request an original-method refund. The worker uses a stable idempotency key; only a successful API response, signed webhook or subsequent retrieval with `succeeded` completes it. Pending and failed responses remain visible and never count as refunded.
- **EPay:** Complete the real refund in the merchant console first. Enter the actual refund reference and reason, and explicitly confirm that funds were returned. The panel records the verified administrator confirmation; it does not send an EPay refund or credit the internal wallet.
- **Unknown-order receipts:** Reconcile the merchant statement and complete any actual refund before recording manual confirmation. Such receipts cannot automatically fund a user or plan.

Refund references are unique within the merchant account. External payments never silently become internal-wallet refunds. Balance-funded orders return the approved refundable amount to the internal balance after entitlement handling. Refund operations require the current administrator password, which is cleared from the form after use.

Standalone installations default to the payment module, redemption and support enabled. Plans need a listed offer, and external payment methods require configuration. Token takeover disables the sub-site payment module once; upgrades preserve existing settings. Disabling new sales or payment methods retains historical settlement and after-sales workflows. Without merchant credentials, installation or release checks do not constitute a real-payment acceptance test.

## Redemption codes

Administrators can generate 1–100 codes per batch, choose an amount with up to two decimals, set an expiry date/time or no expiry, and add a batch note. The default expiry is 30 days; finite expiry is limited to ten years. At most 10,000 valid unused codes are allowed.

Save or download the full codes immediately. The list shows only suffixes, values, expiry, batch and status. Select unused codes and verify the administrator password to revoke them permanently. Revoking a redeemed code never deducts credited balance.

Members enter the code in **Account balance → Redeem a code**. Funds go to the signed-in account once. Concurrent redemption and retries cannot credit a code twice. There are up to 10 attempts per user per ten-minute window. Expiry uses server time; the UI displays local time.

Codes use 128 random bits. Code records store a hash and suffix. Original generation responses are encrypted with the site's master key to support retries after a lost response. Normal listing does not recover full codes.

## Balance and orders

The header shows the signed-in account’s available balance and opens the wallet when clicked, including on mobile. The tooltip and accessible label include held funds. When an administrator selects another member in the wallet, the header continues to show the administrator’s own balance. Redemption, adjustments and order actions refresh it immediately; visible pages also refresh every 30 seconds and when revisited. Loading or failed reads display “—” instead of a misleading zero. Local balance is hidden while managing a remote sub-site.

Amounts are stored as integer cents. Minimum positive amount: CNY 0.01. Maximum transaction amount and total account balance: CNY 1 billion. An authenticated owner session can credit, gift or debit an unarchived account without a second password prompt. A reason is still required, every change is audited in the immutable ledger and integrity-checked, and regular members cannot adjust balances. Negative balances are prohibited.

Every movement posts balanced ledger entries and updates the wallet in one transaction. Startup reconciliation and wallet checkpoints detect inconsistencies and block financial writes. These are internal accounting records, not proof of an externally verified payment. There are no withdrawals, transfers, overdrafts or automatic renewals. Node multipliers consume quota, not additional cash.

Creating an order generates a unique `GYO-` number and snapshots its price and entitlement. The order expires after 15 minutes without confirmation. Wallet confirmation holds the balance; successful entitlement provisioning captures it in the same transaction. Online payment confirmation starts provisioning without a wallet hold. Cancellation releases any held funds. State and retries persist across process restarts; retries back off to 60 seconds. Provisioning times out after 30 minutes: wallet funds are released and external payments require original-method refund handling.

Purchases support initial activation, reactivation after expiry, and renewal of the same plan version. Renewal extends expiry without clearing current usage or moving its cycle boundary. A different plan starts first and pauses the previous plan with its remaining validity, usage and permissions. Buying A, then B, then C gives the usage order C → B → A. Members see the remaining days of each paused plan. Permanent plans do not require renewal; changing plans does not automatically deduct the old plan’s residual value from the new price.

### Refund requests and paused plans

Members can request a refund under **My orders** and cancel before review. Administrators can approve or reject, with a reason visible to the member. Requesting or rejecting a refund does not revoke the plan.

Refunds identify the active or paused entitlement by its original order. Later node-policy changes and subsequent purchases do not by themselves block a refund. An entitlement replaced manually and no longer linked to the order still requires reconciliation; the system does not refund another plan in its place.

Amounts are rounded down to integer cents. Administrators cannot enter an arbitrary partial-refund amount:

| Order position | Current calculation |
| --- | --- |
| Active plan with a finite traffic quota | When quota has been used: price × unused quota ÷ plan quota, with a minimum of zero; consumed quota is not refunded |
| Active unlimited-traffic plan with finite validity | Proportion of remaining time to total plan duration, capped at the original price |
| Paused plan | The current implementation refunds that order’s original price; it does not prorate usage before the pause |
| Free order | Entitlement can be withdrawn; the refund is zero |

An active refund revokes and settles that entitlement before restoring any remaining paused plans in their original order, with their saved time and usage. Refunding a paused item removes only that item. A renewal refund removes that renewal’s extension and preserves usage.

Balance payments return the refundable amount to the internal balance. External payments enter the original-method workflow above. Funds are not refunded before required revocation completes; an offline sub-site can leave settlement waiting.

Active orders exclude conflicting manual entitlement and business-site membership changes. Old connections and business-site grants must settle before a new paid quota period is issued.

## Support tickets

Enabled signed-in members can create, reply to and close tickets. Expired or quota-exhausted accounts retain portal access. Members can reopen a closed/resolved ticket within seven days, subject to the open-ticket limit. Categories cover account/balance, orders/plans, nodes/connectivity, usage and other issues. Members can link their own orders and authorized nodes. Resource labels survive node deletion.

The server owns the ticket workflow: new tickets and member replies enter `processing`, public administrator replies enter `waiting_user`, and internal notes leave the public state unchanged. `resolved` and `closed` are terminal states. Ticket lists provide server-side search, state filters and `next_before` cursor pagination; replies and state changes carry a revision and reject stale writes with 409.

Administrators can set status/priority and add private internal notes and images. Members cannot access other tickets or internal attachments. Notifications use the existing inbox, independently of the original ticket record.

| Limit | Default |
| --- | --- |
| Formats | PNG/JPG/JPEG; no SVG, GIF, WebP, archives or animated PNG |
| Image size | At most 2 MiB before and after processing |
| Dimensions | At most 4096 per side and 8 million pixels |
| Reply | Up to 3 images and 4000 text characters; title up to 120 characters |
| Image storage | 20 MiB per ticket, 100 MiB per user; 1 GiB Lite / 5 GiB Pro controller |
| Upload attempts | 30 per user per hourly window, including failures |
| Member tickets | 5 open tickets, 10 new tickets per 24 hours, 20 replies per hour |
| Unsubmitted images | Expire after 15 minutes |
| Closed-ticket images | Retained for 90 days; reopening restarts retention |

The server checks extension, MIME, decoded format, size and pixels; strips metadata and re-encodes content while preserving JPEG orientation. A single temporary worker subprocess has CPU, memory and time limits. Busy workers reject additional uploads. Images are refused below 1 GiB or 10% free disk space; text tickets remain available.

Images are encrypted SQL records with authenticated access, so ownership and content are backed up atomically. Storage limits count normalized image bytes; database encoding adds overhead. Ticket text and financial records are retained indefinitely in this version.

## Fleet, archives and recovery

The Pro controller owns its members’ balances, codes, orders and tickets. A cross-site plan creates one order rather than a charge per site. Legacy pull agents have no controller financial/support data or APIs. Token-connected sub-sites keep their own data and default to payments disabled on first takeover; financial services must be managed locally rather than through the remote gateway.

Legacy assigned sites must support entitlement/period protocol 2. Upgrade unsupported sites first. Configured reservations do not block listing, purchase or renewal simply because they exceed the member’s remaining quota; runtime grants are bounded by the available quota and are withdrawn when exhausted. Offline old grants must be revoked and settled before a new period can be issued. After controller provisioning, nodes become usable according to business-site synchronization.

No-logs mode preserves financial, support and entitlement business records. User deletion with financial/support history becomes archival after active orders and balances are settled. IDs and histories are not reused. Archived accounts are read-only.

Full backups include encrypted images and the recovery key. Portable snapshots and SQLite/PostgreSQL migration validate ledger consistency. Row/file streaming limits memory; the database restore limit is 8 GiB. Reserve adequate disk space for backups and encoding overhead.

Commerce started with `005_commerce.sql`; payment integration was introduced by `013_payment_integration.sql`, and refund requests by `017_commerce_refund_requests.sql`. Their records are included in current full backups. Direct downgrade to a binary without these migrations is blocked. Disaster recovery to an older version requires a matching full backup; later transactions cannot be reconstructed automatically. See [installation](INSTALL.md), [operations](OPERATIONS.md) and the [Chinese detailed guide](COMMERCE.md).
