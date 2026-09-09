# Guangyue Panel · 广月面板

**A next-generation enterprise solution for cross-border e-commerce teams.**

A lightweight, single-VPS console for users, proxy nodes, private egress resources and a separate public proxy pool. Built with Go, SQLite and Vue 3; Redis, PostgreSQL and Docker are not required.

[中文](README.md) · [Installation](docs/INSTALL.md) · [User guide](docs/USER_GUIDE.md) · [Operations](docs/OPERATIONS.md)

## Features

- Multi-user VLESS Reality/Vision and Hysteria 2, including protected default direct nodes.
- Nginx TCP 443 SNI routing alongside website HTTPS; HY2 shares the port number over UDP.
- Private HTTP CONNECT / SOCKS5 exits, subscription and single-node imports, source ownership and staggered daily updates.
- Independently managed public sources, blacklist, bounded health checks and separate subscription credentials.
- User expiry, protocol permissions, shared quotas, node links, QR codes and member-visible quality evidence.
- Chinese / English, light / dark themes, simple / professional navigation, inbox and site settings.
- Per-node DNS/IPv6 policy, encrypted stored credentials, backup/restore, normal/no-logs runtime modes.

## Deployment

The release installer supports **Linux amd64 with Debian 12/13 or Ubuntu 22.04/24.04 and systemd**. Start with 1 CPU / 1 GiB RAM; actual usage depends on active cores, traffic, probes and imported resources. This is a single-host product, not a high-availability or tenant-isolation platform.

1. Point one or two DNS names to your VPS. One name works; separate panel and node names are recommended. Open TCP 80/443 and UDP 443.
2. Follow the [complete installation guide](docs/INSTALL.md) for packages, ACME certificates and checksum-verified release artifacts.
3. Fetch pinned upstream Xray/Mihomo using the included scripts. Our application release does not redistribute those binaries.
4. Run `python3 deploy/install.py --help`. Preflight is read-only; installation requires `--apply`.
5. Read initial owner credentials locally on the server, sign in, change the password, create a member and import the member subscription into a compatible client.

For an existing HTTPS server, follow [Nginx coexistence](docs/NGINX.md) before applying the installer. It refuses occupied ports and existing installations. There is no Docker deployment promise in this release.

## Evidence and limits

Quality reports retain provider, timestamp, unavailable and conflicting results. Reaching a streaming or AI service's public page does not guarantee account access, playback or regional entitlement. DNS policy covers traffic traversing the managed node; a client application can bypass it unless configured appropriately. Public proxy collection is optional and should not carry sensitive business traffic.

## Development and licenses

See [CONTRIBUTING](CONTRIBUTING.md) for tests and reproducible build instructions, [architecture](docs/ARCHITECTURE.md), [configuration](docs/CONFIGURATION.md), [security](SECURITY.md), and [release preparation](docs/RELEASE.md).

The panel is MIT licensed, retaining the original fake-ui notice. The custom Hysteria patch is MIT. Xray is MPL-2.0 and Mihomo is GPL-3.0; see [third-party notices](THIRD_PARTY_NOTICES.md). Documentation structure is inspired by Sub2API; its branding and screenshots are not included.
