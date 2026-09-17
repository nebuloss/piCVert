#!/usr/bin/env sh
# piCVert — install or upgrade the service.
#
#   curl -fsSL https://raw.githubusercontent.com/nebuloss/piCVert/main/deploy/install.sh | sh
#
# or, from a checkout:
#
#   sudo ./deploy/install.sh
#
# It downloads the released binary for this machine's architecture, checks it
# against the published digest, and installs it as a systemd service. Nothing
# is compiled and no toolchain is needed: the templates, the fonts and the whole
# interface are inside the one file it fetches.
#
# IDEMPOTENT. Run it again to upgrade. It never touches /etc/picvert.env or the
# data directory once they exist, because those are yours — so an upgrade
# cannot take your configuration or your CVs with it.
#
# POSIX sh, not bash, because a minimal container often has neither bash nor a
# reason to install one.
set -eu

REPO=${PICVERT_REPO:-nebuloss/piCVert}
VERSION=${PICVERT_VERSION:-latest}
PREFIX=${PICVERT_PREFIX:-/opt/picvert}
DATA=${PICVERT_DATA_DIR:-/var/lib/picvert}
SERVICE_USER=${PICVERT_USER:-picvert}
ENVFILE=${PICVERT_ENVFILE:-/etc/picvert.env}
UNIT=/etc/systemd/system/picvert.service

say()  { printf '\033[1m›\033[0m %s\n' "$*"; }
warn() { printf '\033[33m!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# Root is needed for what this WRITES, not as a rule. Checking the paths rather
# than the user means an install into a prefix you already own — a home
# directory, a test root — needs no privilege at all, and it is what lets this
# script be exercised before it is published.
needs_root=no
for path in "$PREFIX" "$(dirname "$ENVFILE")" "$(dirname "$DATA")"; do
  # The nearest existing ancestor is what the write actually lands in.
  probe=$path
  while [ ! -e "$probe" ] && [ "$probe" != / ] && [ "$probe" != . ]; do
    probe=$(dirname "$probe")
  done
  [ -w "$probe" ] || needs_root=yes
done
if [ "$needs_root" = yes ] && [ "$(id -u)" -ne 0 ]; then
  die "cannot write to $PREFIX, $ENVFILE or $DATA — run this as root (try: sudo sh install.sh)"
fi

# --- what machine is this? ---------------------------------------------------

# The release publishes one binary per architecture, so this has to be right.
# A wrong guess is an "Exec format error" from systemd, which says nothing about
# architectures to anyone who has not seen it before.
case "$(uname -m)" in
  x86_64 | amd64)  ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  armv7l | armv7)  ARCH=armv7 ;;
  *) die "unsupported architecture: $(uname -m)
piCVert publishes amd64, arm64 and armv7. Build from source instead:
  git clone https://github.com/$REPO && cd piCVert && go build ./cmd/picvert" ;;
esac
[ "$(uname -s)" = Linux ] || die "this installer is for Linux; on macOS just run the binary"

ASSET="picvert-linux-$ARCH"

# --- is systemd here? --------------------------------------------------------

# An LXC container may be running without an init system at all, which is a
# perfectly ordinary way to run one and a completely different way to start a
# service. Saying so beats installing a unit file nothing will ever read.
HAVE_SYSTEMD=no
if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1 &&
   [ "$(id -u)" -eq 0 ]; then
  HAVE_SYSTEMD=yes
fi

# --- fetch -------------------------------------------------------------------

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  fetch_stdout() { wget -qO- "$1"; }
else
  die "neither curl nor wget is installed"
fi

if [ "$VERSION" = latest ]; then
  say "asking GitHub for the latest release"
  VERSION=$(fetch_stdout "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$VERSION" ] || die "cannot work out the latest version; set PICVERT_VERSION=vX.Y.Z"
fi
BASE="https://github.com/$REPO/releases/download/$VERSION"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

