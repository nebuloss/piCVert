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
  constructor(private readonly label: string, private readonly value: string) {}

  render(): HTMLElement {
    const button = el('button', {
      type: 'button', class: 'link-copy', title: `Copy the ${this.label} link`,
      text: this.label,
      onclick: () => {
        void copy(this.value).then((done) => {
          button.textContent = done ? 'copied' : 'select it';
          window.setTimeout(() => { button.textContent = this.label; }, 1500);
        });
      },
    });
    return el('div', { class: 'linkrow' }, [
      button,
      el('code', { text: this.value, title: this.value }),
    ]);
  }
}

/** CreateForm makes a CV. It is the only way one comes into existence here. */
class CreateForm {
  constructor(
    private readonly api: HttpClient,
    private readonly onCreated: () => void,
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
            slug.value = '';
            name.value = '';
            delete slug.dataset.touched;
            this.onCreated();
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
  rotate: (profile: ProfileSummary) => void;
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
        { label: 'access', width: '13%' },
        { label: 'links', width: '21%' },
        { label: 'size', width: '9%' },
        { label: 'last change', width: '13%' },
        { label: '', width: '31%' },
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
      ]),
      el('td', { class: 'links' }, [
        new CopyField('edit', p.links.edit).render(),
        new CopyField('read', p.links.read).render(),
      ]),
      el('td', { class: 'size', text: `${show.bytes(p.bytes)} \u00b7 ${p.history} entries` }),
      el('td', { class: 'when', text: show.when(p.updatedAt) }),
      el('td', { class: 'actions' }, [
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.html`, target: '_blank', text: 'page' }),
        ' ',
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.pdf`, target: '_blank', text: 'pdf' }),
        el('button', { text: 'new edit link', onclick: () => this.actions.rotate(p) }),
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

/** TrashTable is what has been deleted and can still be caught. */
class TrashTable {
  constructor(
    private readonly node: HTMLElement,
    private readonly actions: TrashActions,
  ) {}

  show(entries: TrashEntry[]): void {
    if (!entries.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'Nothing set aside.' })]);
      return;
    }
    replace(this.node, [table(
      [
        { label: 'CV', width: '26%' },
        { label: 'deleted', width: '18%' },
        { label: 'erased', width: '18%' },
        { label: 'size', width: '12%' },
        { label: '', width: '26%' },
      ],
      entries.map((entry) => el('tr', {}, [
        el('td', { class: 'who' }, [
          el('div', { class: 'name', text: entry.name }),
          el('code', { class: 'muted', text: entry.slug }),
        ]),
        el('td', { class: 'when', text: show.when(entry.deletedAt) }),
        el('td', { class: 'when', text: show.when(entry.expiresAt) }),
        el('td', { class: 'size', text: show.bytes(entry.bytes) }),
        el('td', { class: 'actions' }, [
          el('button', { text: 'restore', onclick: () => this.actions.restore(entry) }),
          el('button', { class: 'danger', text: 'erase now',
            onclick: () => this.actions.purge(entry) }),
        ]),
      ])),
    )]);
  }
}

class Admin {
  private readonly api = new HttpClient();
  private readonly profiles: ProfileTable;
  private readonly trash: TrashTable;

  private readonly list: HTMLElement;
  private readonly trashNode: HTMLElement;
  private readonly search: HTMLInputElement;
  private readonly count: HTMLElement;
  private readonly policy: HTMLElement;
  private readonly create: HTMLElement;
  private readonly metrics: MetricsPanel;

  private page = 1;

  constructor(root: Document) {
    this.list = need(root, '#list');
    this.trashNode = need(root, '#trash');
    this.search = need<HTMLInputElement>(root, '#q');
    this.count = need(root, '#count');
    this.policy = need(root, '#policy');
    this.create = need(root, '#create');

    this.metrics = new MetricsPanel(need(root, '#metrics'), this.api);

    this.profiles = new ProfileTable(this.list, {
      rotate: (p) => void this.act(
        () => this.api.post(`/api/p/${p.slug}/links/rotate?mode=edit`),
        `Renew the edit link of “${p.name}”?\n\n` +
        `Every copy already handed out stops working at once.`),
      remove: (p) => void this.act(
        () => this.api.delete(`/api/p/${p.slug}`),
        `Delete “${p.name}”?\n\n` +
        `It is set aside first and can be restored until it expires.`),
    });

    this.trash = new TrashTable(this.trashNode, {
      restore: (entry) => void this.act(
        () => this.api.post(`/api/trash/${entry.slug}/restore`)),
      purge: (entry) => void this.act(
        () => this.api.delete(`/api/trash/${entry.slug}`),
        `Erase “${entry.name}” for good?\n\nThis cannot be undone.`),
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
  }

  start(): void {
    this.create.appendChild(new CreateForm(this.api, () => void this.refresh()).render());
    void this.refresh();
    void this.refreshTrash();
    void this.metrics.refresh();
    // Every thirty seconds. Often enough to watch something happen, rarely
    // enough that a page left open all day is not a load in itself.
    window.setInterval(() => void this.metrics.refresh(), 30_000);
  }

  /** act performs something destructive, asks first, and refreshes after. */
  private async act(run: () => Promise<unknown>, question?: string): Promise<void> {
    if (question && !confirm(question)) return;
    try {
      await run();
      await this.refresh();
      await this.refreshTrash();
    } catch (error) {
      alert(error instanceof Error ? error.message : String(error));
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
      this.profiles.show(answer);
    } catch (error) {
      replace(this.list, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
    }
  }

  private async refreshTrash(): Promise<void> {
    try {
      const answer = await this.api.get<TrashAnswer>('/api/trash');
      this.trash.show(answer.entries ?? []);
    } catch (error) {
      replace(this.trashNode, [el('div', { class: 'empty',
        text: error instanceof Error ? error.message : String(error) })]);
    }
  }
}

new Admin(document).start();
