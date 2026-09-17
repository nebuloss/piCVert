// panels.js — the blocks of the form that are not fields.
//
// Each is a class with a `render()`, exactly like a Control, because that is
// what the form does with them. They are separate from the controls because
// they are not derived from the field tree: they are the operations you can
// perform on a CV rather than the values it holds.

import { copy, el } from '../lib/dom.js';

/** SectionBar is the move and remove controls above a section's fields. */
export class SectionBar {
  constructor(doc, index, total, { onReorder, onRemove }) {
    this.doc = doc;
    this.index = index;
    this.total = total;
    this.onReorder = onReorder;
    this.onRemove = onRemove;
  }

  render() {
    const move = (to) => {
      if (to < 0 || to >= this.total) return;
      // Sent as a PERMUTATION rather than as "this one moved there". The server
      // validates a permutation — every position used exactly once — and can
      // therefore refuse an order that would drop or duplicate a section, which
      // an instruction to move one element cannot be checked for.
      const order = [...Array(this.total).keys()];
      order.splice(to, 0, order.splice(this.index, 1)[0]);
      this.onReorder(order);
    };
    return el('div', { class: 'entry-bar' }, [
      el('button', { type: 'button', text: '↑ section', onclick: () => move(this.index - 1) }),
      el('button', { type: 'button', text: '↓ section', onclick: () => move(this.index + 1) }),
      el('button', { type: 'button', class: 'danger', text: '✕ section',
        onclick: () => this.onRemove(this.index) }),
    ]);
  }
}

/** AddSection offers the kinds this template can actually draw. */
export class AddSection {
  constructor(template, onAdd) {
    this.template = template;
    this.onAdd = onAdd;
  }

  render() {
    const select = el('select', {}, (this.template.sections ?? []).map((slot) =>
      el('option', { value: slot.type, text: slot.label || slot.type })));
    return el('div', { class: 'row inline' }, [
      select,
      el('button', { type: 'button', text: 'Add', onclick: () => this.onAdd(select.value) }),
    ]);
  }
}

/** TemplatePicker switches the layout, and reports a refusal rather than hiding it. */
export class TemplatePicker {
  constructor(templates, current, onPick) {
    this.templates = templates;
    this.current = current;
    this.onPick = onPick;
  }

  render() {
    const select = el('select', {
      onchange: async () => {
        try {
          await this.onPick(select.value);
        } catch (error) {
          // Put back, because the switch did NOT happen. A picker showing the
          // template that was refused is a picker lying about what is on the
          // page — and the refusal is deliberate: a layout that cannot draw one
          // of your sections would lose it.
          select.value = this.current;
          throw error;
        }
      },
    });
    for (const t of this.templates) {
      const option = el('option', { value: t.uuid, text: t.title || t.name });
      if (t.uuid === this.current) option.selected = true;
      select.appendChild(option);
    }
    return el('div', { class: 'row' }, [el('label', { text: 'Template' }), select]);
  }
}

/**
 * SharePanel hands over the read-only link.
 *
 * The EDIT link is never shown. Whoever is reading this page already holds it —
 * it is in their address bar — and printing it again only puts it somewhere
 * else it can be copied from by mistake. What one actually needs is the other
 * link: the one you can give a recruiter without giving them the ability to
 * rewrite your CV.
 */
export class SharePanel {
  constructor(api) {
    this.api = api;
  }

  render() {
    const field = el('input', { type: 'text', readonly: true, value: 'loading…' });
    const button = el('button', {
      type: 'button', text: 'Copy',
      onclick: async () => {
        field.select();
        const done = await copy(field.value);
        // The text is selected either way, so the keyboard still works and the
        // button has not claimed something it did not do.
        button.textContent = done ? 'Copied' : 'Press Ctrl+C';
        setTimeout(() => { button.textContent = 'Copy'; }, 1800);
      },
    });

    this.api.links()
      .then((answer) => { field.value = answer.read; })
      .catch((error) => { field.value = error.message; });

    return el('div', {}, [
      el('div', { class: 'row' }, [
        el('label', { text: 'Read-only link — safe to send' }),
        field,
        el('div', { class: 'help', text:
          'It shows the CV and offers the PDF. It cannot change anything, and ' +
          'it does not reveal the link you are using now.' }),
      ]),
      button,
    ]);
  }
}

