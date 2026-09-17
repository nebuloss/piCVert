// admin.js — the inventory, and the things only this port can do.
//
// It talks to its OWN origin and nothing else. The admin port and the public
// port are two services that happen to share a data directory, and a page here
// reaching into the public one would be the first step towards this interface
// needing to be reachable from outside — which is precisely what its design
// avoids. The links it displays point at the public service because a person
// has to paste them somewhere; nothing here fetches them.

import { HttpClient } from '../lib/http.js';
import { clear, copy, el, replace } from '../lib/dom.js';

/** Bytes and dates, rendered the same way everywhere they appear. */
const show = {
  bytes(n) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${Math.round(n / 1024)} kB`;
    return `${(n / 1024 / 1024).toFixed(1)} MB`;
  },
  when(stamp) {
    if (!stamp) return '';
    const date = new Date(stamp);
    return Number.isNaN(date.valueOf()) ? stamp : date.toLocaleString();
  },
};

/** table builds a headed table, since both halves of this page are one. */
function table(headings, rows) {
  return el('table', {}, [
    el('thead', {}, [el('tr', {}, headings.map((h) => el('th', { text: h })))]),
    el('tbody', {}, rows),
  ]);
}

/**
 * CopyField is a link with a button that copies it.
 *
 * A token is 32 characters of base64 and the link around it is longer. Selecting
 * that by hand, from a table, without catching the whitespace either side, is a
 * thing people get wrong — and a link that is wrong by one character is a link
 * whose failure looks exactly like a revoked one.
 */
class CopyField {
  constructor(label, value) {
    this.label = label;
    this.value = value;
  }

  render() {
    const button = el('button', {
      type: 'button', class: 'link-copy', title: `Copy the ${this.label} link`,
      text: this.label,
      onclick: async () => {
        const done = await copy(this.value);
        button.textContent = done ? 'copied' : 'select it';
        setTimeout(() => { button.textContent = this.label; }, 1500);
      },
    });
    return el('div', { class: 'linkrow' }, [button, el('code', { text: this.value })]);
  }
}

/** CreateForm makes a CV. It is the only way one comes into existence. */
class CreateForm {
  constructor(api, onCreated) {
    this.api = api;
    this.onCreated = onCreated;
  }

  render() {
    const slug = el('input', {
      type: 'text', placeholder: 'jean-dupont', pattern: '[a-z0-9][a-z0-9_-]*',
      required: true,
    });
    const name = el('input', { type: 'text', placeholder: 'Jean Dupont' });
    const lang = el('input', { type: 'text', placeholder: 'en', maxlength: 2, size: 4 });
    const template = el('select', {});
    const note = el('div', { class: 'help' });

    this.api.get('/api/templates')
      .then((answer) => {
        for (const t of answer.templates ?? []) {
          template.appendChild(el('option', { value: t.uuid, text: t.title || t.name }));
        }
      })
      .catch((error) => { note.textContent = error.message; });

    const submit = el('button', {
      type: 'submit', class: 'primary', text: 'Create',
    });

    const form = el('form', {
      class: 'create',
      onsubmit: async (event) => {
        event.preventDefault();
        submit.disabled = true;
        note.textContent = '';
        try {
          const answer = await this.api.post('/api/profiles', {
            slug: slug.value.trim(),
            name: name.value.trim(),
            template: template.value,
            lang: lang.value.trim(),
          });
          // The links are shown at once and not merely listed in the table. The
          // edit link is the ONLY way into the CV just made, and a person who
          // closes this page without it has created something nobody can open.
          replace(note, [
            el('strong', { text: 'Created. Hand this link to its author:' }),
            new CopyField('edit', answer.profile.links.edit).render(),
          ]);
          note.className = 'help created';
          slug.value = '';
          name.value = '';
          this.onCreated();
        } catch (error) {
          note.textContent = error.message;
          note.className = 'help error';
        } finally {
          submit.disabled = false;
        }
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

/** ProfileTable is the inventory of CVs. */
class ProfileTable {
  constructor(node, api, actions) {
    this.node = node;
    this.api = api;
    this.actions = actions;
  }

  show(answer) {
    if (!answer.profiles.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'No CVs yet.' })]);
      return;
    }
    replace(this.node, [table(
      ['CV', 'access', 'links', 'size', 'last change', ''],
      answer.profiles.map((p) => this.row(p)),
    )]);
  }

  row(p) {
    return el('tr', {}, [
      el('td', {}, [
        el('div', { text: p.name }),
        el('code', { class: 'muted', text: p.slug }),
      ]),
      el('td', {}, [
        el('span', {
          class: `tag${p.public ? ' public' : ''}`,
          text: p.public ? 'published' : 'by link only',
        }),
        ' ',
        el('span', { class: 'tag', text: (p.languages ?? []).map((l) => l.lang).join(', ') }),
      ]),
      el('td', { class: 'links' }, [
        new CopyField('edit', p.links.edit).render(),
        new CopyField('read', p.links.read).render(),
      ]),
      el('td', { text: `${show.bytes(p.bytes)} · ${p.history} entries` }),
      el('td', { text: show.when(p.updatedAt) }),
      el('td', { class: 'actions' }, [
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.html`, target: '_blank', text: 'page' }),
        ' ',
        el('a', { class: 'tag', href: `/view/${p.slug}/cv.pdf`, target: '_blank', text: 'pdf' }),
        el('button', {
          text: 'new edit link',
          onclick: () => this.actions.rotate(p),
        }),
        el('button', {
          class: 'danger', text: 'delete',
          onclick: () => this.actions.remove(p),
        }),
      ]),
    ]);
  }
}

