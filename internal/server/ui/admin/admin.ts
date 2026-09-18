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
  AdminTemplatesAnswer, CreateAnswer, InventoryAnswer, MetricsAnswer,
  ProfileSummary, TrashAnswer, TrashEntry,
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
  constructor(private readonly node: HTMLElement, private readonly api: HttpClient) {}

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

    // Warnings first, because they are the reason to look. Each is a thing
    // somebody would do something about today.
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

    replace(this.node, [
      ...warnings,
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
/** A column: what it is called, and how much of the width it gets. */
interface Column {
  label: string;
  width: string;
}

/**
 * table builds a headed table, since both halves of this page are one.
 *
 * The widths are DECLARED, and the table is laid out fixed. Left to itself the
 * browser gives the room to whichever column holds the longest unbreakable run
 * of characters — here that is a 192-bit token inside a URL, which has no word
 * breaks in it at all. It took the width, and "Jean Dupont" was broken across
 * two lines to pay for it.
 *
 * Wrapped in a div because the rounded corners belong to the wrapper:
 * `overflow: hidden` on a <table> is honoured by some browsers and quietly
 * dropped by others, so the corners were square on exactly one machine.
 */
function table(columns: Column[], rows: HTMLElement[]): HTMLElement {
  // Each cell carries its column's name. On a narrow screen the table becomes
  // a list of cards, and a value with no header above it is a number nobody
  // can identify — "6 kB · 0 entries" means nothing on its own. The label is
  // drawn from this same array, so a renamed column cannot disagree with
  // itself in the two layouts.
  for (const row of rows) {
    row.querySelectorAll('td').forEach((cell, i) => {
      const label = columns[i]?.label;
      if (label) cell.dataset.label = label;
    });
  }
  return el('div', { class: 'tablewrap' }, [
    el('table', {}, [
      el('colgroup', {}, columns.map((c) => el('col', { style: `width:${c.width}` }))),
      el('thead', {}, [el('tr', {}, columns.map((c) => el('th', { text: c.label })))]),
      el('tbody', {}, rows),
    ]),
  ]);
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
  constructor(
    private readonly label: string,
    private readonly value: string,
    private readonly onRotate?: () => void,
  ) {}

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
        void copy(this.value).then((done) => {
          copyButton.textContent = done ? 'copied' : 'select it';
          window.setTimeout(() => { copyButton.textContent = 'copy'; }, 1500);
        });
      },
    });

    return el('div', { class: 'linkrow' }, [
      el('span', { class: 'link-label', text: this.label }),
      el('code', { text: this.fingerprint(), title: this.value }),
      copyButton,
      // BOTH, always. A link is either handed to somebody else or followed
      // oneself, and the page offered only the first — so seeing what a CV
      // actually looks like meant copying an address and pasting it into
      // another tab. On a phone that is several deliberate actions to do the
      // most obvious thing on the page.
      el('a', {
        class: 'link-open', href: this.value, target: '_blank', rel: 'noopener',
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

interface ProfileActions {
  /**
   * Renew one link. The MODE matters: the two links are independent, and the
   * server has always been able to renew either — only the edit one was ever
   * offered, so a read link handed to the wrong person could not be revoked
   * from here at all.
   */
  rotate: (profile: ProfileSummary, mode: 'edit' | 'read') => void;
  remove: (profile: ProfileSummary) => void;
}

/** ProfileTable is the inventory of CVs. */
class ProfileTable {
  constructor(
    private readonly node: HTMLElement,
    private readonly actions: ProfileActions,
  ) {}

  show(answer: InventoryAnswer): void {
    if (!answer.profiles.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'No CVs yet.' })]);
      return;
    }
    replace(this.node, [table(
      [
        { label: 'CV', width: '13%' },
        { label: 'access', width: '11%' },
        { label: 'what it is', width: '17%' },
        { label: 'links', width: '17%' },
        { label: 'size', width: '9%' },
        { label: 'changed', width: '12%' },
        { label: '', width: '21%' },
      ],
      answer.profiles.map((p) => this.row(p)),
    )]);
  }

  private row(p: ProfileSummary): HTMLElement {
    const cells: Child[] = [
      el('td', { class: 'who' }, [
        el('div', { class: 'name', text: p.name }),
        el('code', { class: 'muted', text: p.slug }),
      ]),
      el('td', { class: 'access' }, [
        el('span', {
          class: `tag${p.public ? ' public' : ''}`,
          text: p.public ? 'published' : 'by link only',
        }),
        ' ',
        el('span', { class: 'tag', text: p.languages.map((l) => l.lang).join(', ') }),
        // An unreadable document looked exactly like a healthy one: same row,
        // same size, same date. The one moment anybody scans this page is when
        // something is wrong, and it was the one thing the page did not say.
        p.ok ? null : el('span', {
          class: 'tag broken', text: 'unreadable', title: p.problem ?? '',
        }),
        p.templateMissing ? el('span', {
          class: 'tag broken', text: 'template missing',
          title: `This CV was written for ${p.template}, which is not installed. `
            + 'It will not render as its author last saw it.',
        }) : null,
      ]),
      el('td', { class: 'what' }, [
        // What the CV IS, not merely how big it is. A list of names and byte
        // counts is an inventory of files; this is an inventory of CVs.
        p.role ? el('div', { class: 'role', text: p.role }) : null,
        el('div', { class: 'muted', text: [
          p.template,
          p.sections === undefined ? null : `${p.sections} sections`,
          p.photo ? 'photo' : null,
        ].filter(Boolean).join(' · ') }),
      ]),
      el('td', { class: 'links' }, [
        new CopyField('edit', p.links.edit, () => this.actions.rotate(p, 'edit')).render(),
        new CopyField('read', p.links.read, () => this.actions.rotate(p, 'read')).render(),
      ]),
      el('td', { class: 'size', text: `${show.bytes(p.bytes)} \u00b7 ${p.history} entries` }),
      el('td', { class: 'when', text: show.when(p.updatedAt) }),
      el('td', { class: 'actions' }, [
        // NOT "edit" and not "view": both are the private links, and both are
        // already offered beside the link they belong to, with `open`. Having
        // them here as well gave every row two buttons that did the same
        // thing — and the two would have had to keep agreeing about which
        // link they meant.
        //
        // What is left is what ONLY this port can do: it serves any CV
        // directly, without a token, which is how an administrator looks at
        // one whose link they have not got in front of them.
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.html`, target: '_blank', text: 'page' }),
        ' ',
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.pdf`, target: '_blank', text: 'pdf' }),
        el('button', { class: 'danger', text: 'delete', onclick: () => this.actions.remove(p) }),
      ]),
    ];
    return el('tr', {}, cells);
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
      replace(this.node, [el('div', { class: 'empty', text: 'Nothing set aside.' })]);
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
 * Views is the pair of tabs.
 *
 * The CVs and the trash are not looked at in the same motion: one is the
 * current inventory, the other a safety net opened when something has gone
 * missing. They were stacked, which padded the inventory with what nobody was
 * looking for.
 */
