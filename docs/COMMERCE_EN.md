# Balances, redemption codes, orders and support

Available in **0.21.0+**, on Lite and Pro controllers. Open **Account & services** in the unified panel. Members see their own records; administrators manage all members. The panel works without an external payment provider, and optional signed adapters can be configured for online checkout.

## Enable the services

1. Create a plan with the required node groups, allowance, period, protocols, validity and “Package price / CNY”. Prices are stored as integer cents and plan edits create a new template version.
2. In **Buy a plan**, create an offer with a plan version and CNY price, then enable sales for that offer.
3. In **System settings → Balance, redemption codes and support**, enable plan purchases and verify the current administrator password.
4. Credit members using redemption codes or an administrator balance adjustment.

## Online payments

Open **System settings → Commerce and integrations → Online payments**. Newly created methods are disabled by default. Hosted checkout supports **EPay V1 MD5** and **Stripe Checkout**. The generic JSON Webhook adapter is retained for existing integrations and does not offer a customer checkout. Prices and wallet balances use integer CNY cents.

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

Approve a completed plan's refund in **Orders** first. After entitlement revocation and settlement, an external-payment order becomes `refund_pending` (**awaiting original-method refund**). This does **not** mean funds have been returned. Open **Online payments → Receipts and refunds** and select the actual receipt ID:

- **Stripe:** Request an original-method refund. The worker uses a stable idempotency key; only a successful API response, signed webhook or subsequent retrieval with `succeeded` completes it. Pending and failed responses remain visible and never count as refunded.
- **EPay:** Complete the real refund in the merchant console first. Enter the actual refund reference and reason, and explicitly confirm that funds were returned. The panel records the verified administrator confirmation; it does not send an EPay refund or credit the internal wallet.
- **Unknown-order receipts:** Reconcile the merchant statement and complete any actual refund before recording manual confirmation. Such receipts cannot automatically fund a user or plan.

Refund references are unique within the merchant account. External payments never silently become internal-wallet refunds. Wallet-funded orders retain the existing wallet-return behavior after entitlement revocation. Refund operations require the current administrator password, which is cleared from the form after use.

Fresh installations default to sales disabled, with redemption and support enabled. Upgrades preserve existing settings. Disabling new sales or payment methods retains historical settlement and after-sales workflows. Without merchant credentials, installation or release checks do not constitute a real-payment acceptance test.

## Redemption codes

Administrators can generate 1–100 codes per batch, choose an amount with up to two decimals, set an expiry date/time or no expiry, and add a batch note. The default expiry is 30 days; finite expiry is limited to ten years. At most 10,000 valid unused codes are allowed.

Save or download the full codes immediately. The list shows only suffixes, values, expiry, batch and status. Select unused codes and verify the administrator password to revoke them permanently. Revoking a redeemed code never deducts credited balance.

Members enter the code in **Account balance → Redeem a code**. Funds go to the signed-in account once. Concurrent redemption and retries cannot credit a code twice. There are up to 10 attempts per user per ten-minute window. Expiry uses server time; the UI displays local time.

Codes use 128 random bits. Code records store a hash and suffix. Original generation responses are encrypted with the site's master key to support retries after a lost response. Normal listing does not recover full codes.

## Balance and orders

Since 0.21.2, the header shows the signed-in account’s available balance and opens the wallet when clicked, including on mobile. The tooltip and accessible label include held funds. When an administrator selects another member in the wallet, the header continues to show the administrator’s own balance. Redemption, adjustments and order actions refresh it immediately; visible pages also refresh every 30 seconds and when revisited. Loading or failed reads display “—” instead of a misleading zero. Local balance is hidden while managing an independent remote site.

Amounts are stored as integer cents. Minimum positive amount: CNY 0.01. Maximum transaction amount and total account balance: CNY 1 billion. An authenticated owner session can credit, gift or debit an unarchived account without a second password prompt. A reason is still required, every change is audited in the immutable ledger and integrity-checked, and regular members cannot adjust balances. Negative balances are prohibited.

Every movement posts balanced ledger entries and updates the wallet in one transaction. Startup reconciliation and wallet checkpoints detect inconsistencies and block financial writes. These are internal accounting records, not proof of an externally verified payment. There are no withdrawals, transfers, overdrafts or automatic renewals. Node multipliers consume quota, not additional cash.

Creating an order generates a unique `GYO-` number and snapshots its price and entitlement. The order expires after 15 minutes without confirmation. Wallet confirmation holds the balance; successful entitlement provisioning captures it in the same transaction. Online payment confirmation starts provisioning without a wallet hold. Cancellation releases any held funds. State and retries persist across process restarts; retries back off to 60 seconds. Provisioning times out after 30 minutes: wallet funds are released and external payments require original-method refund handling.

Supported purchases: initial activation, reactivation after expiry, and renewal of the **same plan version**. Renewal extends expiry without clearing current period usage or moving its boundary. Permanent entitlements do not require renewal. Cross-plan prorated upgrades and downgrades are not available.

Administrators can approve a full refund. The system first revokes and settles the order entitlement, then returns wallet-funded orders to account balance or moves external payments to the original-method refund workflow above. A first-purchase refund ends its entitlement without restoring a previous free plan. A renewal refund restores the earlier expiry and preserves usage. Later entitlement changes block automatic refunds. Partial refunds are not implemented.

Active orders exclude conflicting manual entitlement and business-site membership changes. Old connections and business-site grants must settle before a new paid quota period is issued.

## Support tickets

Enabled signed-in members can create, reply to and close tickets. Expired or quota-exhausted accounts retain portal access. Members can reopen a closed/resolved ticket within seven days, subject to the open-ticket limit. Categories cover account/balance, orders/plans, nodes/connectivity, usage and other issues. Members can link their own orders and authorized nodes. Resource labels survive node deletion.

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

The Pro controller owns wallets, codes, orders and tickets. A cross-site plan creates one order, not one charge per site. Business agents have no financial/support data or APIs. Legacy independent-site relays cannot forward account-service requests.

Assigned sites must support entitlement/period protocol 2. Unsupported sites or incompatible quota reservations block purchases. Offline old grants must be revoked and settled before a new period can be issued. After controller provisioning, nodes become usable according to business-site synchronization.

No-logs mode preserves financial, support and entitlement business records. User deletion with financial/support history becomes archival after active orders and balances are settled. IDs and histories are not reused. Archived accounts are read-only.

Full backups include encrypted images and the recovery key. Portable snapshots and SQLite/PostgreSQL migration validate ledger consistency. Row/file streaming limits memory; the database restore limit is 8 GiB. Reserve adequate disk space for backups and encoding overhead.

Commerce started with `005_commerce.sql`; payment integration adds `013_payment_integration.sql`, including receipt and refund tables in backups. Direct downgrade to a binary without these migrations is blocked. Disaster recovery to an older version requires a matching full backup; later transactions cannot be reconstructed automatically. See [installation](INSTALL.md), [operations](OPERATIONS.md) and the [Chinese detailed guide](COMMERCE.md).
