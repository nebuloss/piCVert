/**
 * admin.ts — the inventory, and the things only this port can do.
 *
 * It talks to its OWN origin and nothing else. The admin port and the public
 * port are two services that happen to share a data directory, and a page here
 * reaching into the public one would be the first step towards this interface
 * needing to be reachable from outside — which is precisely what its design
 * avoids. The links it displays point at the public service because a person
 * has to paste them somewhere; nothing here fetches them.
 */

import { HttpClient } from '../lib/http.ts';
import { confirmDialog } from '../lib/dialog.ts';
import { toast } from '../lib/toast.ts';
import { copy, el, need, replace } from '../lib/dom.ts';
import type { Child } from '../lib/dom.ts';
import type {
  AccessAnswer, AdminTemplatesAnswer, CreateAnswer, InventoryAnswer,
  MetricsAnswer, ProfileSummary, TrashAnswer, TrashEntry, Visitor,
} from '../model/api.ts';

/** Bytes and dates, rendered the same way everywhere they appear. */
const show = {
  bytes(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${Math.round(n / 1024)} kB`;
    return `${(n / 1024 / 1024).toFixed(1)} MB`;
  },
  when(stamp?: string): string {
    if (!stamp) return '';
    const date = new Date(stamp);
    return Number.isNaN(date.valueOf()) ? stamp : date.toLocaleString();
  },
  /**
   * How long ago, in the few characters a scanned list can spare.
   *
   * A full timestamp is twenty characters that are the same for every CV
   * touched this week. What a list is read for is which one moved LAST, and
   * "2 h" answers that where "22/09/2026, 14:03" has to be decoded first. The
   * exact moment is on the line's tooltip and in the details.
   */
  ago(stamp?: string): string {
    if (!stamp) return '';
    const ms = Date.now() - new Date(stamp).valueOf();
    if (Number.isNaN(ms)) return '';
    if (ms < 0 || ms < 60_000) return 'just now';
    const minutes = Math.floor(ms / 60_000);
    if (minutes < 60) return `${minutes} min`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours} h`;
    const days = Math.floor(hours / 24);
    if (days < 30) return `${days} d`;
    return new Date(stamp).toLocaleDateString(undefined, { month: 'short', year: '2-digit' });
  },
  /**
   * How long is left, said the way a person would.
   *
   * A deleted CV is a thing with a deadline, and an absolute timestamp makes
   * the reader do the subtraction — at the one moment they are already in a
   * hurry, because something has gone missing.
   */
  left(stamp?: string): string {
    if (!stamp) return '';
    const ms = new Date(stamp).valueOf() - Date.now();
    if (Number.isNaN(ms)) return '';
    if (ms <= 0) return 'erased at the next sweep';
    const hours = Math.floor(ms / 3_600_000);
    if (hours < 1) return `${Math.max(1, Math.round(ms / 60_000))} min left`;
    if (hours < 48) return `${hours} h left`;
    return `${Math.floor(hours / 24)} days left`;
  },
};

/**
 * MetricsPanel is what the service has been doing.
 *
 * Every number here answers a question an administrator actually asks — is it
 * busy, is the cache doing anything, is somebody attacking it, when was the
 * last backup. A panel of numbers nobody would act on is worse than none,
 * because it looks like observability while saying nothing.
 */
class MetricsPanel {
  constructor(
    private readonly node: HTMLElement,
    private readonly alerts: HTMLElement,
    private readonly badge: HTMLElement,
    private readonly api: HttpClient,
  ) {}

