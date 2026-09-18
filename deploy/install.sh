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

# --- which init is this? -----------------------------------------------------

# systemd, OpenRC, or nothing at all — and all three are ordinary. An Alpine
# container is a very reasonable place to run one static binary, and it has no
# systemd; a container may also be running with no init at all, which is a
# completely different way to start a service.
#
# Detected rather than assumed, because the first version assumed systemd and
# left an Alpine container with a binary and no way to start it.
INIT=none
if [ "$(id -u)" -eq 0 ]; then
  if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
    INIT=systemd
  elif command -v rc-update >/dev/null 2>&1 && [ -d /etc/init.d ]; then
    INIT=openrc
  fi
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
    # busybox, as Alpine has. THE GROUP FIRST, and named: `adduser -S` alone
    # puts the account in `nogroup` and creates no group of its own, so a
    # service asking to run as `picvert:picvert` fails with "group not found"
    # — which says nothing about the account having been made at all.
    if command -v addgroup >/dev/null 2>&1; then
      addgroup -S "$SERVICE_USER" 2>/dev/null || true
      adduser -S -D -H -h "$DATA" -s /sbin/nologin -G "$SERVICE_USER" "$SERVICE_USER"
    else
      adduser -S -D -H -h "$DATA" -s /sbin/nologin "$SERVICE_USER"
    fi
  else
    die "cannot create the $SERVICE_USER account: no useradd or adduser"
  fi
fi

# An account that already exists but has no matching group is the state the
# first version of this left behind. Corrected rather than ignored, so that
# re-running the installer repairs an installation instead of reproducing it.
if command -v addgroup >/dev/null 2>&1 && ! getent group "$SERVICE_USER" >/dev/null 2>&1; then
  addgroup -S "$SERVICE_USER" 2>/dev/null || true
  addgroup "$SERVICE_USER" "$SERVICE_USER" 2>/dev/null || true
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

# And on the PATH, because the commands an administrator needs are on this
# binary and not in the web interface: changing the password, taking a backup,
# restoring one. Typing the whole of /opt/picvert/picvert for those is the kind
# of friction that ends with the password never being changed.
#
# A symlink rather than a copy: an upgrade replaces the file under $PREFIX and
# a copy would leave the old version sitting on the PATH, which is the worst
# outcome — two versions installed, and the one you reach by name is the stale
# one.
LINKDIR=${PICVERT_LINK_DIR:-/usr/local/bin}
if [ -d "$LINKDIR" ] && [ -w "$LINKDIR" ]; then
  ln -sf "$PREFIX/picvert" "$LINKDIR/picvert"
  say "linked $LINKDIR/picvert"
fi

# --- configuration -----------------------------------------------------------

CONFIG=${PICVERT_CONFIG_FILE:-/etc/picvert.yaml}

if [ -f "$CONFIG" ]; then
  say "$CONFIG exists — left alone"
else
  say "writing $CONFIG"
  fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert.yaml.example" "$CONFIG" ||
    fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert.yaml.example" "$CONFIG" ||
    die "cannot fetch the example configuration"

  # The administration port listens on every interface, so it needs a password
  # before anything will start. Generated here rather than demanded, so that
  # installing stays one command and the safe path is the automatic one.
  #
  # Printed ONCE. Nothing stores it in a form anybody can read back — what goes
  # in the file is a hash — so this line is the only time it exists.
  ADMIN_PASSWORD=$(head -c 18 /dev/urandom | base64 | tr -d '/+=' | cut -c1-24)
  ADMIN_HASH=$(printf '%s\n' "$ADMIN_PASSWORD" | "$PREFIX/picvert" passwd --stdin 2>/dev/null)
  if [ -n "$ADMIN_HASH" ]; then
    # Into the `password: ""` line the example already carries, NOT appended.
    # Appending produced a second `admin:` key and a file YAML refuses to
    # parse — found by rehearsing the install rather than by reading it.
    #
    # The hash contains `$` and `/`, so `|` is the delimiter and the
    # replacement goes through a shell variable rather than being interpolated
    # into the pattern.
    awk -v hash="$ADMIN_HASH" '
      !done && /^  password: ""$/ { print "  password: \"" hash "\""; done = 1; next }
      { print }
    ' "$CONFIG" > "$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
    if grep -q "$ADMIN_HASH" "$CONFIG"; then
      GENERATED_PASSWORD=$ADMIN_PASSWORD
    else
      warn "could not write the administration password into $CONFIG"
    fi
  fi

  chmod 0640 "$CONFIG"
  NEW_CONFIG=yes
fi

