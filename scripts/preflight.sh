#!/usr/bin/env bash
# Runs CI against the COMMITTED tree, exactly as GitHub will.
#
# # WHY A FRESH CLONE AND NOT THE WORKING DIRECTORY
#
# This exists because a gofmt failure reached GitHub. The check used to run on
# the build host against a directory that had been rsynced there and then had
# `gofmt -w .` applied to it by some earlier command — so the host's copy was
# formatted, the check passed, and the copy that was actually committed was
# never touched. The two had quietly diverged, and the one being verified was
# not the one being pushed.
#
# A clone cannot diverge from what was committed: that is what it is.
#
# Run it before every push. It takes a minute.
set -u
HOST=${HOST:-guillaume@10.0.50.21}
REPO=${REPO:-/home/guillaume/misc/piCVert}
WORK=/tmp/picvert-ci

# Everything that is committed, and nothing that is not.
rm -rf "$WORK"
git clone -q "$REPO" "$WORK" || exit 1

# The remote copy is named after the commit, so two runs cannot interfere and a
# stale directory cannot be mistaken for a fresh one.
sha=$(git -C "$WORK" rev-parse --short HEAD)
remote="~/ci-$sha"
echo "› checking $sha"

rsync -az --delete "$WORK/" "$HOST:$remote/" || exit 1

# shellcheck disable=SC2029
ssh "$HOST" "cd $remote && bash -s" <<'REMOTE'
set -u
fail=0
ok()  { printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad() { printf '  \033[31mFAIL\033[0m %s\n' "$*"; fail=1; }

echo "› go"
unformatted=$(gofmt -l .)
if [ -z "$unformatted" ]; then ok gofmt; else echo "$unformatted" | sed 's/^/       /'; bad gofmt; fi
go vet ./... 2>&1 | sed 's/^/       /' && ok vet || bad vet
go test ./... 2>&1 | grep -v 'no test files' | sed 's/^/       /'
go test ./... > /dev/null 2>&1 && ok tests || bad tests

# AND on ONE core with two threads, which is what a shared CI runner behaves
# like — not merely GOMAXPROCS=2.
#
# This exists because a test passed here and failed on CI twice. The build host
# has twelve real cores and no hyperthreading, so GOMAXPROCS=2 gives it two
# genuine execution units and a runner gives it one contended. Pinning to a
# single core reproduces the runner's numbers exactly, where GOMAXPROCS alone
# did not.
if command -v taskset > /dev/null 2>&1; then
  taskset -c 0 env GOMAXPROCS=2 go test -count=1 -timeout 300s ./... > /dev/null 2>&1 \
    && ok 'tests on one contended core' || bad 'tests on one contended core'
else
  GOMAXPROCS=2 go test -count=1 ./... > /dev/null 2>&1 \
    && ok 'tests on two processors' || bad 'tests on two processors'
fi

# And under the race detector, which is where a shared buffer or an unguarded
# map is caught rather than merely producing a wrong answer.
go test -race ./internal/layout/ ./internal/lease/ > /dev/null 2>&1 \
  && ok 'race detector' || bad 'race detector'

echo "› interface"
npm ci --silent --no-audit --no-fund > /dev/null 2>&1
npx tsc -p tsconfig.json --noEmit 2>&1 | sed 's/^/       /' && ok typecheck || bad typecheck

go generate ./... > /dev/null 2>&1
if git diff --quiet -- internal/server/web/assets 2>/dev/null; then
  ok 'the bundle matches its source'
else
  git diff --stat -- internal/server/web/assets | sed 's/^/       /'
  bad 'the bundle is stale — run go generate ./...'
fi

echo "› every target"
export CGO_ENABLED=0
while read -r os arch; do
  GOOS=$os GOARCH=$arch go build -o /dev/null ./cmd/picvert 2>/dev/null \
    && ok "$os/$arch" || bad "$os/$arch"
done <<'TARGETS'
linux amd64
linux arm64
linux arm
darwin amd64
darwin arm64
windows amd64
TARGETS

echo "› installer"
sh -n deploy/install.sh && ok 'install.sh' || bad 'install.sh'

exit "$fail"
REMOTE
result=$?

ssh "$HOST" "rm -rf $remote"
printf '\n'
[ "$result" -eq 0 ] && echo "CI would pass." || echo "CI would FAIL — do not push."
exit "$result"
