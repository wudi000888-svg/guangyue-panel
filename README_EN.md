<!-- Current deployment roles are documented below. -->
# Guangyue Panel · 广月面板

**A next-generation enterprise solution for cross-border e-commerce teams.**

A network resource console for cross-border commerce teams. Lite defaults to an independent SQLite site; Pro defaults to the master with PostgreSQL, Redis, durable background tasks, unified subscriptions and full control of sub-site permissions. A pairing token can be generated after setup. Lite can issue tokens but only Pro can use one to take over a site. Both editions share Go, Vue 3 and the proxy cores. See [edition selection and Pro setup](docs/EDITIONS.md).

[中文](README.md) · [One-click installation](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [User guide](docs/USER_GUIDE.md) · [Operations](docs/OPERATIONS.md)

## Features

- Account balances, expiring/revocable redemption codes, plan orders, refunds and private image support tickets; optional Epay and Stripe Checkout; [guide](docs/COMMERCE_EN.md).
- Built-in Trust Wallet Core HD wallet or external xpub. Selecting crypto payment allocates a permanent invoice address; approved Ethereum/BSC USDT/USDC payments activate plans after dual-RPC finality verification. Hot wallets support balance views, a dedicated Gas funding address, immediate Gas funding details for the selected chains, automatic balance refresh and administrator-approved asset sweeping with a frozen fee cap; one-click setup supplies public RPC endpoints and automatic CNY quotes without manual URLs or rates; [guide](docs/CRYPTO_TREASURY.md). External xpub remains watch-only for spending. L2 automatic payment/sweeping remains unavailable pending L1 finality evidence.
- Node traffic multipliers (0–100×), permission groups, versioned plan templates and manual entitlements without payments; [guide](docs/PLANS.md).
- Separate raw and quota usage, 30-day/calendar-month periods and per-node usage reports. Node deletion automatically removes group membership.
- Multi-user VLESS Reality/Vision and Hysteria 2, including protected default direct nodes.
- Interactive Docker or native installation, an independent TCP 443 HTTPS/Reality gateway, automatic TLS certificates and HY2 UDP 443.
- Private HTTP CONNECT / SOCKS5 exits, subscription and single-node imports, source ownership and staggered daily updates.
- Independently managed public sources, blacklist, bounded health checks and separate subscription credentials.
- User expiry, protocol permissions, shared quotas, node links, QR codes and member-visible quality evidence.
- Chinese / English, light / dark themes, customizable navigation, inbox and site settings.
- Per-node DNS/IPv6 policy, encrypted stored credentials, backup/restore, normal/no-logs runtime modes.

## Deployment

From 0.41.0, the installer defaults to **Docker**, with **native systemd** available in the same wizard. Its OS preflight accepts Linux amd64 Debian 12/13 and Ubuntu 22.04/24.04. Consult the release CI for actual VM verification; accepting Debian in preflight is not a claim of completed native Debian VM testing. Start with 1 CPU / 1 GiB for Lite or 2 CPU / 2 GiB for Pro, plus capacity for other applications and backups. Docker requires Linux 5.4 or newer.

1. Point one or two domains directly to the VPS and disable CDN proxying. Docker currently exposes IPv4 only, so use A records; choose native for public IPv6/AAAA. TCP and UDP 443 must both be free and allowed through the security group. Existing Nginx HTTP 80 remains intact.
2. Run the script below, choose the edition, domains and backend, accept ACME terms and review the detected installation plan. Packages, verified components and TLS-ALPN certificates are prepared automatically.
3. Read `/var/lib/guangyue/initial-owner.json` privately, log in as `owner`, change the random password and test real VLESS/HY2 connections.

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

The script pins a complete official release and verifies installer/runtime/image archives. Images are loaded from public release assets without a registry account. `--check` performs read-only inspection; `--resume` retains the original plan, credentials and keys. It refuses foreign port ownership and leaves Nginx configuration intact. Existing Nginx installations keep their original upgrade path; see the [legacy guide](docs/LEGACY_INSTALL.md).

## Evidence and limits

Quality reports retain provider, timestamp, unavailable and conflicting results. Reaching a streaming or AI service's public page does not guarantee account access, playback or regional entitlement. DNS policy covers traffic traversing the managed node; a client application can bypass it unless configured appropriately. Public proxy collection is optional and should not carry sensitive business traffic.

## Development and licenses

See [CONTRIBUTING](CONTRIBUTING.md) for tests and reproducible build instructions, [architecture](docs/ARCHITECTURE.md), [configuration](docs/CONFIGURATION.md), [security](SECURITY.md), and [release preparation](docs/RELEASE.md).

The panel is LGPL-3.0-only licensed from 0.15.0, retaining the original fake-ui MIT notice in `licenses/guangyue-legacy-MIT.txt`. The accompanying GPL-3.0 text is included. The custom Hysteria patch is MIT. Xray is MPL-2.0 and Mihomo is GPL-3.0; see [third-party notices](THIRD_PARTY_NOTICES.md). Documentation structure is inspired by Sub2API; its branding and screenshots are not included.

## Controller and business sites (0.18.0)

Choose `guangyue-panel-lite-…tar.gz` for an independent site or managed child, or `guangyue-panel-pro-…tar.gz` for a master. New sites no longer need a registration file. The Pro master generates a one-time connection token; after installation, a child binds with `--controller-url`, `--site-id` and `--connect-token`. Child sites use SQLite and retain local login, nodes, subscriptions and offline operation. The legacy `--enrollment-file` path remains only for migration. The controller distributes scoped member credentials and exit bindings, aggregates subscriptions, and reserves per-site quotas. Business sites pull over HTTPS and expire authorization after a 15-minute lease. Quality reports are shared by VLESS/HY2 using the same local exit; speed results remain independent. See the [complete business-site guide](docs/BUSINESS-SITES.md) for installation, quotas, recovery and offline limits.

## Online updates

The upper-left version badge lets administrators check official releases, select an update and confirm it. An independent local updater verifies the package, backs up the deployment, restarts services and checks the target version. The page refreshes after an 8-second countdown once healthy. Compatible installed versions can be rolled back without restoring old user or traffic data; the root-owned installation baseline is always enforced. See [update and rollback operations](docs/UPDATES.md).