  async refresh(): Promise<void> {
    let answer: MetricsAnswer;
    try {
      answer = await this.api.get<MetricsAnswer>('/api/metrics');
    } catch (error) {
      replace(this.node, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
      return;
    }
    const m = answer.metrics;

    // WARNINGS GO OUTSIDE THE TABS, and that is the whole reason this method
    // writes to two places.
    //
    // Each is a thing somebody would act on today. Filed inside the Service
    // tab they are invisible until somebody goes and looks, and nobody goes
    // looking for a warning they have not been shown — which would make the
    // tabs a way of hiding exactly the part of this panel that is urgent.
    const warnings: Child[] = [];
    if (!answer.domain) {
      warnings.push(this.warn(
        'No domain configured — the links below are paths, not addresses.'));
    }
    if (!answer.guarded) {
      warnings.push(this.warn(
        'This port has no password. It is protected only by not being reachable.'));
    }
    if (!m.lastBackup) {
      warnings.push(this.warn('No backup has been taken since this service started.'));
    }
    if (answer.bytes > answer.limits.profileMB * answer.profiles * 1024 * 1024 * 0.8) {
      warnings.push(this.warn('The CVs are near their combined ceiling.'));
    }
    replace(this.alerts, warnings);

    // The tab carries the count, so the number of things wrong is legible
    // from the other two views.
    this.badge.textContent = warnings.length ? String(warnings.length) : 'ok';
    this.badge.classList.toggle('pending', warnings.length > 0);

    replace(this.node, [
      el('div', { class: 'metrics' }, [
        this.stat('CVs', String(answer.profiles), show.bytes(answer.bytes)),
        this.stat('being edited', String(answer.editing), 'right now'),
        this.stat('pages drawn', String(m.renders),
          `${m.renderMedianMs.toFixed(0)} ms typical, ${m.renderSlowMs.toFixed(0)} ms slow`),
        this.stat('PDFs', String(m.pdfs), `${m.saves} saves`),
        this.stat('cache', `${Math.round(m.cacheRatio * 100)}%`,
          `${answer.cacheCount} pages, ${show.bytes(answer.cacheBytes)}`),
        this.stat('refused', String(m.refused),
          `${m.conflicts} conflicts, ${m.errors} errors`),
        this.stat('uptime', duration(m.uptimeSec), `version ${m.version}`),
        this.stat('last backup', m.lastBackup ? show.when(m.lastBackup) : '—',
          m.lastBackup ? '' : 'none this session'),
      ]),
    ]);
  }

  private warn(text: string): HTMLElement {
    return el('div', { class: 'warning', text });
  }

  private stat(label: string, value: string, note: string): HTMLElement {
    return el('div', { class: 'stat' }, [
      el('div', { class: 'stat-value', text: value }),
      el('div', { class: 'stat-label', text: label }),
      note ? el('div', { class: 'stat-note', text: note }) : null,
    ]);
  }
}

/** duration reads a number of seconds as something a person would say. */
function duration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

/**
 * table builds a headed table, since both halves of this page are one.
 *
 * Wrapped, because the rounded corners belong to the wrapper: `overflow:
 * hidden` on a <table> is honoured by some browsers and quietly dropped by
 * others, and the corners were square on exactly one machine.
 */
/**
 * publicHref turns a private link into an address that works FROM THIS PAGE.
 *
 * The two ports are two servers. A link is stored as a bare path whenever no
 * domain is configured — which is the default — and a bare path followed from
 * the administration page resolves against the administration port, where
 * /e/<token>/ does not exist. Measured: 404 there, 200 on the public port.
 *
 * `data-public-url` is the domain when the service knows it. Without one, the
 * same host on the public port is the best guess available, and it is right
 * for every arrangement that has not been told otherwise.
 */
function publicHref(path: string): string {
  if (/^https?:\/\//i.test(path)) return path;
  const configured = document.body.dataset.publicUrl;
  if (configured) return configured.replace(/\/$/, '') + path;
  // The port the server says it serves CVs on, not a guess. Same host,
  // because the two ports are two listeners in one process.
  const port = document.body.dataset.publicPort || '3000';
  return `${location.protocol}//${location.hostname}:${port}${path}`;
}

/**
 * CopyField is a link with a button that copies it.
 *
 * A token is 32 characters of base64 and the link around it is longer.
 * Selecting that by hand, from a table, without catching the whitespace either
 * side, is a thing people get wrong — and a link that is wrong by one character
 * is a link whose failure looks exactly like a revoked one.
 */
class CopyField {
  /** The address as it works from here, which is also the one worth copying. */
  private readonly href: string;

  constructor(
    private readonly label: string,
    private readonly value: string,
    private readonly onRotate?: () => void,
  ) {
    this.href = publicHref(value);
  }

  /**
   * The token, not the address.
   *
   * The full link does not fit a table cell and was clipped to `https://…`,
   * which is the same eight characters on every row of every CV — a column
   * carrying no information at all. The token is the part that differs, and
   * seeing it change is how one confirms a renewal actually happened.
   */
  private fingerprint(): string {
    const token = this.value.match(/\/e\/([^/]+)/)?.[1];
    return token ? `${token.slice(0, 10)}…` : this.value;
  }

  render(): HTMLElement {
    const copyButton = el('button', {
      type: 'button', class: 'link-copy', title: `Copy the ${this.label} link`,
      text: 'copy',
      onclick: () => {
        void copy(this.href).then((done) => {
          copyButton.textContent = done ? 'copied' : 'select it';
          window.setTimeout(() => { copyButton.textContent = 'copy'; }, 1500);
        });
      },
    });

    return el('div', { class: 'linkrow' }, [
      el('span', { class: 'link-label', text: this.label }),
      el('code', { text: this.fingerprint(), title: this.href }),
      copyButton,
      // BOTH, always. A link is either handed to somebody else or followed
      // oneself, and the page offered only the first — so seeing what a CV
      // actually looks like meant copying an address and pasting it into
      // another tab. On a phone that is several deliberate actions to do the
      // most obvious thing on the page.
      el('a', {
        class: 'link-open', href: this.href, target: '_blank', rel: 'noopener',
        title: `Open the ${this.label} link`, text: 'open',
      }),
      // Renewing belongs BESIDE the link it renews. Offered once per row it
      // could only ever mean one of the two — and it meant the edit one,
      // silently, so a read link given to the wrong person could not be
      // revoked from this page at all.
      this.onRotate
        ? el('button', {
            type: 'button', class: 'link-renew', text: 'renew',
            title: `Renew the ${this.label} link`,
            onclick: this.onRotate,
          })
        : null,
    ]);
  }
}

/** CreateForm makes a CV. It is the only way one comes into existence here. */
class CreateForm {
  constructor(
    private readonly api: HttpClient,
    private readonly onCreated: (name: string) => void,
  ) {}

  render(): HTMLElement {
    const slug = el('input', {
      type: 'text', placeholder: 'jean-dupont',
      pattern: '[a-z0-9][a-z0-9_-]*', required: true,
    });
    const name = el('input', { type: 'text', placeholder: 'Jean Dupont' });
    const lang = el('input', { type: 'text', placeholder: 'en', maxlength: 2, size: 4 });
    const template = el('select', {});
    const note = el('div', { class: 'help' });
    const submit = el('button', { type: 'submit', class: 'primary', text: 'Create' });

    this.api.get<AdminTemplatesAnswer>('/api/templates')
      .then((answer) => {
        for (const t of answer.templates) {
          template.appendChild(el('option', { value: t.uuid, text: t.title || t.name }));
        }
      })
      .catch((error: unknown) => {
        note.textContent = error instanceof Error ? error.message : String(error);
      });

    const form = el('form', {
      class: 'create',
      onsubmit: (event: Event) => {
        event.preventDefault();
        submit.disabled = true;
        note.textContent = '';
        note.className = 'help';
        this.api.post<CreateAnswer>('/api/profiles', {
          slug: slug.value.trim(),
          name: name.value.trim(),
          template: template.value,
          lang: lang.value.trim(),
        })
          .then((answer) => {
            // The links are shown at once and not merely listed in the table.
            // The edit link is the ONLY way into the CV just made, and somebody
            // who closes this page without it has created something nobody can
            // open.
            replace(note, [
              el('strong', { text: 'Created. Hand this link to its author:' }),
              new CopyField('edit', answer.profile.links.edit).render(),
            ]);
            note.className = 'help created';
            // Read BEFORE the fields are cleared: taking it afterwards is
            // reading an empty input and reporting that nothing was named.
            const created = answer.profile.name || answer.profile.slug;
            slug.value = '';
            name.value = '';
            delete slug.dataset.touched;
            this.onCreated(created);
          })
          .catch((error: unknown) => {
            note.textContent = error instanceof Error ? error.message : String(error);
            note.className = 'help error';
          })
          .finally(() => { submit.disabled = false; });
      },
    }, [
      el('div', { class: 'fields' }, [
        el('label', {}, ['identifier', slug]),
        el('label', {}, ['name', name]),
        el('label', {}, ['language', lang]),
        el('label', {}, ['template', template]),
      ]),
      submit,
      note,
    ]);

    // The identifier is what appears in the address, so it is offered rather
    // than demanded: typing a name and getting a sensible slug is the common
    // case, and correcting it by hand is still there for the rest.
    name.addEventListener('input', () => {
      if (slug.dataset.touched) return;
      slug.value = name.value.toLowerCase().normalize('NFD')
        .replace(/[\u0300-\u036f]/g, '')
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '');
    });
    slug.addEventListener('input', () => { slug.dataset.touched = '1'; });

    return form;
  }
}

/**
 * RequestsView is who has fetched the CVs, grouped by address.
 *
 * # WHY GROUPED AND NOT A LIST OF REQUESTS
 *
 * A stream of forty lines is forty things to add up by eye. The question being
 * asked is "has anybody opened this, and is it one person or six" — which is
 * the grouping itself, not the entries. Each row is an address, what it
 * fetched, which CVs, and when it was last here.
 *
 * # WHY A WHOLE-SERVICE VIEW AND NOT A PANEL PER CV
 *
 * This began inside each CV's details, which answered "who read THIS" and
 * nothing else. The answer that actually matters is the one no single panel
 * can show: an address that appears against one CV is a reader, and an address
 * appearing against nine is either the owner or somebody walking the tokens.
 * Seeing that from per-CV panels means opening thirty of them and remembering
 * what was in each.
 *
 * The per-CV question is still asked — by filtering this to one CV, which is
 * what the button in a CV's details does.
 *
 * # WHY IT SAYS WHAT IT DOES NOT KNOW
 *
 * The log is held in memory and bounded, so an empty list can mean "nobody
 * came" or "this service restarted an hour ago". Those are opposite answers
 * and a view that cannot tell them apart must say so, or the first person to
 * read the second as the first will conclude their CV was never opened.
 */
class RequestsView {
  private answer?: AccessAnswer;
  private filter = '';

