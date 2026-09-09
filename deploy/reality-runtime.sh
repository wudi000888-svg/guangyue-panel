#!/bin/bash
# Sourced only by the root deployment scripts; no helper is installed or invoked
# by the unprivileged panel. This deployment targets the inspected www-data VPS.

reality_runtime_preflight() {
  local nginx_identity
  nginx_identity=$(nginx -T 2>/dev/null | awk '$1 == "user" { gsub(/;/, ""); print $2 ":" ($3 ? $3 : $2) }')
  if test "$nginx_identity" != www-data:www-data; then
    printf 'Expected Nginx worker www-data:www-data; refusing to broaden runtime socket permissions.\n' >&2
    return 1
  fi
  test -f "$stage/deploy/guangyue-reality.tmpfiles.conf"
  test ! -L /run/guangyue-reality
  if test -e /run/guangyue-reality; then test -d /run/guangyue-reality; fi
  test ! -L /etc/tmpfiles.d/guangyue-reality.conf
}

reality_runtime_backup() {
  local target=$1
  if test -f /etc/tmpfiles.d/guangyue-reality.conf; then
    cp -a /etc/tmpfiles.d/guangyue-reality.conf "$target/reality-tmpfiles.conf"
  fi
  if test -d /run/guangyue-reality; then
    stat -c '%a %u %g' /run/guangyue-reality > "$target/reality-runtime-mode"
  fi
}

reality_runtime_prepare() {
  install -m 0644 "$stage/deploy/guangyue-reality.tmpfiles.conf" /etc/tmpfiles.d/guangyue-reality.conf
  systemd-tmpfiles --create /etc/tmpfiles.d/guangyue-reality.conf
  test "$(stat -c '%a %U %G' /run/guangyue-reality)" = '2750 guangyue www-data'
  # Sharing this directory must never widen the database/secret directory.
  test "$(stat -c '%a %U %G' /var/lib/guangyue)" = '700 guangyue guangyue'
}

reality_runtime_restore() {
  # Call only after stopping Xray. Restore previous config and directory metadata;
  # remove only known runtime socket/lock artifacts when this update created it.
  local target=$1 reality_mode reality_uid reality_gid
  if test -f "$target/reality-tmpfiles.conf"; then
    cp -a "$target/reality-tmpfiles.conf" /etc/tmpfiles.d/guangyue-reality.conf
  else
    rm -f /etc/tmpfiles.d/guangyue-reality.conf
  fi
  if test -f "$target/reality-runtime-mode"; then
    read -r reality_mode reality_uid reality_gid < "$target/reality-runtime-mode"
    install -d -m "$reality_mode" -o "$reality_uid" -g "$reality_gid" /run/guangyue-reality
  elif test -d /run/guangyue-reality && ! test -L /run/guangyue-reality; then
    rm -f /run/guangyue-reality/*.sock /run/guangyue-reality/*.sock.lock
    # Preserve any unexpected entries instead of deleting unknown data.
    rmdir /run/guangyue-reality 2>/dev/null || true
  fi
}
