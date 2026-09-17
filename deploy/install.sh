#!/usr/bin/env bash
# Installs piCVert as a system service.
#
# Idempotent: run it again to upgrade. It builds the binary, puts it in place,
# and restarts — and it never touches /etc/picvert.env or the data directory
# once they exist, because those are yours.
set -euo pipefail

PREFIX=${PREFIX:-/opt/picvert}
DATA=${DATA:-/var/lib/picvert}
USER=${USER_NAME:-picvert}
ENVFILE=${ENVFILE:-/etc/picvert.env}
UNIT=/etc/systemd/system/picvert.service

here=$(cd "$(dirname "$0")/.." && pwd)

[ "$(id -u)" -eq 0 ] || { echo "run as root"; exit 1; }
command -v go >/dev/null || { echo "go is not installed"; exit 1; }

echo "› building"
# Static, so the unit's hardening cannot be defeated by a missing library and
# so the binary can be copied to a host without a toolchain.
( cd "$here" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/picvert.new ./cmd/picvert )

echo "› account and directories"
id -u "$USER" >/dev/null 2>&1 || useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin "$USER"
install -d -o root -g root -m 0755 "$PREFIX"
install -d -o "$USER" -g "$USER" -m 0750 "$DATA" "$DATA/data"

echo "› binary"
install -o root -g root -m 0755 /tmp/picvert.new "$PREFIX/picvert"
rm -f /tmp/picvert.new

echo "› configuration"
if [ -f "$ENVFILE" ]; then
  echo "  $ENVFILE exists, left alone"
else
  install -o root -g "$USER" -m 0640 "$here/deploy/picvert.env.example" "$ENVFILE"
  echo "  $ENVFILE written from the example — EDIT IT before going public"
fi

echo "› service"
install -o root -g root -m 0644 "$here/deploy/picvert.service" "$UNIT"
systemctl daemon-reload
systemctl enable picvert >/dev/null
systemctl restart picvert

sleep 1
if systemctl is-active --quiet picvert; then
  echo "› running"
  systemctl --no-pager --lines=3 status picvert | sed 's/^/  /'
else
  echo "› FAILED to start:"
  journalctl -u picvert --no-pager --lines=20 | sed 's/^/  /'
  exit 1
fi

cat <<EOF

Next:
  1. Edit $ENVFILE — at least PICVERT_PUBLIC_URL.
  2. Put a reverse proxy in front of the public port only:
       deploy/nginx-picvert.conf
  3. Reach the admin port over SSH, never through the proxy:
       ssh -L 3001:127.0.0.1:3001 $(hostname)
     then open http://127.0.0.1:3001 and create a CV.

Back up $DATA/data. Nothing else here is worth keeping: the binary carries the
templates, the fonts and the interface, and every page is drawn on request.
EOF