say "downloading piCVert $VERSION for linux/$ARCH"
fetch "$BASE/$ASSET" "$TMP/picvert" || die "no $ASSET in release $VERSION"

# The digest is not optional. This script fetches an executable over the network
# and is about to run it as a service; checking it against what the build
# published is the only thing standing between a bad mirror and root.
if fetch "$BASE/SHA256SUMS" "$TMP/SHA256SUMS" 2>/dev/null; then
  want=$(sed -n "s/^\([0-9a-f]*\)[[:space:]][[:space:]]*\(dist\/\)\?$ASSET\$/\1/p" "$TMP/SHA256SUMS" | head -n 1)
  if [ -z "$want" ]; then
    warn "SHA256SUMS does not mention $ASSET — cannot verify"
  elif command -v sha256sum >/dev/null 2>&1; then
    got=$(sha256sum "$TMP/picvert" | cut -d' ' -f1)
    [ "$got" = "$want" ] || die "checksum mismatch — refusing to install
  expected $want
  got      $got"
    say "checksum verified"
  else
    warn "sha256sum is not installed — cannot verify the download"
  fi
else
  warn "no SHA256SUMS in this release — cannot verify the download"
fi

chmod 0755 "$TMP/picvert"
# Run it before installing it: a binary for the wrong architecture, or a
# truncated download, fails here rather than as a service that will not start.
"$TMP/picvert" version >/dev/null 2>&1 || die "the downloaded binary will not run on this machine"

# --- account and directories -------------------------------------------------

say "account and directories"
if [ "$(id -u)" -ne 0 ]; then
  # Not root, so there is no account to create and nothing to give away.
  SERVICE_USER=$(id -un)
elif ! id "$SERVICE_USER" >/dev/null 2>&1; then
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin "$SERVICE_USER"
  elif command -v adduser >/dev/null 2>&1; then
    # busybox / Alpine
    adduser -S -D -H -h "$DATA" -s /sbin/nologin "$SERVICE_USER"
  else
    die "cannot create the $SERVICE_USER account: no useradd or adduser"
  fi
fi

mkdir -p "$PREFIX" "$DATA/data" "$DATA/backups"
chown "$SERVICE_USER:$SERVICE_USER" "$DATA" "$DATA/data" "$DATA/backups" 2>/dev/null || true
# 0750: the CVs are private by default, and the directory holding them says so
# as well as the service does.
chmod 0750 "$DATA" "$DATA/data" "$DATA/backups"

say "installing $PREFIX/picvert"
# Into place atomically. Overwriting a running binary in place is what produces
# "Text file busy", and on the upgrade path that is every time.
cp "$TMP/picvert" "$PREFIX/picvert.new"
chmod 0755 "$PREFIX/picvert.new"
mv -f "$PREFIX/picvert.new" "$PREFIX/picvert"

# --- configuration -----------------------------------------------------------

if [ -f "$ENVFILE" ]; then
  say "$ENVFILE exists — left alone"
else
  say "writing $ENVFILE"
  fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert.env.example" "$ENVFILE" ||
    fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert.env.example" "$ENVFILE" ||
    die "cannot fetch the example configuration"
  chown "root:$SERVICE_USER" "$ENVFILE" 2>/dev/null || true
  chmod 0640 "$ENVFILE"
  NEW_CONFIG=yes
fi

# --- the service -------------------------------------------------------------

