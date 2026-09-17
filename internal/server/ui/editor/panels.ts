/**
 * panels.ts — the blocks of the form that are not fields.
 *
 * Each is a `Renderable`, exactly like a Control, because that is what the form
 * does with them. They are separate from the controls because they are not
 * derived from the field tree: they are the operations you can perform on a CV
 * rather than the values it holds.
 */

import { copy, el } from '../lib/dom.ts';
import type { Api } from './api.ts';
import type { CvDocument } from '../model/document.ts';
import type { DeleteAnswer, HistoryEntry, LanguageEntry, Template, TemplateSummary } from '../model/api.ts';

/** Anything the form can put on the page. */
export interface Renderable {
  render(): HTMLElement;
}

/** SectionBar is the move and remove controls above a section's fields. */
export class SectionBar implements Renderable {
  constructor(
    private readonly index: number,
    private readonly total: number,
    private readonly onReorder: (order: number[]) => void,
    private readonly onRemove: (index: number) => void,
  ) {}

  render(): HTMLElement {
    const move = (to: number): void => {
      if (to < 0 || to >= this.total) return;
      // Sent as a PERMUTATION rather than as "this one moved there". The server
      // validates a permutation — every position used exactly once — and can
      // therefore refuse an order that would drop or duplicate a section, which
      // an instruction to move one element cannot be checked for.
      const order = [...Array(this.total).keys()];
      order.splice(to, 0, ...order.splice(this.index, 1));
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
export class AddSection implements Renderable {
  constructor(
    private readonly template: Template,
    private readonly onAdd: (type: string) => void,
  ) {}

  render(): HTMLElement {
    const select = el('select', {},
      this.template.sections.map((slot) =>
        el('option', { value: slot.type, text: slot.label || slot.type })));
    return el('div', { class: 'row inline' }, [
      select,
      el('button', { type: 'button', text: 'Add', onclick: () => this.onAdd(select.value) }),
    ]);
  }
}

/** TemplatePicker switches the layout, and reports a refusal rather than hiding it. */
export class TemplatePicker implements Renderable {
  constructor(
    private readonly templates: TemplateSummary[],
    private readonly current: string,
    private readonly onPick: (uuid: string) => Promise<void>,
  ) {}

  render(): HTMLElement {
    const select = el('select', {
      onchange: () => {
        void this.onPick(select.value).catch((error: unknown) => {
          // Put back, because the switch did NOT happen. A picker showing the
          // template that was refused is a picker lying about what is on the
          // page — and the refusal is deliberate: a layout that cannot draw one
          // of your sections would lose it.
          select.value = this.current;
          throw error;
        });
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
export class SharePanel implements Renderable {
  constructor(private readonly api: Api) {}

  render(): HTMLElement {
    const field = el('input', { type: 'text', readonly: true });
    field.value = 'loading…';

    const button = el('button', {
      type: 'button', text: 'Copy',
      onclick: () => {
        field.select();
        void copy(field.value).then((done) => {
          // The text is selected either way, so the keyboard still works and
          // the button has not claimed something it did not do.
          button.textContent = done ? 'Copied' : 'Press Ctrl+C';
          window.setTimeout(() => { button.textContent = 'Copy'; }, 1800);
        });
      },
    });

    this.api.links()
      .then((answer) => { field.value = answer.read; })
      .catch((error: unknown) => {
        field.value = error instanceof Error ? error.message : String(error);
      });

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
 * sentence. Somebody who knows this is reversible for a day does not have to be
 * certain before clicking, and somebody who does not know it may never click at
 * all and keep a CV they wanted gone.
 */
export class DeletePanel implements Renderable {
  constructor(
    private readonly doc: CvDocument,
    private readonly api: Api,
    private readonly onDeleted: (answer: DeleteAnswer) => void,
    private readonly onError: (error: unknown) => void,
  ) {}

  render(): HTMLElement {
    const expected = this.doc.name;
    const button = el('button', {
      type: 'button', class: 'danger', text: 'Delete this CV', disabled: true,
      onclick: () => {
        button.disabled = true;
        this.api.remove()
          .then((answer) => this.onDeleted(answer))
          .catch((error: unknown) => {
            button.disabled = false;
            this.onError(error);
          });
      },
    });
    const field = el('input', {
      type: 'text',
      placeholder: expected || 'the name on this CV',
      oninput: (event: Event) => {
        const typed = (event.target as HTMLInputElement).value.trim();
        button.disabled = typed !== expected || !expected;
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
  constructor(
    private readonly select: HTMLSelectElement,
    addButton: HTMLButtonElement,
    onSwitch: (lang: string) => void,
    onAdd: () => void,
  ) {
    select.addEventListener('change', () => onSwitch(select.value));
    addButton.addEventListener('click', () => onAdd());
  }

  show(languages: LanguageEntry[], current: string): void {
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
 * → Summa" would bury the change somebody is actually looking for.
 */
export class HistoryPanel {
  constructor(
    private readonly dialog: HTMLDialogElement,
    private readonly body: HTMLElement,
    private readonly api: Api,
  ) {}

  async open(): Promise<void> {
    this.body.textContent = 'loading…';
    this.dialog.showModal();
    try {
      const answer = await this.api.history();
      this.body.textContent = '';
      if (!answer.entries.length) {
        this.body.appendChild(el('p', { text: 'Nothing recorded yet.' }));
        return;
      }
      for (const entry of answer.entries) this.body.appendChild(line(entry));
    } catch (error) {
      this.body.textContent = error instanceof Error ? error.message : String(error);
    }
  }
}

function line(entry: HistoryEntry): HTMLElement {
  const trail = entry.trail
    .map((crumb) => crumb.label + (crumb.n ? ` #${crumb.n}` : ''))
    .join(' › ');
  const shown = (value: HistoryEntry['before']): string =>
    value === null || value === undefined || value === '' ? '(nothing)' : String(value);

  return el('div', { class: 'entryline' }, [
    el('div', { class: 'trail', text: `${when(entry.at)} — ${trail}` }),
    entry.kind === 'move'
      ? el('div', { text: `moved from position ${String(entry.before)} to ${String(entry.after)}` })
      : el('div', {}, [
          el('del', { text: shown(entry.before) }),
          ' → ',
          el('ins', { text: shown(entry.after) }),
        ]),
  ]);
}

function when(stamp: string): string {
  const date = new Date(stamp);
  return Number.isNaN(date.valueOf()) ? stamp : date.toLocaleString();
}
