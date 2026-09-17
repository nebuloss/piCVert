// controls.js — the field tree, turned into a form.
//
// # THERE IS NO PER-SECTION CODE HERE, AND THAT IS THE WHOLE DESIGN
//
// The server sends a description of the document — categories, sub-categories,
// leaves and their kinds — and this walks it. A field added to the engine's
// vocabulary appears in the editor with nothing here changing, and a template
// that composes its sections differently is described in its own terms.
//
// # WHY CLASSES AND A REGISTRY RATHER THAN A SWITCH
//
// This is the same argument `layout.Layouter` makes in the Go engine, and for
// the same reason. A switch on the field kind has to be repeated — once to
// build the control, once to read it back, once to make a blank one for a new
// list entry — and the compiler has nothing to say about the branch you missed.
// A kind with no case simply renders nothing, which surfaces as a field that is
// mysteriously absent from one section.
//
// As classes, the three operations sit together per kind, and a kind with no
// class fails loudly with its own name. The clinching case is the same one too:
// `text` and `rich` are one control with one flag, and as two switch branches
// they could only be kept in step by hand.
//
// Controls are a COMPOSITE: a group holds controls, a list holds groups. That
// is not a design choice so much as the field tree's own shape, followed.

import { el } from '../lib/dom.js';
import { Path } from '../model/document.js';

/**
 * Control is one field, at one path.
 *
 * Every control reports through the DOCUMENT, not through a callback chain:
 * `this.doc.set(path, value)`. A control that handed its value to its parent to
 * store would make every container responsible for knowing where its children
 * belong, and a group nested three deep would assemble the path from three
 * partial answers.
 */
export class Control {
  constructor(field, path, doc) {
    this.field = field;
    this.path = path;
    this.doc = doc;
  }

  get value() { return this.doc.get(this.path); }
  set value(next) { this.doc.set(this.path, next); }

  /** render returns the element. Subclasses implement it. */
  render() {
    throw new Error(`control for "${this.field.kind}" does not render`);
  }

  /**
   * blank is what a new entry of this kind looks like.
   *
   * On the class rather than in a helper, because it is the same knowledge as
   * rendering: whoever knows a group is drawn as its members' controls is who
   * knows an empty group is an object with those keys. Split apart, they drift,
   * and a new list entry renders as nothing.
   */
  static blank() { return ''; }

  /** The label, with the mark that says a field cannot be left out. */
  labelText() {
    return this.field.label + (this.field.required ? ' *' : '');
  }

  /** row wraps a control in its label and help text. */
  row(inner, extra = '') {
    return el('div', { class: `row${extra}` }, [
      el('label', { text: this.labelText() }),
      inner,
      this.field.help ? el('div', { class: 'help', text: this.field.help }) : null,
    ]);
  }
}

class TextControl extends Control {
  render() {
    const rich = this.field.kind === 'rich';
    const input = el(rich ? 'textarea' : 'input', {
      type: rich ? undefined : 'text',
      rows: rich ? (this.field.rows ?? 4) : undefined,
      maxlength: this.field.max ?? undefined,
      placeholder: this.field.placeholder ?? '',
      oninput: (event) => { this.value = event.target.value; },
    });
    input.value = this.value ?? '';
    return this.row(input);
  }
}

class NumberControl extends Control {
  render() {
    const input = el('input', {
      type: 'number',
      min: this.field.min ?? undefined,
      max: this.field.max ?? undefined,
      oninput: (event) => {
        // An empty number field means "no value", not zero. Writing zero back
        // would turn a blank gauge into a gauge reading nothing out of ten,
        // which is a claim the author never made.
        this.value = event.target.value === '' ? null : Number(event.target.value);
      },
    });
    input.value = this.value ?? '';
    return this.row(input);
  }

  static blank() { return null; }
}

class BoolControl extends Control {
  render() {
    const input = el('input', {
      type: 'checkbox',
      onchange: (event) => { this.value = event.target.checked; },
    });
    input.checked = Boolean(this.value);
    // Its own shape: a checkbox under a label reads as a heading with a stray
    // box beneath it, where a checkbox beside one reads as a question.
    return el('div', { class: 'row bool' }, [input, el('label', { text: this.field.label })]);
  }

  static blank() { return false; }
}

class EnumControl extends Control {
  render() {
    const select = el('select', {
      onchange: (event) => { this.value = event.target.value; },
    });
    for (const option of this.field.values ?? []) {
      const node = el('option', { value: option.value, text: option.label || option.value });
      if (String(this.value ?? '') === String(option.value)) node.selected = true;
      select.appendChild(node);
    }
    return this.row(select);
  }
}

class IconControl extends Control {
  /**
   * An icon is a name from the template's set, so the control is a list of what
   * the template actually ships. Typed free-hand it is a field where a
   * misspelling renders as a blank square, with nothing to say why.
   */
  render() {
    const names = this.doc.icons ?? [];
    if (!names.length) return new TextControl(this.field, this.path, this.doc).render();

    const select = el('select', {
      onchange: (event) => { this.value = event.target.value; },
    });
    select.appendChild(el('option', { value: '', text: '— none —' }));
    for (const name of names) {
      const node = el('option', { value: name, text: name });
      if (this.value === name) node.selected = true;
      select.appendChild(node);
    }
    return this.row(select);
  }
}