if [ "$HAVE_SYSTEMD" = yes ]; then
  say "installing the service"
  fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert.service" "$UNIT" ||
    fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert.service" "$UNIT" ||
    die "cannot fetch the service unit"
  chmod 0644 "$UNIT"

  # Nightly backups, because a backup somebody has to remember to take is a
  # backup that exists until the week it is needed.
  for unit in picvert-backup.service picvert-backup.timer; do
    fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/$unit" \
      "/etc/systemd/system/$unit" ||
      fetch "https://raw.githubusercontent.com/$REPO/main/deploy/$unit" \
        "/etc/systemd/system/$unit" || true
  done

  systemctl daemon-reload
  systemctl enable picvert >/dev/null 2>&1 || true
  systemctl enable --now picvert-backup.timer >/dev/null 2>&1 || true
  systemctl restart picvert || true

  sleep 1
  if ! systemctl is-active --quiet picvert; then
    # The unit asks the kernel for a private /dev and a read-only /proc/sys.
    # An UNPRIVILEGED container will not get them, and systemd fails the
    # service with 226/NAMESPACE — which names nothing a person can act on and
    # looks exactly like a broken binary.
    #
    # Tried strict FIRST and relaxed only on that failure, so a container that
    # can take the hardening keeps it. Guessing from /proc would get this wrong
    # in both directions: a privileged container looks like a container and
    # copes fine, and some hosts fail for reasons of their own.
    status=$(systemctl show -p ExecMainStatus --value picvert 2>/dev/null || echo '')
    result=$(systemctl show -p Result --value picvert 2>/dev/null || echo '')
    if [ "$status" = 226 ] || [ "$result" = exit-code ] || [ "$result" = resources ]; then
      say "the hardened unit will not start here — relaxing what a container cannot grant"
      mkdir -p "$UNIT.d"
      fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert-container.conf" \
        "$UNIT.d/container.conf" ||
        fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert-container.conf" \
          "$UNIT.d/container.conf" ||
        die "cannot fetch the container override"
      chmod 0644 "$UNIT.d/container.conf"
      systemctl daemon-reload
      systemctl restart picvert || true
      sleep 1
    fi
  fi

  if systemctl is-active --quiet picvert; then
    say "running"
    [ -f "$UNIT.d/container.conf" ] &&
      warn "some kernel-level hardening is off: see $UNIT.d/container.conf"
  else
    printf '\n'
    journalctl -u picvert --no-pager --lines=25 2>/dev/null | sed 's/^/  /'
    die "the service did not start — the log is above"
  fi
else
  warn "no systemd here, so nothing was set up to start it automatically."
  warn "Start it by hand with:"
  # `set -a` exports what the file assigns, which handles quoted values and
  # comments correctly — where piping the file through xargs breaks on the
  # first value containing a space.
  printf '\n  set -a; . %s; set +a; %s/picvert serve\n\n' "$ENVFILE" "$PREFIX"
fi

# --- what to do next ---------------------------------------------------------

printf '\n'
"$PREFIX/picvert" version
printf '\n'

if [ "${NEW_CONFIG:-no}" = yes ]; then
  cat <<EOF
Next, in this order:

  1. Edit $ENVFILE.
     At the very least PICVERT_PUBLIC_URL, which is how the links it hands out
     are written. Everything else has a working default.

  2. Put a reverse proxy in front of the PUBLIC port only (3000).
     An example nginx site is in the deploy/ directory of the repository.

  3. Make the first CV. There are two ways, and both are deliberate:

       $PREFIX/picvert new --slug jean --name "Jean Dupont"

     or reach the administration port over SSH — never through the proxy:

       ssh -L 3001:127.0.0.1:3001 root@$(hostname 2>/dev/null || echo this-host)

     then open http://127.0.0.1:3001 on your own machine.

THE ADMINISTRATION PORT HAS NO ACCESS CONTROL. That is by design: what protects
it is being unreachable from outside. Do not proxy it, and do not put a password
on it either — a password-protected surface would become the only thing here
worth attacking.

Backups run nightly into $DATA/backups, keeping a fortnight. Take one now with:

  $PREFIX/picvert backup

and put one back with:

  $PREFIX/picvert restore --from <file>

Copy them somewhere that is not this machine. A backup on the disk it is
protecting is a backup for exactly one kind of accident.
EOF
else
  echo "Upgraded. Your configuration and your CVs were left alone."
fi