class Views {
  private readonly tabs: { button: HTMLButtonElement; panel: HTMLElement }[];

  constructor(root: Document) {
    this.tabs = [
      { button: need<HTMLButtonElement>(root, '#tab-cvs'), panel: need(root, '#view-cvs') },
      { button: need<HTMLButtonElement>(root, '#tab-trash'), panel: need(root, '#view-trash') },
    ];
    for (const [index, tab] of this.tabs.entries()) {
      tab.button.addEventListener('click', () => this.select(index));
    }
  }

  select(index: number): void {
    for (const [i, tab] of this.tabs.entries()) {
      const on = i === index;
      tab.button.setAttribute('aria-selected', String(on));
      tab.panel.hidden = !on;
    }
  }
}

class Admin {
  private readonly api = new HttpClient();
  private readonly profiles: ProfileTable;
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

  private page = 1;

  constructor(root: Document) {
    this.list = need(root, '#list');
    this.trashNode = need(root, '#trash');
    this.search = need<HTMLInputElement>(root, '#q');
    this.count = need(root, '#count');
    this.policy = need(root, '#policy');
    this.create = need(root, '#create');
    this.badgeCvs = need(root, '#badge-cvs');
    this.badgeTrash = need(root, '#badge-trash');

    this.views = new Views(root);
    this.metrics = new MetricsPanel(need(root, '#metrics'), this.api);

    this.profiles = new ProfileTable(this.list, {
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
        `“${p.name}” was set aside.`,
        {
          title: 'Delete this CV?',
          body: 'It is set aside first and can be restored until it expires. '
            + 'Both private links stop working now.',
          target: `${p.name} — ${p.slug}`,
          confirm: 'Delete',
        }),
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
      this.views.select(0);
      this.create.hidden = !this.create.hidden;
      if (!this.create.hidden) this.create.querySelector('input')?.focus();
    });
  }

  start(): void {
    this.create.appendChild(new CreateForm(this.api, (name) => {
      toast.show(`“${name}” created — hand over its edit link.`);
      void this.refresh();
    }).render());
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
