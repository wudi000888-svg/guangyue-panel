# PG17 client tools are included for the exact bundled database version.
FROM postgres:17-bookworm@sha256:66aafa11cf15800a3c94763f7e11d1e7b2e37e5e84bc4ce027cb6e1e4bfaf4df
ARG PANEL_VERSION
LABEL org.opencontainers.image.title="Guangyue Panel" \
      org.opencontainers.image.source="https://github.com/wudi000888-svg/guangyue-panel" \
      org.opencontainers.image.version="${PANEL_VERSION}" \
      org.opencontainers.image.licenses="NOASSERTION"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 65532 guangyue \
 && useradd --system --uid 65532 --gid 65532 --home-dir /nonexistent --shell /usr/sbin/nologin guangyue \
 && mkdir -p /opt/guangyue-personal/bin /var/lib/guangyue /run/guangyue-runtime /run/guangyue-reality /var/lib/guangyue-maintenance \
 && chmod 0700 /var/lib/guangyue /run/guangyue-runtime \
 && chown 65532:65532 /var/lib/guangyue /run/guangyue-runtime /run/guangyue-reality \
 && ln -s /var/lib/guangyue-maintenance/update.sock /run/guangyue-update.sock
COPY --chmod=0755 bin/guangyue-linux-amd64 /opt/guangyue-personal/bin/guangyue
COPY --chmod=0755 bin/guangyue-gateway-linux-amd64 /opt/guangyue-personal/bin/guangyue-gateway
COPY --chmod=0755 bin/hysteria-node-linux-amd64 /opt/guangyue-personal/bin/hysteria
COPY --chmod=0755 bin/xray-linux-amd64 /opt/guangyue-personal/bin/xray
COPY --chmod=0755 bin/mihomo-linux-amd64 /opt/guangyue-personal/bin/mihomo
COPY web /opt/guangyue-personal/web
COPY licenses /opt/guangyue-personal/licenses
COPY VERSION /opt/guangyue-personal/VERSION
COPY --chmod=0755 entrypoint.sh /usr/local/bin/guangyue-entrypoint
ENV GUANGYUE_CONTAINER=1
USER 65532:65532
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/guangyue-entrypoint"]
