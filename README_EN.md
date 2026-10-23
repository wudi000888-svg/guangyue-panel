<!-- language-navigation -->
[简体中文](README.md) · [English](README_EN.md) · [فارسی](README.fa_IR.md) · [العربية](README.ar_EG.md) · [Español](README.es_ES.md) · [Русский](README.ru_RU.md) · [Türkçe](README.tr_TR.md)
<!-- /language-navigation -->

<div align="center">

<img src="docs/assets/banner.svg" alt="Guangyue Panel: one-click deployment, plan operations, IP and child-site management" width="960" />

# Guangyue Panel · 广月面板

**Start from one domain and bring nodes, plans and child sites into a single workspace.**

[![Latest release](https://img.shields.io/github/v/release/wudi000888-svg/guangyue-panel?color=2563eb)](https://github.com/wudi000888-svg/guangyue-panel/releases/latest)
[![CI](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/wudi000888-svg/guangyue-panel/actions/workflows/ci.yml)
[![LGPL-3.0](https://img.shields.io/badge/License-LGPL--3.0-blue.svg)](LICENSE)

[Install](docs/INSTALL.md) · [Download latest](https://github.com/wudi000888-svg/guangyue-panel/releases/latest) · [User guide](docs/USER_GUIDE.md) · [Documentation](docs/README.md)

</div>

Guangyue is a self-hosted panel for proxy services and network resources, built with Go and Vue 3. With a suitable server and a domain that already resolves to it, you can deploy VLESS Reality/Vision and Hysteria 2, then manage plan subscriptions, IP exits, independent child sites, orders, tickets and payments from the panel.

It suits proxy service operators starting from a single site, administrators who need to manage several independent sites centrally, and teams that assign IP exits to members. Lite is for standalone operation; Pro extends it into a master console. Both are installed by the same interactive script.

## Get started

### Before you install

- **The domain must already resolve to this server, without CDN proxying.** In Cloudflare, choose "DNS only" and turn off the orange cloud.
- Use a **full Linux amd64 (x86_64) systemd server** with root / sudo access. Preflight accepts Debian 12/13 and Ubuntu 22.04/24.04; native VM installation tests currently cover Ubuntu 22.04/24.04. An accepted OS does not mean every server configuration has been tested.
- **Public TCP 443 and UDP 443 must be free and allowed**, in both the cloud security group and the host firewall. The script always uses 443 and does not migrate an existing HTTPS service; an existing Nginx service on HTTP 80 can stay.
- **Docker is the default; native installation is optional.** Docker currently publishes IPv4 only, so configure A records only, without AAAA; choose native dual-stack installation if you need IPv6. Docker requires Linux 5.4 or newer.
- Start with 1 CPU / 1 GiB for Lite and 2 CPU / 2 GiB for Pro, and leave room for images, data and backups. This is an installation recommendation, not a promise of concurrent users or bandwidth.

### Run the installer

Copy the entire block into your server terminal:

```bash
gy_install_file=$(mktemp)
curl --proto '=https' --proto-redir '=https' -fsSL \
  https://github.com/wudi000888-svg/guangyue-panel/releases/latest/download/install.sh \
  -o "$gy_install_file" && sudo bash "$gy_install_file"
rm -f "$gy_install_file"
```

The wizard asks you to choose Lite or Pro, the domain and the deployment method. It checks the system, resources, DNS, ports and any existing Nginx / Docker configuration, then shows an installation plan for you to confirm. Runtime components and Docker images come from the selected release and are verified against its checksums; no image registry account is needed. Certificates are issued and renewed automatically through TLS-ALPN-01 on TCP 443.

After installation, read the private initial account file `/var/lib/guangyue/initial-owner.json` as the script instructs, sign in and change the administrator password, then test VLESS and HY2 with a real client. Configure registration, plan listings and payment channels as needed; external payment and email services require your own working configuration.

`--check` runs a read-only preflight, and `--resume` continues an interrupted installation. Existing sites should use [online update and rollback](docs/UPDATES.md) instead of overwriting their data with a fresh installation. See the [installation guide](docs/INSTALL.md) for full requirements, failure recovery and deployment limits.

## Choose Lite or Pro

| | Lite | Pro |
| --- | --- | --- |
| Default role | Standalone site; can also act as a child site | Master site that manages child sites |
| Database | SQLite | PostgreSQL |
| Cache and tasks | Bounded in-process cache, persistent task queue | Redis cache, persistent task queue |
| Local features | Nodes, IP pool, plans, users, orders and tickets | Same as Lite, plus centralized management |
| Connection | Generates a child-site token for the master to import | Paste a child-site token to connect |
| Installation | Docker / native systemd | Docker / native systemd |

Installation needs no registration file and no pre-created site groups. **The child site generates a token; the Pro master pastes it in.** Tokens can have an expiry or stay valid until revoked. A connected child keeps its local administrators, accounts, database and independent services, and access can be revoked. A child site taken over for the first time has its payment module disabled by default; its local administrator can still enable it. See [editions](docs/EDITIONS.md) and [site management](docs/BUSINESS-SITES.md).

## What you can do

| Area | Capabilities |
| --- | --- |
| Nodes and subscriptions | VLESS Reality/Vision, HY2; Raw / Base64 / Mihomo subscriptions, copy and QR codes; official client links per platform, with optional downloads relayed by your site |
| Plan operations | Grant access by source node group or by selected nodes within a group; allowance and validity, free listings, purchase limits, queued plans; per-plan node rates in steps of 0.5, up to 10× |
| Orders and support | On-site balance, redemption codes, orders, refund requests / cancellation / approval / rejection, image tickets, internal administrator notes and inbox notifications |
| IP resources | HTTP CONNECT / SOCKS5 exits, authorized upstream subscriptions, automatic source updates, quality checks, DNS and IPv6 policies; public-resource navigation is hidden by default and can be shown as a per-browser preference |
| Child-site management | Token connection, permissions and service operations, shared budgets with cooldown recovery, node mounting, child IP exits, supported version checks with update and rollback |
| Monitoring | User traffic charts, VLESS session and HY2 connection sampling, service status, task progress and restart recovery |
| Email and payments | SMTP email, EPay V1, Stripe Checkout, crypto payments for supported chains and allowlisted assets; external channels need configuration and acceptance testing |
| Wallets | Built-in HD hot wallet / external public-key import, permanent order addresses, balances and funded addresses, Gas estimates, administrator-approved sweeps |
| Daily workspace | Chinese and English interface, light and dark themes, mobile layout, adjustable sidebar, country access rules, backup / restore and version management |

### Plans decide which nodes members can use

Administrators choose local, child-site or public node groups for a plan, or specific nodes within a group. Members see their current plan, remaining time and allowance, subscription entry and clients, without needing to understand node groups or the internal rules of mixed subscriptions.

Buying another plan pauses the remaining validity of the current one, which resumes according to the entitlement queue. Refunds require administrator review: the active plan is refunded by remaining allowance or time, queued orders currently return their original order amount, and the restoration order of other entitlements is preserved. See [plans](docs/PLANS.md) and [orders and refunds](docs/COMMERCE_EN.md) for the exact rules. Changes to the active plan's nodes, rates and protocols synchronize server-side access; allowance, validity and queued plans keep their snapshots. Subscriptions cached by clients still need to be refreshed.

### Child-site nodes and child-site IPs are two different paths

- **Mounted child nodes:** the client connects directly to the child node; traffic does not pass through the master.
- **Child IP exits:** the client connects to a master node, and the master relays traffic through the child's IP exit.

The two resource types are managed separately and labeled by source. When a child site's shared budget runs out, access pauses as configured and resumes after the cooldown; node-group memberships are preserved. See [node mounting and budgets](docs/NODE_MOUNTS.md) for the setup.

### Crypto payments and wallets

When a user pays with cryptocurrency, the order receives its own permanently retained address. Automatic collection and sweeping currently cover the supported USDT / USDC assets on Ethereum, BNB Smart Chain, Plasma and Solana, as defined by the contract or mint allowlist; configuring an L2 does not mean automatic settlement and sweeping are supported for it.

Balance and Gas queries are read-only. **Fees are paid only when an administrator approves a sweep.** Automatic fee support applies only to genuine order addresses whose balance of the selected allowlisted stablecoin is strictly greater than 0.1. EVM tops up only the shortfall; Solana pays fees from a fixed SOL funding address. If the originally approved fee ceiling would be exceeded, the operation pauses for manual review. See [wallet management](docs/CRYPTO_WALLETS.md) and [order collection and sweeping](docs/CRYPTO_TREASURY.md) for details and chain differences.

The built-in hot wallet is meant for small operating balances. Although the mnemonic is stored encrypted, signing authority remains on the same server: back it up privately and withdraw funds regularly. An external EVM xpub can be used for address derivation and lookups while transfers are made from the external wallet; it does not give the server signing ability.

## Scope and limits

- The inbound protocols provided directly are **VLESS Reality/Vision and Hysteria 2**. Importable upstream formats depend on the parser and Mihomo, and do not add native inbounds of the same name.
- Deployment is currently a single-site controller. There is no promise of multi-master high availability for one site, organization-level tenant isolation, an SLA or an independent security audit. Pro's database and cache do not improve a network route that is itself congested.
- CI covers isolated Linux installation, update recovery and protocol verification. That is not a large-scale production capacity report, nor acceptance of your merchant setup or mainnet transactions. Before going live, test real clients, backups, email and payment flows.
- RPC, market-quote and quality-check sources may be rate-limited or unavailable. Unknown results are never treated as zero fees, and reaching a public page does not mean that accounts, playback, payments or regional services are all unlocked.
- This repository provides **project introductions in 7 languages**; the panel interface currently supports **Simplified Chinese and English**. Detailed manuals are mainly in Chinese, some with English versions; a translated introduction does not mean the interface and manuals are localized into that language.

## Find docs by task

| What do you want to do? | Start here |
| --- | --- |
| Install and choose an edition | [One-click installation](docs/INSTALL.md) · [Docker](docs/DOCKER.md) · [Lite / Pro](docs/EDITIONS.md) |
| Configure plans, sales and member subscriptions | [User guide](docs/USER_GUIDE.md) · [Plans](docs/PLANS.md) · [Orders and payments](docs/COMMERCE_EN.md) |
| Connect child sites, mount nodes and assign exits | [Site management](docs/BUSINESS-SITES.md) · [Node mounting](docs/NODE_MOUNTS.md) · [Live monitoring](docs/LIVE-MONITORING.md) |
| Configure email, crypto payments and sweeps | [Email](docs/EMAIL.md) · [Wallets](docs/CRYPTO_WALLETS.md) · [Collection and sweeping](docs/CRYPTO_TREASURY.md) |
| Operate, troubleshoot and update | [Operations](docs/OPERATIONS.md) · [Updates](docs/UPDATES.md) · [Configuration](docs/CONFIGURATION.md) |
| Develop and report security issues | [Contributing](CONTRIBUTING.md) · [Architecture](docs/ARCHITECTURE.md) · [Security](SECURITY.md) |

See the [documentation index](docs/README.md) for the full list. Design material is in the [Penpot design source](docs/designs/penpot/README.md) and the [interface design system](docs/designs/penpot-courtyard-system.md).

## Open source and contributing

Reproducible issues, suggestions and pull requests are welcome. When reporting a problem, include the version, edition, deployment method, site role and reproduction steps; do not upload real subscription credentials, private keys, mnemonics or complete production configurations. See [CONTRIBUTING](CONTRIBUTING.md) for the verification required for code and documentation changes, and use the [private reporting process](SECURITY.md) for security issues.

Guangyue Panel is **LGPL-3.0-only** since 0.15.0. The original fake-ui MIT notice is kept in the [legacy license](licenses/guangyue-legacy-MIT.txt), together with the [GPL-3.0 text](licenses/GPL-3.0.txt). The custom Hysteria patch is MIT, Xray is MPL-2.0 and Mihomo is GPL-3.0; see the [third-party notices](THIRD_PARTY_NOTICES.md) for component attribution and corresponding source.

The structure of this introduction follows [3x-ui](https://github.com/MHSanaei/3x-ui), and the multilingual content is written to match Guangyue's actual capabilities; the earlier documentation structure was inspired by Sub2API, without using its branding or screenshots. Complete releases provide runtime packages, the installer, Docker image archives, a checksum manifest and dependency information; see [release notes](docs/RELEASE.md). If the project helps you, a Star or feedback from a real installation is welcome.