  constructor(
    private readonly node: HTMLElement,
    private readonly search: HTMLInputElement,
    private readonly count: HTMLElement,
    private readonly badge: HTMLElement,
    private readonly api: HttpClient,
  ) {
    let typing = 0;
    this.search.addEventListener('input', () => {
      clearTimeout(typing);
      typing = window.setTimeout(() => {
        this.filter = this.search.value.trim().toLowerCase();
        this.draw();
      }, 150);
    });
  }

  /** Narrow to one CV, and say so in the box so it can be cleared. */
  only(slug: string): void {
    this.filter = slug.toLowerCase();
    this.search.value = slug;
    this.draw();
  }

  async refresh(): Promise<void> {
    try {
      this.answer = await this.api.get<AccessAnswer>('/api/access');
    } catch (error) {
      replace(this.node, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
      return;
    }
    // The tab carries the number of addresses, which is the figure somebody
    // would look at the tab for: how many people, not how many requests.
    this.badge.textContent = String(this.answer.access.visitors.length);
    this.draw();
  }

  private draw(): void {
    const answer = this.answer;
    if (!answer) return;
    const { visitors, total, kept } = answer.access;

    const shown = this.filter
      ? visitors.filter((v) => this.matches(v))
      : visitors;

    // Said whether the list is empty or not: the reader cannot otherwise know
    // whether "nothing" means nothing happened or nothing is remembered.
    const scope = el('div', { class: 'access-scope', text:
      `Kept in memory only, for ${Math.round(answer.retainHours / 24)} days or `
      + `${answer.perCV} fetches per CV, and cleared by a restart. `
      + `Recording since ${show.when(answer.since)}.` });

    this.count.textContent = visitors.length
      ? `${shown.length} of ${visitors.length} address(es) — ${total} fetches`
      : '';

    if (!shown.length) {
      replace(this.node, [
        el('div', { class: 'empty', text: this.filter
          ? 'No address matches that.'
          : 'No fetches recorded.' }),
        scope,
      ]);
      return;
    }

    const dropped = total > kept
      ? el('div', { class: 'access-scope', text:
          `${total} fetches in total; the most recent ${kept} are grouped above.` })
      : null;

    replace(this.node, [
      el('div', { class: 'access-rows' }, shown.map((v) => this.row(v))),
      dropped,
      scope,
    ]);
  }

  /** Address, CV or browser — the three things somebody arrives knowing. */
  private matches(v: Visitor): boolean {
    const hay = [v.ip, v.agent ?? '', ...(v.cvs ?? [])].join(' ').toLowerCase();
    return hay.includes(this.filter);
  }

  private row(v: Visitor): HTMLElement {
    // What it fetched, in the words that distinguish the three. A PDF is
    // somebody KEEPING the CV, which is a different act from looking at it.
    const what = [
      v.viewer ? `${v.viewer} open` : null,
      v.pages ? `${v.pages} page` : null,
      v.pdfs ? `${v.pdfs} pdf` : null,
    ].filter(Boolean).join(', ');

    const cvs = v.cvs ?? [];
    return el('div', { class: `access-row${v.author ? ' author' : ''}` }, [
      el('code', { class: 'access-ip', text: v.ip }),
      // The author's own address, marked. Without it an afternoon of editing
      // reads as an audience.
      v.author ? el('span', { class: 'tag', text: 'author' }) : null,
      el('span', { class: 'access-cvs', text: cvs.slice(0, 2).join(', '),
        title: cvs.join(', ') }),
      cvs.length > 2
        ? el('span', { class: 'access-more', text: `+${cvs.length - 2}`,
            title: cvs.join(', ') })
        : null,
      el('span', { class: 'access-what', text: what }),
      v.agent ? el('span', { class: 'access-agent', text: v.agent }) : null,
      el('span', { class: 'gap' }),
      el('span', {
        class: 'access-last', text: show.ago(v.last),
        title: `first ${show.when(v.first)}, last ${show.when(v.last)}`,
      }),
    ]);
  }
}

interface ProfileActions {
  /**
   * Renew one link. The MODE matters: the two links are independent, and the
   * server has always been able to renew either — only the edit one was ever
   * offered, so a read link handed to the wrong person could not be revoked
   * from here at all.
   */
  rotate: (profile: ProfileSummary, mode: 'edit' | 'read') => void;
  remove: (profile: ProfileSummary) => void;
  /** Show who has fetched this CV, in the view that holds every CV's. */
  requests: (profile: ProfileSummary) => void;
}

/**
 * ProfileList is the inventory of CVs: ONE LINE EACH.
 *
 * # WHY A LINE AND NOT A ROW OF COLUMNS
 *
 * This was a table of seven columns, and a CV occupied about a hundred and ten
 * pixels of it — two link rows of five controls each, a stack of tags, a size,
 * a date, four buttons. Ten CVs did not fit on a screen. But the reason to
 * open this page is almost never a particular CV: it is to see WHAT IS THERE,
 * and the columns that made a row tall were the ones nobody reads while doing
 * that. A token fingerprint is not scanned down a column; it is looked at once,
 * when a link is being renewed.
 *
 * So the line carries what identifies a CV and what one does to it constantly,
 * and everything else moves behind a disclosure on the same line. Nothing was
 * removed from the page — the size, the history, the role, both links with
 * their fingerprints, both renewals and the deletion are all still one click
 * away, and that click is on the CV itself.
 *
 * It also retires the narrow-screen rules that turned the table back into
 * cards: a line that is already a line needs no second layout.
 */
class ProfileList {
  /** The slugs whose details are open, kept ACROSS a refresh.
   *
   * Every action on this page reloads the inventory, so a panel that lived in
   * the DOM alone closed itself the moment it was used — renew a link and the
   * thing you were looking at vanishes. */
  private readonly open = new Set<string>();