# The group has to exist before this can work, and a `|| true` on the chown hid
# that it did not: the file stayed root:root 0640 and the service could not read
# its own configuration, which surfaces as "permission denied" on a path that
# plainly exists.
#
# Re-applied on EVERY run rather than only when the file is written, so that an
# installation left in that state is repaired by installing again.
if [ -f "$CONFIG" ]; then
  chown "root:$SERVICE_USER" "$CONFIG" || warn "cannot give $SERVICE_USER read access to $CONFIG"
  chmod 0640 "$CONFIG"

  # The data directory, pointed at where this installation actually put it.
  #
  # The example ships a default that is right for the usual prefix, and this is
  # what makes it right when somebody moved it — and what stops `picvert new`
  # writing a CV somewhere the service never reads. That happened: the CV was
  # created, the command printed links, and nothing served them.
  if grep -q '^data-dir:' "$CONFIG"; then
    sed -i "s|^data-dir:.*|data-dir: \"$DATA/data\"|" "$CONFIG" 2>/dev/null || true
  fi
fi
if [ -f "$ENVFILE" ]; then
  chown "root:$SERVICE_USER" "$ENVFILE" 2>/dev/null || true
  chmod 0640 "$ENVFILE"
fi

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

if [ "$INIT" = openrc ]; then
  say "installing the service (OpenRC)"
  fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert.openrc" \
    /etc/init.d/picvert ||
    fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert.openrc" \
      /etc/init.d/picvert ||
    die "cannot fetch the service script"
  chmod 0755 /etc/init.d/picvert

  # Nightly backups, through the periodic directory busybox crond already runs.
  if [ -d /etc/periodic/daily ]; then
    fetch "https://raw.githubusercontent.com/$REPO/$VERSION/deploy/picvert-backup.openrc" \
      /etc/periodic/daily/picvert-backup ||
      fetch "https://raw.githubusercontent.com/$REPO/main/deploy/picvert-backup.openrc" \
        /etc/periodic/daily/picvert-backup || true
    [ -f /etc/periodic/daily/picvert-backup ] && chmod 0755 /etc/periodic/daily/picvert-backup
  fi

  rc-update add picvert default >/dev/null 2>&1 || true
  rc-service picvert restart >/dev/null 2>&1 || rc-service picvert start >/dev/null 2>&1 || true

  sleep 1
  if rc-service picvert status >/dev/null 2>&1; then
    say "running"
  else
    printf '\n'
    tail -n 20 /var/log/picvert.log 2>/dev/null | sed 's/^/  /'
    die "the service did not start — the log is above"
  fi

elif [ "$INIT" = systemd ]; then
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
  warn "no init system here, so nothing was set up to start it automatically."
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

if [ -n "${GENERATED_PASSWORD:-}" ]; then
  cat <<EOF

┌─ THE ADMINISTRATION PASSWORD ─────────────────────────────────────────
│
│   $GENERATED_PASSWORD
│
│  Written down nowhere else: $CONFIG holds only a hash of it.
│  Change it with: picvert passwd — then edit that file.
└───────────────────────────────────────────────────────────────────────
EOF
fi

if [ "${NEW_CONFIG:-no}" = yes ]; then
  cat <<EOF
Next, in this order:

  1. Edit $CONFIG.
     At the very least \`domain\`, which is how the links it hands out are
     written. Everything else has a working default.

  2. Put a reverse proxy in front of the PUBLIC port only (3000).
     An example nginx site is in the deploy/ directory of the repository.

  3. Make the first CV, from the administration page on port 3001 with the
     password above, or from here:

       picvert new --slug jean --name "Jean Dupont"

THE ADMINISTRATION PORT IS REACHABLE FROM YOUR NETWORK, and the password above
is what stands in front of it. It manages every CV and displays every private
link on the service.

Never proxy it to the internet. The PUBLIC side deliberately has no
authenticated surface at all — with nothing to guess there, there is nothing to
attack continuously — and putting this one behind a public hostname would undo
exactly that.

To keep it off the network entirely, set admin.listen to "127.0.0.1:3001" in
$CONFIG and reach it over SSH:

  ssh -L 3001:127.0.0.1:3001 $(hostname 2>/dev/null || echo this-host)

Backups run nightly into $DATA/backups, keeping a fortnight. Take one now with:

  picvert backup

and put one back with:

  picvert restore --from <file>

Copy them somewhere that is not this machine. A backup on the disk it is
protecting is a backup for exactly one kind of accident.
EOF
else
  echo "Upgraded. Your configuration and your CVs were left alone."
fi
