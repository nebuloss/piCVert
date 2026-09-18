#!/usr/bin/env bash
# Builds piCVert and puts it straight onto the appliance, without GitHub.
#
# # WHY THIS EXISTS
#
# The release workflow takes about five minutes: tag, push, wait for six
# targets to build, wait for the assets to publish, then have the container
# download one of them. That is the right path for a RELEASE, and a slow and
# silly one for "does this fix work on the box".
#
# This does the same thing in about twenty seconds, and deliberately does NOT
# touch the configuration, the data or the service files — it replaces one
# binary and restarts. Anything else is the installer's job.
#
# # WHY THE BINARY GOES THE LONG WAY ROUND
#
# The build host and the appliance cannot reach each other: the build host is
# on 10.0.50.0/24 and the container is behind Proxmox on another network. Only
# this workstation can talk to both. So the binary is built there, pulled here,
# and pushed on from here. Three hops for one file, because there is no route
# for two.
set -eu

BUILD_HOST=${BUILD_HOST:-guillaume@10.0.50.21}
PVE=${PVE:-root@10.0.0.2}
CT=${CT:-405}
REMOTE=${REMOTE:-'~/picvert'}

say() { printf '\033[1m›\033[0m %s\n' "$*"; }

# Stamped the way the release stamps it, plus the commit, so `picvert version`
# on the appliance can never be mistaken for a released build.
version="dev-$(git rev-parse --short HEAD)$(git diff --quiet || echo '-dirty')"

say "syncing to $BUILD_HOST"
rsync -az --delete \
  --exclude /.git --exclude node_modules --exclude picvert.yaml \
  ./ "$BUILD_HOST:$REMOTE/"

say "building $version"
# shellcheck disable=SC2029
ssh "$BUILD_HOST" "cd $REMOTE && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -ldflags='-s -w -X main.version=$version' \
  -o /tmp/picvert-dev ./cmd/picvert"

say "fetching the binary"
rsync -az "$BUILD_HOST:/tmp/picvert-dev" /tmp/picvert-dev

say "pushing to container $CT"
# Via the Proxmox host's own filesystem: pct push reads from there, not here.
ssh "$PVE" 'cat > /tmp/picvert-dev' < /tmp/picvert-dev
ssh "$PVE" "pct push $CT /tmp/picvert-dev /opt/picvert/picvert.new --perms 755"

# Renamed rather than overwritten: replacing a running binary in place is what
# produces "Text file busy", and on this path that is every single time.
ssh "$PVE" "pct exec $CT -- sh -c '
  mv -f /opt/picvert/picvert.new /opt/picvert/picvert
  rc-service picvert restart >/dev/null 2>&1 || systemctl restart picvert
'"
ssh "$PVE" "rm -f /tmp/picvert-dev"

say "checking"
sleep 2
ssh "$PVE" "pct exec $CT -- sh -c '
  /opt/picvert/picvert version
  printf \"health : \"
  wget -qO- -T5 http://127.0.0.1:3000/healthz || echo FAILED
  echo
'"