  constructor(
    private readonly node: HTMLElement,
    private readonly actions: ProfileActions,
  ) {}

  show(answer: InventoryAnswer): void {
    if (!answer.profiles.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'No CVs yet.' })]);
      return;
    }
    replace(this.node, [el('div', { class: 'cvs' },
      answer.profiles.map((p) => this.entry(p)))]);
  }

  private entry(p: ProfileSummary): HTMLElement {
    const details = el('div', { class: 'cv-details', id: `d-${p.slug}`, hidden: true });
    let built = false;

    const toggle = el('button', {
      type: 'button', class: 'cv-open', 'aria-controls': details.id,
      'aria-expanded': 'false', title: 'What this CV is, and its links',
    }, [el('span', { class: 'chevron', text: '\u203a' })]);

    const line = el('div', { class: 'cv' }, [
      toggle,
      el('span', { class: 'cv-name', text: p.name || p.slug, title: p.name }),
      el('code', { class: 'cv-slug', text: p.slug }),
      // Only what is NOT the default. "by link only" is how every CV starts,
      // and a tag on every line saying so is a column of noise that hides the
      // one line where it does not apply.
      p.public ? el('span', { class: 'tag public', text: 'published' }) : null,
      p.ok ? null : el('span', {
        class: 'tag broken', text: 'unreadable', title: p.problem ?? '',
      }),
      p.templateMissing ? el('span', {
        class: 'tag broken', text: 'template missing',
        title: `This CV was written for ${p.template}, which is not installed. `
          + 'It will not render as its author last saw it.',
      }) : null,
      el('span', { class: 'gap' }),
      // How many times this CV has been fetched. On the line rather than
      // inside, because it is the number that makes somebody open the details
      // at all — a CV nobody has looked at and a CV fetched forty times are
      // the same row otherwise.
      p.visits
        ? el('span', {
            class: 'cv-visits', text: `${p.visits} \u00d7`,
            title: 'Fetches recorded since this service started',
          })
        : null,
      el('span', { class: 'cv-when', text: show.ago(p.updatedAt), title: show.when(p.updatedAt) }),
      // The two things done to a CV without thinking about it: take the link
      // its author needs, and look at the page. Everything rarer is inside.
      this.quickCopy(p),
      // THROUGH THE PUBLIC PORT, by the CV's own read link.
      //
      // These used to be /view/<slug>/… on this port, which rendered any CV
      // without a token on the one port that has no access control. Nothing
      // is lost by removing it: the read link is how a CV is published, and
      // following it is also the only way to see what its reader sees.
      el('a', {
        class: 'tag', href: publicHref(`${p.links.read}cv.html`),
        target: '_blank', rel: 'noopener', text: 'page',
      }),
      el('a', {
        // Inline, so it opens in a tab rather than landing in the downloads
        // folder. A CV is a thing to look at.
        class: 'tag', href: publicHref(`${p.links.read}cv.pdf?inline=1`),
        target: '_blank', rel: 'noopener', text: 'pdf',
      }),
    ]);

    // Built on first opening rather than with the line. Two CopyFields per CV,
    // each holding a listener and a timer, is a cost paid on every refresh for
    // panels that are almost all shut.
    const show_ = (on: boolean): void => {
      if (on && !built) {
        built = true;
        replace(details, this.detailsOf(p));
      }
      details.hidden = !on;
      toggle.setAttribute('aria-expanded', String(on));
      line.classList.toggle('open', on);
      if (on) this.open.add(p.slug); else this.open.delete(p.slug);
    };
    toggle.addEventListener('click', () => show_(details.hidden));
    if (this.open.has(p.slug)) show_(true);

    return el('div', { class: 'cv-entry' }, [line, details]);
  }

  /**
   * The edit link, copied in one action.
   *
   * It is the only way into a CV and the thing handed over when one is made,
   * so it is the single control on the line that is not a link to look at.
   */
  private quickCopy(p: ProfileSummary): HTMLElement {
    const button = el('button', {
      type: 'button', class: 'tag copy', text: 'copy edit link',
      title: 'Copy this CV\u2019s private edit link',
      onclick: () => {
        void copy(publicHref(p.links.edit)).then((done) => {
          button.textContent = done ? 'copied' : 'select it';
          window.setTimeout(() => { button.textContent = 'copy edit link'; }, 1500);
        });
      },
    });
    return button;
  }

  /** What a CV IS, and the things done to it rarely. */
  private detailsOf(p: ProfileSummary): Child[] {
    const facts = [
      p.template,
      p.sections === undefined ? null : `${p.sections} sections`,
      p.photo ? 'photo' : null,
      p.languages.map((l) => l.lang).join(', '),
      `${show.bytes(p.bytes)}, ${p.history} saved versions`,
      p.updatedAt ? `changed ${show.when(p.updatedAt)}` : null,
    ].filter(Boolean).join(' \u00b7 ');

    return [
      p.role ? el('div', { class: 'cv-role', text: p.role }) : null,
      el('div', { class: 'cv-facts', text: facts }),
      new CopyField('edit', p.links.edit, () => this.actions.rotate(p, 'edit')).render(),
      new CopyField('read', p.links.read, () => this.actions.rotate(p, 'read')).render(),
      // Not a panel of its own any more: the same rows, in the tab that holds
      // every CV's, filtered to this one. Two places drawing the same list is
      // two places to keep agreeing.
      el('div', { class: 'cv-seen' }, [
        el('button', {
          type: 'button', class: 'tag',
          text: p.visits ? `requests (${p.visits})` : 'requests',
          title: 'Who has fetched this CV',
          onclick: () => this.actions.requests(p),
        }),
      ]),
      el('div', { class: 'cv-danger' }, [
        el('button', {
          class: 'danger', text: 'Delete this CV',
          onclick: () => this.actions.remove(p),
        }),
      ]),
    ];
  }
}

