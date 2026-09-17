/**
 * controls.ts — the field tree, turned into a form.
 *
 * # THERE IS NO PER-SECTION CODE HERE, AND THAT IS THE WHOLE DESIGN
 *
 * The server sends a description of the document — categories, sub-categories,
 * leaves and their kinds — and this walks it. A field added to the engine's
 * vocabulary appears in the editor with nothing here changing, and a template
 * that composes its sections differently is described in its own terms.
 *
 * # WHY CLASSES AND A REGISTRY RATHER THAN A SWITCH
 *
 * This is the same argument `layout.Layouter` makes in the Go engine, and for
 * the same reason. A switch on the field kind has to be repeated — once to
 * build the control, once to read it back, once to make a blank one for a new
 * list entry — and the compiler has nothing to say about the branch you missed.
 * A kind with no case simply renders nothing, which surfaces as a field
 * mysteriously absent from one section.
 *
 * As classes the three operations sit together per kind, `abstract render()`
 * makes a subclass that forgets one a compile error, and the registry's own
 * type makes a kind with no class a compile error too. The clinching case is
 * the same as the engine's: `text` and `rich` are one control with one flag,
 * and as two switch branches they could only be kept in step by hand.
 *
 * Controls are a COMPOSITE: a group holds controls, a list holds groups. That
 * is not a design choice so much as the field tree's own shape, followed.
 */

import { el } from '../lib/dom.ts';
import type { Child } from '../lib/dom.ts';
import { Path } from '../model/document.ts';
import type { CvDocument } from '../model/document.ts';
import type { Field, FieldKind } from '../model/api.ts';

/** What a photo control does with the file it was given. */
export type UploadPhoto = (file: File) => void;

/**
 * Control is one field, at one path.
 *
 * Every control reports through the DOCUMENT, not through a callback chain:
 * `this.value = x`. A control that handed its value to its parent to store
 * would make every container responsible for knowing where its children belong,
 * and a group nested three deep would assemble the path from three partial
 * answers.
 */
export abstract class Control {
  constructor(
    protected readonly field: Field,
    protected readonly path: string,
    protected readonly doc: CvDocument,
    protected readonly upload?: UploadPhoto,
  ) {}

  protected get value(): unknown {
    return this.doc.get(this.path);
  }

  protected set value(next: unknown) {
    this.doc.set(this.path, next);
  }

  /** Abstract, so a control that cannot draw itself does not compile. */
  abstract render(): HTMLElement;

  /** The label, with the mark that says a field cannot be left out. */
  protected labelText(): string {
    return this.field.label + (this.field.required ? ' *' : '');
  }

  /** row wraps a control in its label and help text. */
  protected row(inner: Child, extra = ''): HTMLElement {
    return el('div', { class: `row${extra}` }, [
      el('label', { text: this.labelText() }),
      inner,
      this.field.help ? el('div', { class: 'help', text: this.field.help }) : null,
    ]);
  }
}

/**
 * The static side of a control: what an empty one of this kind looks like.
 *
 * On the class rather than in a helper, because it is the same knowledge as
 * rendering — whoever knows a group is drawn as its members' controls is who
 * knows an empty group is an object with those keys. Split apart, they drift,
 * and a new list entry renders as nothing.
 */
export interface ControlClass {
  new (field: Field, path: string, doc: CvDocument, upload?: UploadPhoto): Control;
  blank(field: Field): unknown;
}

class TextControl extends Control {
  render(): HTMLElement {
    const rich = this.field.kind === 'rich';
    const input = rich
      ? el('textarea', {
          rows: this.field.rows ?? 4,
          maxlength: this.field.max,
          placeholder: this.field.placeholder ?? '',
          oninput: (event: Event) => {
            this.value = (event.target as HTMLTextAreaElement).value;
          },
        })
      : el('input', {
          type: 'text',
          maxlength: this.field.max,
          placeholder: this.field.placeholder ?? '',
          oninput: (event: Event) => {
            this.value = (event.target as HTMLInputElement).value;
          },
        });
    input.value = String(this.value ?? '');
    return this.row(input);
  }

