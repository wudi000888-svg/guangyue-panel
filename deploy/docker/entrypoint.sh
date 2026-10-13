#!/bin/sh
# No root repair, shell command arguments, Docker socket or host service access.
set -eu
umask 077
if [ "$#" -ne 0 ]; then
  printf '%s\n' 'The panel entrypoint accepts no arbitrary command arguments.' >&2
  exit 2
fi
if [ "$(id -u)" -eq 0 ] || [ "${GUANGYUE_CONTAINER:-}" != 1 ]; then
  printf '%s\n' 'Run the panel as its unprivileged service UID with GUANGYUE_CONTAINER=1.' >&2
  exit 2
fi
if [ ! -r /etc/guangyue-personal.json ] || [ ! -w /var/lib/guangyue ]; then
  printf '%s\n' 'The fixed configuration must be readable and the private state volume writable by the service UID.' >&2
  exit 2
fi
mkdir -p /run/guangyue-runtime
# Preparation takes the same controller lock as the running panel and does not
# replace existing keys, accounts or databases. Compose gates database readiness.
/opt/guangyue-personal/bin/guangyue -prepare
exec /opt/guangyue-personal/bin/guangyue -run-container
