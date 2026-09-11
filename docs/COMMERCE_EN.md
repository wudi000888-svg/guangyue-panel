# Balances, redemption codes, orders and support

Available in **0.21.0+**, on Lite and Pro controllers. Open **Account & services** in either Simple or Professional mode. Members see their own records; administrators manage all members. No external payment provider is required or connected.

## Enable the services

1. Create a plan with the required node groups, allowance, period, protocols, validity and “Package price / CNY”. Prices are stored as integer cents and plan edits create a new template version.
2. In **Buy a plan**, create an offer with a plan version and CNY price, then enable sales for that offer.
3. In **System settings → Balance, redemption codes and support**, enable plan purchases and verify the current administrator password.
4. Credit members using redemption codes or an administrator balance adjustment.

Upgrades start with sales disabled, redemption and tickets enabled. Existing wallets start at zero; existing plans are not automatically priced or listed. Historic usage is not charged. Disabling an entry point preserves its records. Manual entitlements remain available.

## Redemption codes

Administrators can generate 1–100 codes per batch, choose an amount with up to two decimals, set an expiry date/time or no expiry, and add a batch note. The default expiry is 30 days; finite expiry is limited to ten years. At most 10,000 valid unused codes are allowed.

Save or download the full codes immediately. The list shows only suffixes, values, expiry, batch and status. Select unused codes and verify the administrator password to revoke them permanently. Revoking a redeemed code never deducts credited balance.

Members enter the code in **Account balance → Redeem a code**. Funds go to the signed-in account once. Concurrent redemption and retries cannot credit a code twice. There are up to 10 attempts per user per ten-minute window. Expiry uses server time; the UI displays local time.

Codes use 128 random bits. Code records store a hash and suffix. Original generation responses are encrypted with the site's master key to support retries after a lost response. Normal listing does not recover full codes.

## Balance and orders

Since 0.21.2, the header shows the signed-in account’s available balance and opens the wallet when clicked, including on mobile. The tooltip and accessible label include held funds. When an administrator selects another member in the wallet, the header continues to show the administrator’s own balance. Redemption, adjustments and order actions refresh it immediately; visible pages also refresh every 30 seconds and when revisited. Loading or failed reads display “—” instead of a misleading zero. Local balance is hidden while managing an independent remote site.

Amounts are stored as integer cents. Minimum positive amount: CNY 0.01. Maximum transaction amount and total account balance: CNY 1 billion. An authenticated owner session can credit, gift or debit an unarchived account without a second password prompt. A reason is still required, every change is audited in the immutable ledger and integrity-checked, and regular members cannot adjust balances. Negative balances are prohibited.

Every movement posts balanced ledger entries and updates the wallet in one transaction. Startup reconciliation and wallet checkpoints detect inconsistencies and block financial writes. These are internal accounting records, not proof of an externally verified payment. There are no withdrawals, transfers, overdrafts or automatic renewals. Node multipliers consume quota, not additional cash.

Creating an order generates a unique `GYO-` number and snapshots its price and entitlement. The order expires after 15 minutes without confirmation. Confirmation holds the balance; successful entitlement provisioning captures it in the same transaction. Cancellation releases any held funds. State and retries persist across process restarts; retries back off to 60 seconds. Provisioning times out after 30 minutes and releases funds.

Supported purchases: initial activation, reactivation after expiry, and renewal of the **same plan version**. Renewal extends expiry without clearing current period usage or moving its boundary. Permanent entitlements do not require renewal. Cross-plan prorated upgrades and downgrades are not available.

Administrators can approve a full refund. The system first revokes and settles the order entitlement, then returns the amount to account balance. A first-purchase refund ends its entitlement without restoring a previous free plan. A renewal refund restores the earlier expiry and preserves usage. Later entitlement changes block automatic refunds. Partial refunds are not implemented.

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

Migration `005_commerce.sql` prevents direct downgrade to 0.20.x or earlier. Disaster recovery to an older version requires a matching full backup; later transactions cannot be reconstructed automatically. See [installation](INSTALL.md), [operations](OPERATIONS.md) and the [Chinese detailed guide](COMMERCE.md).
