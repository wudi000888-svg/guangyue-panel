# Hysteria authenticated node routing

Base: `apernet/hysteria`, tag `app/v2.9.2`, commit `c3a806b5cbbb20fe72099529573da26b1a2e9f22` (MIT).

Build with `bash scripts/build-hy2-core.sh --test`. The upstream source and locked dependency files are retained in the ignored build directory. `GY_GO` can override the Go executable. The output is `build/bin/hysteria-node-linux-amd64`; the upstream license remains in the source checkout.

The patch adds a connection-scoped outbound selector after successful authentication. The trusted HTTP authenticator returns `user.generation.credentialHash@nodeID`. `nodeRouting: true` selects from `nodeOutbounds`; unknown or disabled nodes reject authentication instead of falling back to direct. TCP streams and UDP datagrams use the same immutable route. The Hysteria wire protocol, TLS, congestion control, HTTP / SOCKS5 outbounds, and traffic API remain upstream implementations.

Additional logical nodes receive HMAC-derived per-user passwords. The existing `hy2-main` password remains valid. Rotating a user's base HY2 credential revokes all their node passwords. The node suffix also separates online and traffic identities while retaining the user's shared quota.

The server stays a single process on UDP 443. Route changes restart that process, so other HY2 connections briefly reconnect even though their configured exits are independent. User changes retain the existing HTTP authentication and kick workflow. Do not replace this binary with an unpatched official server: it cannot enforce per-node routes. The panel verifies the custom build marker before rendering or applying HY2 configuration.

Tests cover parsing and unknown-node rejection, and real QUIC connections from two concurrent clients with distinct routes for both TCP streams and UDP datagrams. The patch also publishes authentication state atomically so route selection is visible before streams are accepted.