interface TrashActions {
  restore: (entry: TrashEntry) => void;
  purge: (entry: TrashEntry) => void;
}

/**
 * TrashList is what has been deleted and can still be caught.
 *
 * Cards rather than rows. A deleted CV is not an inventory entry to scan past;
 * it is a thing with a deadline, and the deadline has to be readable without
 * finding the right column and doing the subtraction.
 */
class TrashList {
  constructor(
    private readonly node: HTMLElement,
    private readonly actions: TrashActions,
  ) {}

  show(entries: TrashEntry[]): void {
    if (!entries.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'Nothing deleted.' })]);
      return;
    }
    replace(this.node, entries.map((entry) => this.card(entry)));
  }

  private card(entry: TrashEntry): HTMLElement {
    // Past its expiry but still on disk: it goes at the next sweep, so it is
    // still restorable — and saying so is the difference between catching it
    // and assuming it has gone.
    const due = new Date(entry.expiresAt).valueOf() <= Date.now();
    return el('div', { class: `trashed${due ? ' due' : ''}` }, [
      el('div', { class: 'head' }, [
        el('h3', { text: entry.name }),
        el('span', { class: 'slug', text: entry.slug }),
        el('span', { class: 'left', text: show.left(entry.expiresAt) }),
      ]),
      el('p', { class: 'meta',
        text: `deleted ${show.when(entry.deletedAt)} · ${show.bytes(entry.bytes)}` }),
      el('div', { class: 'actions' }, [
        el('button', { class: 'primary', text: 'Restore',
          onclick: () => this.actions.restore(entry) }),
        el('button', { class: 'danger', text: 'Erase now',
          onclick: () => this.actions.purge(entry) }),
      ]),
    ]);
  }
}