  static blank(): unknown {
    return '';
  }
}

class NumberControl extends Control {
  render(): HTMLElement {
    const input = el('input', {
      type: 'number',
      min: this.field.min,
      max: this.field.max,
      oninput: (event: Event) => {
        // An empty number field means "no value", not zero. Writing zero back
        // would turn a blank gauge into a gauge reading nothing out of ten,
        // which is a claim the author never made.
        const raw = (event.target as HTMLInputElement).value;
        this.value = raw === '' ? null : Number(raw);
      },
    });
    input.value = this.value === null || this.value === undefined ? '' : String(this.value);
    return this.row(input);
  }

  static blank(field: Field): unknown {
    return field.required ? (field.min ?? 0) : null;
  }
}

class BoolControl extends Control {
  render(): HTMLElement {
    const input = el('input', {
      type: 'checkbox',
      onchange: (event: Event) => {
        this.value = (event.target as HTMLInputElement).checked;
      },
    });
    input.checked = Boolean(this.value);
    // Its own shape: a checkbox under a label reads as a heading with a stray
    // box beneath it, where a checkbox beside one reads as a question.
    return el('div', { class: 'row bool' }, [input, el('label', { text: this.field.label })]);
  }

  static blank(): unknown {
    return false;
  }
}

class EnumControl extends Control {
  render(): HTMLElement {
    const select = el('select', {
      onchange: (event: Event) => {
        this.value = (event.target as HTMLSelectElement).value;
      },
    });
    for (const option of this.field.values ?? []) {
      const node = el('option', { value: option.value, text: option.label || option.value });
      if (String(this.value ?? '') === option.value) node.selected = true;
      select.appendChild(node);
    }
    return this.row(select);
  }

  static blank(field: Field): unknown {
    return field.default ?? '';
  }
}

class IconControl extends Control {
  /**
   * An icon is a name from the template's set, so the control offers what the
   * template actually ships. Typed free-hand it is a field where a misspelling
   * renders as a blank square, with nothing to say why.
   */
  render(): HTMLElement {
    const names = this.doc.icons;
    if (!names.length) {
      return new TextControl(this.field, this.path, this.doc).render();
    }
    const select = el('select', {
      onchange: (event: Event) => {
        this.value = (event.target as HTMLSelectElement).value;
      },
    });
    select.appendChild(el('option', { value: '', text: '— none —' }));
    for (const name of names) {
      const node = el('option', { value: name, text: name });
      if (this.value === name) node.selected = true;
      select.appendChild(node);
    }
    return this.row(select);
  }

  static blank(): unknown {
    return '';
  }
}

class GroupControl extends Control {
  render(): HTMLElement {
    return el(
      'div',
      {},
      (this.field.fields ?? []).map((child) =>
        build(child, Path.join(this.path, child.key), this.doc, this.upload).render(),
      ),
    );
  }

