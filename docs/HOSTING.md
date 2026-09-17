# Hosting piCVert

One static binary, one data directory, one reverse proxy. There is no database,
no build step on the host, and nothing beside the binary to deploy: the
templates, the fonts and the whole interface are compiled into it.

## Install

```bash
git clone https://github.com/nebuloss/piCVert && cd piCVert
sudo ./deploy/install.sh
sudo editor /etc/picvert.env          # at least PICVERT_PUBLIC_URL
```

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