/**
 * Views is the page's top-level tabs: CVs, requests, deleted, service.
 *
 * # WHY TABS AND NOT ONE LONG PAGE
 *
 * The four are not looked at in the same motion. The inventory is what the
 * page is FOR; the requests are read when wondering whether a CV reached
 * anybody; the deleted list is a safety net opened when something has gone
 * missing; the numbers are read when wondering how the service is doing.
 * Stacked, the three nobody wants pad the one they came for — the block of
 * figures alone pushed the first CV a third of the way down the page on every
 * single visit.
 *
 * What does NOT go in a tab is a warning. See MetricsPanel: those are shown
 * above all four, because nobody goes looking for a warning they have not
 * been shown.
 *
 * # THE TAB IS IN THE ADDRESS
 *
 * This page reloads itself after every action and is left open for hours. A
 * tab held only in a variable is a tab that resets to the inventory whenever
 * anything happens, which is exactly the moment somebody was reading the
 * deleted list. It is also what makes one view a thing that can be sent to
 * somebody, or bookmarked.
 *
 * # A VIEW IS TOLD WHEN IT IS OPENED
 *
 * The requests are fetched when their tab is first shown rather than with the
 * page. It is the one view whose data nothing else needs, and loading it up
 * front would put a whole-service query on every visit to an inventory.
 */