class GroupControl extends Control {
  render() {
    return el('div', {}, (this.field.fields ?? []).map((child) =>
      build(child, Path.join(this.path, child.key), this.doc).render()));
  }

  static blankOf(field) {
    const out = {};
    for (const child of field.fields ?? []) out[child.key] = blankOf(child);
    return out;
  }
}

/**
 * ListControl draws an ordered series, with the controls that order it.
 *
 * Move and remove act on the WHOLE list and write it back in one go. Reporting
 * "element 3 moved up" would make every reader reconstruct the array, and two
 * reconstructions eventually differ — the same argument the engine makes for
 * computing a layout once.
 */
class ListControl extends Control {
  render() {
    const items = Array.isArray(this.value) ? this.value : [];
    const replace = (next) => { this.value = next; };

    const entries = items.map((_, index) => {
      const move = (to) => {
        if (to < 0 || to >= items.length) return;
        const next = items.slice();
        next.splice(to, 0, next.splice(index, 1)[0]);
        replace(next);
      };
      return el('div', { class: 'entry' }, [
        el('div', { class: 'entry-bar' }, [
          el('button', { type: 'button', title: 'Move up', text: '↑',
            onclick: () => move(index - 1) }),
          el('button', { type: 'button', title: 'Move down', text: '↓',
            onclick: () => move(index + 1) }),
          el('button', { type: 'button', title: 'Remove', text: '✕',
            onclick: () => replace(items.filter((_, i) => i !== index)) }),
        ]),
        build(this.field.of, `${this.path}[${index}]`, this.doc).render(),
      ]);
    });

    return el('fieldset', { class: 'list' }, [
      el('legend', { text: this.field.label }),
      ...entries,
      el('button', {
        type: 'button',
        text: this.field.addLabel || `Add ${this.field.label.toLowerCase()}`,
        onclick: () => replace(items.concat([blankOf(this.field.of)])),
      }),
    ]);
  }

  static blank() { return []; }
}

/**
 * PhotoControl uploads through its own route rather than into the document.
 *
 * A portrait is 200 kB of binary; carrying it as base64 inside the CV would put
 * a third more than that through every save, on every keystroke's worth of
 * autosave. It goes once, as bytes, and the document keeps its filename.
 */
class PhotoControl extends Control {
  constructor(field, path, doc, upload) {
    super(field, path, doc);
    this.upload = upload;
  }

  render() {
    const current = this.value;
    const input = el('input', {
      type: 'file',
      accept: 'image/png,image/jpeg,image/webp',
      onchange: (event) => {
        const file = event.target.files?.[0];
        if (file) this.upload?.(file);
      },
    });
    return this.row(el('div', { class: 'photo' }, [
      input,
      current ? el('span', { class: 'help', text: `current: ${current}` }) : null,
    ]));
  }
}

/**
 * registry maps a field kind to its control.
 *
 * Data rather than a switch, for the reason the engine's `layouters` map is:
 * registration is then itself checkable, and a kind the vocabulary declares but
 * nothing implements fails with its own name rather than rendering as nothing.
 */
const registry = new Map([
  ['text', TextControl],
  ['rich', TextControl],
  ['number', NumberControl],
  ['bool', BoolControl],
  ['enum', EnumControl],
  ['icon', IconControl],
  ['group', GroupControl],
  ['list', ListControl],
  ['photo', PhotoControl],
]);

/** register adds a control kind. Exported so a widget can be added from outside. */
export function register(kind, control) {
  registry.set(kind, control);
}

/** build makes the control for a field. */
export function build(field, path, doc, extra) {
  const Kind = registry.get(field.kind);
  if (!Kind) {
    // Loudly, and in place: a field nobody can edit is a field whose contents
    // cannot be corrected, and silence would hide that behind a form that looks
    // complete.
    return {
      render: () => el('div', { class: 'row' }, [
        el('label', { text: field.label }),
        el('div', { class: 'help error', text: `no control for field kind "${field.kind}"` }),
      ]),
    };
  }
  return new Kind(field, path, doc, extra);
}

/** blankOf is an empty value of a field's kind. */
export function blankOf(field) {
  const Kind = registry.get(field.kind);
  if (Kind === GroupControl) return GroupControl.blankOf(field);
  if (field.kind === 'enum') return field.default ?? '';
  return Kind ? Kind.blank() : '';
}

/** panel is a collapsible block of the form. */
export function panel(title, tag, children, open = false) {
  return el('details', { class: 'panel', open: open || undefined }, [
    el('summary', {}, [
      el('span', { class: 'grow', text: title }),
      tag ? el('span', { class: 'tag', text: tag }) : null,
    ]),
    el('div', { class: 'panel-body' }, children),
  ]);
}