  static blank(field: Field): unknown {
    const out: Record<string, unknown> = {};
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
  render(): HTMLElement {
    const items = Array.isArray(this.value) ? (this.value as unknown[]) : [];
    const replaceWith = (next: unknown[]): void => {
      this.value = next;
    };

    const entries = items.map((_, index) => {
      const move = (to: number): void => {
        if (to < 0 || to >= items.length) return;
        const next = items.slice();
        // Spread rather than destructure: the removed element is typed as
        // possibly absent, and re-inserting the slice itself says the same
        // thing without asserting it is there.
        next.splice(to, 0, ...next.splice(index, 1));
        replaceWith(next);
      };
      return el('div', { class: 'entry' }, [
        el('div', { class: 'entry-bar' }, [
          el('button', { type: 'button', title: 'Move up', text: '↑',
            onclick: () => move(index - 1) }),
          el('button', { type: 'button', title: 'Move down', text: '↓',
            onclick: () => move(index + 1) }),
          el('button', { type: 'button', title: 'Remove', text: '✕',
            onclick: () => replaceWith(items.filter((__, i) => i !== index)) }),
        ]),
        this.field.of
          ? build(this.field.of, `${this.path}[${index}]`, this.doc, this.upload).render()
          : null,
      ]);
    });

    return el('fieldset', { class: 'list' }, [
      el('legend', { text: this.field.label }),
      ...entries,
      el('button', {
        type: 'button',
        text: this.field.addLabel || `Add ${this.field.label.toLowerCase()}`,
        onclick: () => {
          replaceWith(this.field.of ? items.concat([blankOf(this.field.of)]) : items);
        },
      }),
    ]);
  }

  static blank(field: Field): unknown {
    // A list with a minimum starts at that minimum: a `chips` section whose
    // groups list requires one is invalid empty, and the server would refuse
    // the very document this control just created.
    const count = field.min ?? 0;
    const out: unknown[] = [];
    if (field.of) for (let i = 0; i < count; i++) out.push(blankOf(field.of));
    return out;
  }
}

/**
 * PhotoControl uploads through its own route rather than into the document.
 *
 * A portrait is 200 kB of binary; carrying it as base64 inside the CV would put
 * a third more than that through every save, on every keystroke's worth of
 * autosave. It goes once, as bytes, and the document keeps its filename.
 */
class PhotoControl extends Control {
  render(): HTMLElement {
    const current = this.value;
    const input = el('input', {
      type: 'file',
      accept: 'image/png,image/jpeg,image/webp',
      onchange: (event: Event) => {
        const file = (event.target as HTMLInputElement).files?.[0];
        if (file) this.upload?.(file);
      },
    });
    return this.row(
      el('div', { class: 'photo' }, [
        input,
        current ? el('span', { class: 'help', text: `current: ${String(current)}` }) : null,
      ]),
    );
  }

  static blank(): unknown {
    return '';
  }
}

/**
 * registry maps a field kind to its control.
 *
 * Typed as a total map over `FieldKind`, which is the part a switch could not
 * give: adding a kind to the vocabulary and forgetting to implement it is a
 * compile error here, rather than a field that silently fails to render.
 */
const registry: Record<FieldKind, ControlClass> = {
  text: TextControl,
  rich: TextControl,
  number: NumberControl,
  bool: BoolControl,
  enum: EnumControl,
  icon: IconControl,
  group: GroupControl,
  list: ListControl,
  photo: PhotoControl,
};

/** build makes the control for a field. */
export function build(
  field: Field,
  path: string,
  doc: CvDocument,
  upload?: UploadPhoto,
): Control {
  const Kind = registry[field.kind];
  if (!Kind) {
    // Reachable only from a server newer than this interface. Said loudly and
    // in place: a field nobody can edit is a field whose contents cannot be
    // corrected, and silence would hide that behind a form that looks complete.
    return new MissingControl(field, path, doc);
  }
  return new Kind(field, path, doc, upload);
}

class MissingControl extends Control {
  render(): HTMLElement {
    return el('div', { class: 'row' }, [
      el('label', { text: this.field.label }),
      el('div', {
        class: 'help error',
        text: `no control for field kind "${this.field.kind}" — this editor is older than the engine`,
      }),
    ]);
  }

  static blank(): unknown {
    return null;
  }
}

/** blankOf is an empty value of a field's kind. */
export function blankOf(field: Field): unknown {
  return registry[field.kind]?.blank(field) ?? null;
}

/** panel is a collapsible block of the form. */
export function panel(
  title: string,
  tag: string | null,
  children: Child[],
  open = false,
): HTMLElement {
  return el('details', { class: 'panel', open: open || undefined }, [
    el('summary', {}, [
      el('span', { class: 'grow', text: title }),
      tag ? el('span', { class: 'tag', text: tag }) : null,
    ]),
    el('div', { class: 'panel-body' }, children),
  ]);
}