class Views {
  private readonly tabs: { name: string; button: HTMLButtonElement; panel: HTMLElement }[];
  private opened?: (name: string) => void;

  constructor(root: Document) {
    this.tabs = [
      { name: 'cvs', button: need<HTMLButtonElement>(root, '#tab-cvs'), panel: need(root, '#view-cvs') },
      { name: 'requests', button: need<HTMLButtonElement>(root, '#tab-requests'), panel: need(root, '#view-requests') },
      { name: 'deleted', button: need<HTMLButtonElement>(root, '#tab-deleted'), panel: need(root, '#view-deleted') },
      { name: 'service', button: need<HTMLButtonElement>(root, '#tab-service'), panel: need(root, '#view-service') },
    ];
    for (const tab of this.tabs) {
      tab.button.addEventListener('click', () => this.select(tab.name));
    }
    // The back button moves between views, which is what a browser's own
    // control is expected to do on a page whose address says which view it is.
    window.addEventListener('hashchange', () => this.apply(this.fromHash()));
  }

  /** onOpen is called with the name of whichever view is shown. */
  onOpen(fn: (name: string) => void): void {
    this.opened = fn;
    // Applied only now, so the page's own starting view — which may be any of
    // them, since the address says which — reaches the listener too. Called
    // in the constructor it fired before anything had subscribed, and a page
    // loaded straight at #requests showed an empty list until the tab was
    // clicked a second time.
    this.apply(this.fromHash());
  }

  /** select shows a view and puts it in the address. */
  select(name: string): void {
    this.apply(name);
    if (this.fromHash() !== name) {
      // replaceState rather than a new entry: switching tabs is not a
      // navigation to be pressed back through one at a time.
      history.replaceState(null, '', `#${name}`);
    }
  }

  private apply(name: string): void {
    for (const tab of this.tabs) {
      const on = tab.name === name;
      tab.button.setAttribute('aria-selected', String(on));
      tab.panel.hidden = !on;
    }
    this.opened?.(name);
  }

  /** The view named in the address, or the inventory for anything unknown. */
  private fromHash(): string {
    const name = location.hash.replace(/^#/, '');
    const known = this.tabs.find((t) => t.name === name);
    // The first tab is the fallback, and it is read from the list rather than
    // written twice: a default spelt as a literal is a default that survives
    // the tab it names being renamed.
    return known ? known.name : (this.tabs[0]?.name ?? 'cvs');
  }
}

class Admin {
  private readonly api = new HttpClient();
  private readonly profiles: ProfileList;
  private readonly trash: TrashList;
  private readonly views: Views;

  private readonly list: HTMLElement;
  private readonly trashNode: HTMLElement;
  private readonly search: HTMLInputElement;
  private readonly count: HTMLElement;
  private readonly policy: HTMLElement;
  private readonly create: HTMLElement;
  private readonly badgeCvs: HTMLElement;
  private readonly badgeTrash: HTMLElement;
  private readonly metrics: MetricsPanel;
  private readonly requests: RequestsView;

  private page = 1;