/** TrashTable is what has been deleted and can still be caught. */
class TrashTable {
  constructor(node, actions) {
    this.node = node;
    this.actions = actions;
  }

  show(entries) {
    if (!entries?.length) {
      replace(this.node, [el('div', { class: 'empty', text: 'Nothing set aside.' })]);
      return;
    }
    replace(this.node, [table(
      ['CV', 'deleted', 'erased', 'size', ''],
      entries.map((entry) => el('tr', {}, [
        el('td', {}, [
          el('div', { text: entry.name }),
          el('code', { class: 'muted', text: entry.slug }),
        ]),
        el('td', { text: show.when(entry.deletedAt) }),
        el('td', { text: show.when(entry.expiresAt) }),
        el('td', { text: show.bytes(entry.bytes) }),
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
  constructor(root) {
    this.api = new HttpClient({});
    this.page = 1;
    this.node = {
      list: root.querySelector('#list'),
      trash: root.querySelector('#trash'),
      search: root.querySelector('#q'),
      count: root.querySelector('#count'),
      policy: root.querySelector('#policy'),
      prev: root.querySelector('#prev'),
      next: root.querySelector('#next'),
      create: root.querySelector('#create'),
    };

    this.profiles = new ProfileTable(this.node.list, this.api, {
      rotate: (p) => this.act(
        () => this.api.post(`/api/p/${p.slug}/links/rotate?mode=edit`),
        `Renew the edit link of “${p.name}”?\n\n` +
        `Every copy already handed out stops working at once.`),
      remove: (p) => this.act(
        () => this.api.delete(`/api/p/${p.slug}`),
        `Delete “${p.name}”?\n\n` +
        `It is set aside first and can be restored until it expires.`),
    });

    this.trash = new TrashTable(this.node.trash, {
      restore: (entry) => this.act(() => this.api.post(`/api/trash/${entry.slug}/restore`)),
      purge: (entry) => this.act(
        () => this.api.delete(`/api/trash/${entry.slug}`),
        `Erase “${entry.name}” for good?\n\nThis cannot be undone.`),
    });

    let typing = 0;
    this.node.search.addEventListener('input', () => {
      clearTimeout(typing);
      typing = setTimeout(() => { this.page = 1; this.refresh(); }, 200);
    });
    this.node.prev.addEventListener('click', () => {
      if (this.page > 1) { this.page -= 1; this.refresh(); }
    });
    this.node.next.addEventListener('click', () => { this.page += 1; this.refresh(); });
  }

  start() {
    this.node.create.appendChild(
      new CreateForm(this.api, () => this.refresh()).render());
    this.refresh();
    this.refreshTrash();
  }

  /** act performs something destructive, asks first, and refreshes after. */
  async act(run, question) {
    if (question && !confirm(question)) return;
    try {
      await run();
      await this.refresh();
      await this.refreshTrash();
    } catch (error) {
      alert(error.message);
    }
  }

  async refresh() {
    const params = new URLSearchParams({ page: String(this.page) });
    const q = this.node.search.value.trim();
    if (q) params.set('q', q);
    try {
      const answer = await this.api.get(`/api/profiles?${params}`);
      this.page = answer.page;
      this.node.policy.textContent = `published: ${answer.policy}`;
      this.node.count.textContent =
        `${answer.total} CV(s) — page ${answer.page}/${answer.pages}`;
      this.profiles.show(answer);
    } catch (error) {
      replace(clear(this.node.list), [el('div', { class: 'empty', text: error.message })]);
    }
  }

  async refreshTrash() {
    try {
      const answer = await this.api.get('/api/trash');
      this.trash.show(answer.entries);
    } catch (error) {
      replace(clear(this.node.trash), [el('div', { class: 'empty', text: error.message })]);
    }
  }
}

new Admin(document).start();