/**
 * DeletePanel removes the CV, with the two things that make that safe.
 *
 * THE NAME HAS TO BE TYPED. A confirmation dialog is one keystroke from being
 * dismissed by reflex, and what this destroys is the only copy: there is no
 * account behind it and no backup a support desk can reach. Typing the name is
 * the smallest gesture that cannot be made by accident.
 *
 * AND THE GRACE PERIOD IS STATED, because that is the reassuring half of the
 * sentence. Someone who knows this is reversible for a day does not have to be
 * certain before clicking, and someone who does not know it may never click at
 * all and keep a CV they wanted gone.
 */
export class DeletePanel {
  constructor(doc, api, onDeleted) {
    this.doc = doc;
    this.api = api;
    this.onDeleted = onDeleted;
  }

  render() {
    const expected = this.doc.name;
    const button = el('button', {
      type: 'button', class: 'danger', text: 'Delete this CV', disabled: true,
      onclick: async () => {
        button.disabled = true;
        try {
          const answer = await this.api.remove();
          this.onDeleted(answer);
        } catch (error) {
          button.disabled = false;
          throw error;
        }
      },
    });
    const field = el('input', {
      type: 'text',
      placeholder: expected || 'the name on this CV',
      oninput: (event) => {
        button.disabled = event.target.value.trim() !== expected || !expected;
      },
    });

    return el('div', {}, [
      el('div', { class: 'row' }, [
        el('label', { text: 'Type the name on this CV to confirm' }),
        field,
        el('div', { class: 'help', text:
          'There is no account behind this CV and no other copy. It is set ' +
          'aside for a day first, so a mistake can be undone, and then it is gone.' }),
      ]),
      button,
    ]);
  }
}

/** LanguageBar switches between a CV's languages and adds one. */
export class LanguageBar {
  constructor(select, addButton, { onSwitch, onAdd }) {
    this.select = select;
    this.addButton = addButton;
    select.addEventListener('change', () => onSwitch(select.value));
    addButton.addEventListener('click', () => onAdd());
  }

  show(languages, current) {
    this.select.textContent = '';
    for (const entry of languages) {
      const option = el('option', {
        value: entry.variant ?? '',
        text: entry.lang.toUpperCase() + (entry.isDefault ? ' (default)' : ''),
      });
      if ((entry.variant ?? '') === current) option.selected = true;
      this.select.appendChild(option);
    }
    // A picker with one entry teaches people that it does nothing, so it only
    // appears once there is a choice to make. The add button always shows:
    // that is how the second language comes to exist.
    this.select.hidden = languages.length < 2;
  }
}

/**
 * HistoryPanel shows the journal.
 *
 * One line per EPISODE of editing, which is what the server stores: a paragraph
 * rewritten over two minutes is one act, and eighty lines of "Summary → Summar
 * → Summa" would bury the change someone is actually looking for.
 */
export class HistoryPanel {
  constructor(dialog, body, api) {
    this.dialog = dialog;
    this.body = body;
    this.api = api;
  }

  async open() {
    this.body.textContent = 'loading…';
    this.dialog.showModal();
    try {
      const answer = await this.api.history();
      this.body.textContent = '';
      if (!answer.entries.length) {
        this.body.appendChild(el('p', { text: 'Nothing recorded yet.' }));
        return;
      }
      for (const entry of answer.entries) this.body.appendChild(this.line(entry));
    } catch (error) {
      this.body.textContent = error.message;
    }
  }

  line(entry) {
    const trail = (entry.trail ?? [])
      .map((crumb) => crumb.label + (crumb.n ? ` #${crumb.n}` : ''))
      .join(' › ');
    const shown = (value) =>
      value === null || value === undefined || value === '' ? '(nothing)' : String(value);

    return el('div', { class: 'entryline' }, [
      el('div', { class: 'trail', text: `${when(entry.at)} — ${trail}` }),
      entry.kind === 'move'
        ? el('div', { text: `moved from position ${entry.before} to ${entry.after}` })
        : el('div', {}, [
            el('del', { text: shown(entry.before) }),
            ' → ',
            el('ins', { text: shown(entry.after) }),
          ]),
    ]);
  }
}

function when(stamp) {
  const date = new Date(stamp);
  return Number.isNaN(date.valueOf()) ? stamp : date.toLocaleString();
}