  constructor(root: Document) {
    this.list = need(root, '#list');
    this.trashNode = need(root, '#trash');
    this.search = need<HTMLInputElement>(root, '#q');
    this.count = need(root, '#count');
    this.policy = need(root, '#policy');
    this.create = need(root, '#create');
    this.badgeCvs = need(root, '#badge-cvs');
    this.badgeTrash = need(root, '#badge-deleted');

    this.views = new Views(root);
    this.metrics = new MetricsPanel(
      need(root, '#metrics'), need(root, '#alerts'),
      need(root, '#badge-service'), this.api);
    this.requests = new RequestsView(
      need(root, '#requests'), need<HTMLInputElement>(root, '#req-q'),
      need(root, '#req-count'), need(root, '#badge-requests'), this.api);

    this.profiles = new ProfileList(this.list, {
      rotate: (p, mode) => void this.act(
        () => this.api.post(`/api/p/${p.slug}/links/rotate?mode=${mode}`),
        `Renewed the ${mode} link of “${p.name}”.`,
        {
          title: `Renew the ${mode} link?`,
          body: 'The old link stops working immediately, for everyone. '
            + 'Whoever was using it will need the new one.',
          target: `${p.name} — ${mode} link`,
          confirm: 'Renew',
        }),
      remove: (p) => void this.act(
        () => this.api.delete(`/api/p/${p.slug}`),
        `“${p.name}” was deleted — it can still be restored.`,
        {
          title: 'Delete this CV?',
          body: 'It can be restored from the Deleted tab until it expires. '
            + 'Both private links stop working now.',
          target: `${p.name} — ${p.slug}`,
          confirm: 'Delete',
        }),
      // The same rows as the Requests tab, filtered to this CV — rather than
      // a second copy of the list inside every CV's details.
      requests: (p) => {
        this.views.select('requests');
        this.requests.only(p.slug);
      },
    });

    this.trash = new TrashList(this.trashNode, {
      restore: (entry) => void this.act(
        () => this.api.post(`/api/trash/${entry.slug}/restore`),
        `“${entry.name}” restored, with its original links.`),
      purge: (entry) => void this.act(
        () => this.api.delete(`/api/trash/${entry.slug}`),
        `“${entry.name}” erased.`,
        {
          title: 'Erase for good?',
          body: 'The data is destroyed and exists nowhere else. This cannot be undone.',
          target: `${entry.name} — ${entry.slug}`,
          confirm: 'Erase permanently',
        }),
    });

    let typing = 0;
    this.search.addEventListener('input', () => {
      clearTimeout(typing);
      typing = window.setTimeout(() => { this.page = 1; void this.refresh(); }, 200);
    });
    need<HTMLButtonElement>(root, '#prev').addEventListener('click', () => {
      if (this.page > 1) { this.page -= 1; void this.refresh(); }
    });
    need<HTMLButtonElement>(root, '#next').addEventListener('click', () => {
      this.page += 1;
      void this.refresh();
    });

    // The form is folded away until it is wanted: the inventory is what people
    // come to this page to see, and a form above it pushed the list off the
    // screen on every visit for the sake of the thing done least often.
    need<HTMLButtonElement>(root, '#new').addEventListener('click', () => {
      this.views.select('cvs');
      this.create.hidden = !this.create.hidden;
      if (!this.create.hidden) this.create.querySelector('input')?.focus();
    });
  }

  start(): void {
    this.create.appendChild(new CreateForm(this.api, (name) => {
      toast.show(`“${name}” created — hand over its edit link.`);
      void this.refresh();
    }).render());

    // The requests are loaded when their view is first opened, not with the
    // page: it is the one view whose data nothing else needs, and fetching it
    // up front puts a whole-service query on every visit to an inventory.
    // Re-read on each opening, because the answer is the one on this page
    // that changes without anybody here doing anything.
    this.views.onOpen((name) => {
      if (name === 'requests') void this.requests.refresh();
    });

    void this.refresh();
    void this.refreshTrash();
    void this.metrics.refresh();
    // Every thirty seconds. Often enough to watch something happen, rarely
    // enough that a page left open all day is not a load in itself.
    window.setInterval(() => void this.metrics.refresh(), 30_000);
  }

  /**
   * act performs something destructive, asks first, and says what happened.
   *
   * The question is a dialog rather than `confirm()`, which some browsers
   * suppress when the page is not focused — a deletion then appears to do
   * nothing at all. The answer is a toast rather than `alert()`, which
   * interrupts to report what was usually expected.
   */
  private async act(
    run: () => Promise<unknown>,
    done: string,
    question?: Parameters<typeof confirmDialog>[0],
  ): Promise<void> {
    if (question && !await confirmDialog(question)) return;
    try {
      await run();
      toast.show(done);
      await this.refresh();
      await this.refreshTrash();
    } catch (error) {
      toast.failure(error);
    }
  }

  private async refresh(): Promise<void> {
    const params = new URLSearchParams({ page: String(this.page) });
    const q = this.search.value.trim();
    if (q) params.set('q', q);
    try {
      const answer = await this.api.get<InventoryAnswer>(`/api/profiles?${params.toString()}`);
      this.page = answer.page;
      this.policy.textContent = `published: ${answer.policy}`;
      this.count.textContent = `${answer.total} CV(s) — page ${answer.page}/${answer.pages}`;
      this.badgeCvs.textContent = String(answer.total);
      this.profiles.show(answer);
    } catch (error) {
      replace(this.list, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
    }
  }

  private async refreshTrash(): Promise<void> {
    try {
      const answer = await this.api.get<TrashAnswer>('/api/trash');
      const entries = answer.entries ?? [];
      this.badgeTrash.textContent = String(entries.length);
      // Marked from the other view: this is the only place a CV can be caught
      // back, and the clock on it is running.
      this.badgeTrash.classList.toggle('pending', entries.length > 0);
      this.trash.show(entries);
    } catch (error) {
      this.badgeTrash.textContent = '?';
      replace(this.trashNode, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
    }
  }
}

new Admin(document).start();
