# Hosting piCVert

One static binary, one data directory, one reverse proxy. There is no database,
no build step on the host, and nothing beside the binary to deploy: the
templates, the fonts and the whole interface are compiled into it.

## Install

On the machine that will run it — a VM, a Pi, an LXC container:

```sh
curl -fsSL https://raw.githubusercontent.com/nebuloss/piCVert/main/deploy/install.sh | sh
```

It works out the architecture, downloads the released binary, **checks it
against the published digest**, and installs it as a systemd service. Nothing is
compiled and no toolchain is needed: the templates, the fonts and the whole
interface are inside the one file it fetches.

Then:

```sh
editor /etc/picvert.env          # at least PICVERT_PUBLIC_URL
picvert new --slug jean --name "Jean Dupont"
```

Run the same command again to upgrade. It never touches `/etc/picvert.env` or
the data directory once they exist, so an upgrade cannot take your configuration
or your CVs with it.

### In an LXC container

The same command. One thing is worth knowing: the service unit asks the kernel
for a private `/dev` and a read-only `/proc/sys`, and an **unprivileged**
container will not get them — systemd then fails the service with
`226/NAMESPACE`, which names nothing you can act on and looks exactly like a
broken binary.

The installer tries the hardened unit first and, only if it fails that way,
drops in `/etc/systemd/system/picvert.service.d/container.conf`, which turns off
what a container cannot grant and keeps everything else. It says so when it does.

A container that *can* take the full hardening keeps it, which is why this is
tried rather than guessed at.

### The paths are not arbitrary

`/opt/picvert` for the binary and `/var/lib/picvert` for the data. The unit sets
`PrivateTmp` and `ProtectHome`, so a data directory under `/tmp`, `/var/tmp` or
`/home` **cannot work** — the service gets its own empty `/tmp` and no `/home` at
all. Both failures name a missing file rather than a sandbox, which is a bad
half-hour if you have moved the paths.

### Building it instead

```sh
git clone https://github.com/nebuloss/piCVert && cd piCVert
go build -o /opt/picvert/picvert ./cmd/picvert
```

That is the whole build — the interface is compiled by esbuild, which is written
in Go. Then take the unit and the environment example from `deploy/`.

Then put a proxy in front of the **public port only**
(`deploy/nginx-picvert.conf`), and make the first CV:

```bash
ssh -L 3001:127.0.0.1:3001 your-host       # then open http://127.0.0.1:3001
# or, without forwarding anything:
sudo -u picvert /opt/picvert/picvert new --slug jean --name "Jean Dupont"
```

Upgrading is the same script again. It rebuilds, replaces the binary and
restarts, and leaves `/etc/picvert.env` and the data alone.

## The two ports, and why

```
        internet ──► nginx ──► :3000   the CVs, the private links, the editor
                               :3001   administration — NOT PROXIED
```

**The admin port has no access control at all.** That is deliberate and it is
not an oversight to be corrected: the thing protecting it is that nothing
outside can reach it. Reach it over an SSH tunnel.

Do not put a password on it either. A password-protected surface on the public
side would become the only thing here actually worth attacking, and it would be
attacked continuously by people who have never heard of this service. With no
such surface there is nothing to guess.

## What is secret, and what is not

Access rests entirely on **two links per CV** — one that views, one that also
edits. No account, no password, nothing to reset.

- Tokens are 192 bits. Guessing one is out of reach; hammering the service
  is not, so a per-address failure counter refuses an address that has tried
  ten bad links, and says so once in the log rather than once per attempt.
- They are stored **in clear**, in `data/.share-tokens.json`, mode 0600. That is
  on purpose: a link you cannot be shown again is not a stable link, and
  reissuing is what breaks every copy already handed out.
- `Referrer-Policy: no-referrer` on every response, so a token in an address bar
  does not leave with an outbound click.
- `robots.txt` disallows `/e/`, and private pages carry `X-Robots-Tag: noindex`
  as well, because robots.txt is a request some crawlers ignore. A CV in a
  search index is a CV whose link was the only thing keeping it private.

**Nothing is readable at a guessable address unless it is named in
`PICVERT_PUBLIC`.** An unpublished CV answers exactly as a missing one does, so
the address bar cannot be used to find out whose CVs are here.

## Two people editing one CV

**One person edits at a time.** The second is told when they open it, before
they have typed anything — rather than discovering it when their afternoon's
work is refused.

They are offered two things and given a third if they ignore both:

> **“Jean Dupont” is being edited** in another window.
> [Wait for it] [Just view it] — *taking you to view it in 10 seconds…*

Any click or keypress stops the countdown, so nobody is navigated away mid
thought. Waiting polls until it is free and then opens the editor. Most people
opening a CV wanted to look at it, which is why that is where an undecided
person ends up.

### What identifies an editor

Two halves, because neither does it alone:

- a **cookie**, set by the server and unreadable by script, says which *browser*
- a **nonce** in `sessionStorage` says which *tab*

