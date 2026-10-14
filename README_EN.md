![Guangyue Panel: plans, nodes and sites in one console](docs/assets/banner.svg)

# Guangyue Panel · 广月面板

**One domain. Plans, nodes and sites in one console.**

A self-hosted panel for proxy services and network resources. Deploy VLESS Reality/Vision and Hysteria 2, manage IP exits and independent sites, and deliver subscriptions through plans. Built with Go and Vue 3, with Lite for a standalone site and Pro for centralized site management.

[![Latest release](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![License: LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

[Install](docs/INSTALL.md) · [Documentation](docs/README.md) · [Releases](https://github.com/wudi000888-svg/guangyue-panel/releases) · [User guide](docs/USER_GUIDE.md) · [Report a problem](https://github.com/wudi000888-svg/guangyue-panel/issues/new/choose)

## Get started

Prepare a server and a domain, then run the installer. The wizard asks for the edition and domain, checks the environment, and prepares the application, proxy cores, certificates and required database. **Docker is the default; native systemd installation is also available.** The panel and default nodes can share one domain.

- Point the domain directly to your server and **disable CDN proxying**. For Cloudflare, select **DNS only**, with the orange cloud off.
- Public **TCP 443 and UDP 443 must be free and allowed** through the host firewall and cloud security group. The installer does not choose another public port or take over an existing HTTPS service.
- Use a full Linux amd64/x86_64 server with root or sudo access. Preflight accepts Debian 12/13 and Ubuntu 22.04/24.04; native VM installation tests currently cover Ubuntu 22.04/24.04. An accepted OS is not a guarantee that every server configuration has been tested.
- Docker currently publishes IPv4 only: use A records without AAAA. Choose native installation for IPv6. Start with 1 CPU / 1 GiB for Lite or 2 CPU / 2 GiB for Pro, and reserve additional space for data and backups.

Copy the entire block into your server terminal:

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

The installer checks DNS, ports, resources and existing Nginx/Docker configuration before showing a plan for confirmation. Certificates are issued and renewed through TLS-ALPN-01 on TCP 443; existing Nginx HTTP 80 configuration is preserved. Runtime components and Docker images come from the selected complete release and are verified against its checksums.

After installation, use the private initial account file shown by the installer, change the administrator password, and test both VLESS and HY2 with a real client. Configure registration and listed plan offers before opening the site to members. The payment module is enabled by default for standalone sites; external payment channels still need valid credentials and setup. A site first taken over as a managed child defaults to payments disabled. Existing sites should use [update and rollback](docs/UPDATES.md), rather than reinstalling over their data.

See [installation requirements and recovery](docs/INSTALL.md) for the full procedure, including read-only `--check`, interrupted-install `--resume` and the supported deployment limits.

## Choose Lite or Pro

| | Lite | Pro |
| --- | --- | --- |
| Start here | One independently operated site | A master site managing other sites |
| Persistent data | SQLite | PostgreSQL |
| Cache and tasks | Bounded in-process cache and persistent task queue | Redis cache and persistent task queue |
| Connect to a master | Generate a child-site token | Generate a child-site token when acting as a child |
| Manage child sites | Not available | Paste a child-site token to connect and manage |
| Deployment | Docker or native systemd | Docker or native systemd |

Neither edition needs a registration file. **The child site generates the token; Pro imports it.** Tokens can have an expiry or remain valid until revoked. Connected sites retain their own administrators, accounts, database and local services; the child administrator can revoke access. See [editions](docs/EDITIONS.md) and [site management](docs/BUSINESS-SITES.md).

## What you can manage

### Nodes, IP exits and child sites

Run local VLESS Reality/Vision and HY2 nodes, add HTTP CONNECT or SOCKS5 exits, and import authorized upstream subscriptions. Public-resource navigation is hidden by default and can be shown in the current browser. This display preference does not stop existing collection or revoke issued access.

The two child-site paths have distinct purposes:

| Resource | Traffic path |
| --- | --- |
| **Mounted child nodes** | The client connects to the child node directly; traffic does not pass through the master. |
| **Child IP exits** | The client connects to a master node; the master relays traffic through the child's IP exit. |

Pro can mount shared child nodes, manage site permissions, view sampled usage and connection counts, set shared budgets and cooldowns, and check or control supported child-site updates. Local, child and public nodes remain identifiable by source. See [node mounting and budgets](docs/NODE_MOUNTS.md) and [fleet management](docs/BUSINESS-SITES.md).

### Plans and subscriptions

Plans determine which nodes a member can use. Administrators choose complete source groups or selected nodes within a group, set allowance and validity, and apply node rates to an individual plan. Members see their current plan, remaining time and allowance, subscription and compatible client downloads.

Buying a different plan pauses the remaining validity of the earlier plan; the earlier entitlement resumes according to the queue when the active plan ends. Orders, balance records, redemption codes and administrator-reviewed refunds provide a traceable workflow. Active entitlements are refunded according to remaining allowance or time; queued orders currently return their original order amount, preserving the restoration order of other entitlements. See [plans and accounting](docs/PLANS.md) and [commerce](docs/COMMERCE_EN.md).

### Payments, wallets and support

Configure EPay V1 or Stripe Checkout for online payments, SMTP for email, and tickets for member support. External services require their own working credentials and acceptance testing; installation does not create a merchant account or prove a live payment flow.

Crypto payments can allocate a permanent order address when that payment method is selected. Administrators can inspect funded addresses, supported-chain balances and Gas estimates, then approve a sweep to a chosen destination. Fees are paid only after an administrator approves collection. Automatic fee support requires a genuine order address with more than 0.1 of the selected allowlisted stablecoin. EVM funds only the shortfall; Solana uses its fixed SOL fee payer. Operations exceeding the approved fee ceiling pause for manual review. The built-in Trust Wallet Core hot-wallet option and external public-key import serve different needs: external xpub wallets are watch-only for spending. Current automatic collection and sweeping cover Ethereum, BNB Smart Chain, Plasma and Solana for the supported USDT/USDC assets. L2 configurations do not imply automatic settlement or sweeping support. See [crypto treasury rules and limits](docs/CRYPTO_TREASURY.md).

### Day-to-day operations

Chinese and English interfaces, light and dark themes, mobile layouts, configurable navigation, inbox notifications, node quality checks, country-based access rules, backup/restore and version updates are included. Quality results retain their source, time and unavailable state. Use [operations](docs/OPERATIONS.md) and [validation records](docs/VALIDATION.md) to understand what has been checked.

## Scope and limits

- **Two direct protocols:** VLESS Reality/Vision and Hysteria 2. Importable upstream formats are a separate capability and depend on parser and Mihomo support.
- **A site-oriented deployment:** current install assets target Linux amd64 on full systemd servers. The project does not promise an SLA, automatic cross-site failover, organization-level tenant isolation or multi-instance high availability.
- **A control panel, not a bandwidth guarantee:** proxy cores and network paths determine forwarding performance. Pro's database and cache do not make a slow route faster. Traffic and connection views are sampled, with bounded delays.
- **Client updates still matter:** changes to the active plan nodes, rates and protocols synchronize server-side access. Allowance, validity and queued entitlements retain their snapshots; a client retaining an old subscription must refresh it. Revocation is enforced by the relevant core and site synchronization process.
- **Hot wallets are for small operating balances:** encrypted storage does not isolate signing authority from a compromised panel server. Back up privately, withdraw regularly, and use external public-key import when server-side signing is unnecessary. No independent security audit is claimed.
- **Public services can fail:** built-in RPC endpoints and market quotes remain subject to outages and limits. Missing or stale results are shown as unavailable; a displayed quote is not a guaranteed transaction fee, settlement rate or mainnet acceptance test.
- **Quality checks are evidence, not access promises:** reaching a streaming or AI service's public page does not prove account access, playback or regional rights. Optional public proxies should not carry sensitive traffic.

## Find the right guide

| I want to… | Start here |
| --- | --- |
| Install my first site | [One-click installation](docs/INSTALL.md) · [Docker operations](docs/DOCKER.md) |
| Choose an edition or connect child sites | [Lite and Pro](docs/EDITIONS.md) · [Site management](docs/BUSINESS-SITES.md) |
| Share child nodes or use child IP exits | [Node mounting](docs/NODE_MOUNTS.md) |
| Configure plans and member access | [Plans](docs/PLANS.md) · [User guide](docs/USER_GUIDE.md) |
| Enable sales, payments and support | [Commerce](docs/COMMERCE_EN.md) · [Crypto treasury](docs/CRYPTO_TREASURY.md) |
| Back up, troubleshoot, update or roll back | [Operations](docs/OPERATIONS.md) · [Updates](docs/UPDATES.md) |
| Develop, test or report a vulnerability | [Contributing](CONTRIBUTING.md) · [Architecture](docs/ARCHITECTURE.md) · [Security](SECURITY.md) |

Most detailed deployment and resource-management guides are currently in Chinese. Their configuration examples and command names are shared by both interface languages.

## Source, contributions and licenses

Bug reports should include the version, edition, deployment mode, role and reproducible steps using synthetic data. Do not upload production configurations, wallet material or subscription credentials. Follow [CONTRIBUTING](CONTRIBUTING.md) for testing and [SECURITY](SECURITY.md) for private vulnerability reporting.

Guangyue Panel is **LGPL-3.0-only** from 0.15.0. The original fake-ui MIT notice remains in [the legacy notice](licenses/guangyue-legacy-MIT.txt), and the accompanying [GPL-3.0 text](licenses/GPL-3.0.txt) is included. The custom Hysteria patch is MIT; Xray is MPL-2.0 and Mihomo is GPL-3.0. See [third-party notices](THIRD_PARTY_NOTICES.md) for component attribution and corresponding source. The multilingual introduction structure follows [3x-ui](https://github.com/MHSanaei/3x-ui). Earlier documentation structure was inspired by Sub2API; its branding and screenshots are not included.

Releases include runtime packages, the installer, Docker image archives, checksums and dependency information. See [release preparation](docs/RELEASE.md) for the eight required assets and source distribution. Checksums detect changed or damaged files; they do not replace trust in the release publisher.