The cookie survives a reload, which is the whole reason it exists: the identity
used to be made fresh on every page load, so pressing F5 made a stranger of you
and you were told somebody else was editing your own CV — for the length of the
timeout. The nonce survives a reload of its own tab but not a new one, so a
browser with the same CV open twice is two editors rather than one confused one.

Only the nonce is in reach of page script, and forging one buys nothing: it
needs the cookie too, and anything that has that is already this browser. The
cookie is `HttpOnly`, `SameSite=Strict`, and `Secure` over HTTPS.

It is not a credential — it says which window you are, not that you may edit.
A write still needs the link token as well.

### It cannot be held for ever

The holder is a browser tab, and a tab can be closed, crash, lose its network
or go into a bag with the laptop. So the right to edit is a **lease** that
expires, with two timeouts because there are two different failures:

| | | |
|---|---|---|
| **45 s** | the window stopped talking | closed, crashed, disconnected |
| **15 min** | the window is there and nothing has changed | a tab left open on a second monitor |

One timeout cannot do both: short enough to free a closed tab quickly is far
too short for somebody thinking about a sentence.

The editor says "I am still here" every 15 seconds, so two of those can be lost
to a bad connection before anybody is treated as gone. Saying hello does **not**
hold off the second timeout — only actually changing the CV does, which is what
tells a person apart from a forgotten tab. A tab that closes gives the lease up
at once, so the next person rarely waits the full 45 seconds.

Leases live in memory and are lost on restart. That is correct: a restart is
the moment every holder has gone, and honouring leases belonging to nobody is
the one thing a fresh process must not do.

### And if a lease lapses mid-sentence

The save is still refused rather than applied. Every save says which version of
the document it was built from, and one built on a version that has since moved
is rejected — so the older guarantee still stands underneath: **no change is
destroyed without somebody being told.** The lease is what stops that happening
in the first place.

The command line and anyone with `curl` take no lease and are not refused one;
they are covered by the version check.

## Backups

Back up **`/var/lib/picvert/data`** and nothing else.

That directory is the whole state: the documents, the portraits, the journals
and the link file. Everything else — every page, every PDF — is drawn from it on
request, so there is no artefact that can be lost or fall out of step with it.

```bash
systemctl stop picvert          # not strictly needed: writes are atomic
tar czf picvert-$(date +%F).tar.gz -C /var/lib/picvert data
systemctl start picvert
```

Stopping is optional. Every write goes to a temporary file and is renamed into
place, so a backup taken while the service runs catches either the old document
or the new one, never half of one.

## Deleting is reversible for a day

A CV is deleted by whoever holds its edit link — for a self-service CV, its
author and nobody else. It is one click, and what it destroys exists nowhere
else.

So it is not destroyed. The directory is moved to `data/.trash/` and erased
after `PICVERT_TRASH_HOURS` (24 by default). Until then the admin page puts it
back exactly as it was. The sweep happens when the admin page is opened, rather
than on a timer, because a timer that stops is a timer nobody notices.

## How locked down it is

```
systemd-analyze security picvert.service   →  1.8 OK
```

No capabilities at all (`CapabilityBoundingSet=`), a system call allow-list,
no writable-executable memory, a read-only filesystem apart from one directory,
and `UMask=0077` so anything it writes is private even if the code that writes
it forgets.

Verified running under exactly that: zero effective capabilities, seccomp in
filter mode, and a CV rendered to PDF through it.

## Resources

The engine holds one page's layout and the four fonts it measured with. The
unit caps it at 256 MB, which is far above what it uses and low enough that a
pathological document is a failed request rather than a dead host.

There is no browser involved. The PDF is written directly, so the memory profile
is a few megabytes of parsed fonts plus the page being drawn.

## Checking on it

```bash
curl -s localhost:3000/healthz                 # {"ok":true,"profiles":N}
journalctl -u picvert -f
sudo -u picvert /opt/picvert/picvert fit --profile /var/lib/picvert/data/jean
```

`fit` is worth knowing: it says whether a CV holds on one page, how much room
is left in each column, and — when it does not hold — which block to shorten.
It reports exactly what the editor shows, because both come from the same
layout.

## When something is wrong

**The editor is blank.** Look at the browser console. Every asset is served from
`/assets/`, from inside the binary, so a 404 there means the binary is older
than the page asking for it — restart after an upgrade.

**Every link answers 403.** The token file is mode 0600 and owned by whoever
created it. If it was written by root while the service runs as `picvert`, the
service cannot read it. The service realigns ownership when it can; if it
cannot, `chown picvert: /var/lib/picvert/data/.share-tokens.json`.

**A CV comes out clipped.** It should not: the fit check refuses to report a
page as fitting when it does not, and the engine sets the spacing to make it
fit. If it happens, `picvert fit` names the block, and the page and the PDF will
agree about it — they are drawn from one layout.

**The failure throttle is blocking a legitimate address.** It forgets an address
after `PICVERT_RATE_WINDOW_MIN`, and a successful link clears it at once.
Restarting the service clears all of them.
