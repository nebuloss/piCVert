// document.js — the CV in the browser, and the only copy of it.
//
// ONE DOCUMENT, and everything else observes it. The form writes to it, the
// preview and the autosave read it, and none of the three knows the others
// exist. A second copy — a control holding its own value, a panel caching a
// section — is how an editor comes to show one thing and save another, and the
// bug is invisible until someone reloads.

import { Emitter } from '../lib/emitter.js';

/**
 * Path addresses one field, in the SAME notation the server uses for validation
 * errors and history entries:
 *
 *   content.identity.name
 *   content.sections[2].items[0].bullets[1]
 *
 * Shared notation is the point. A message about `content.sections[2].role` is a
 * message naming something the interface can find and scroll to, rather than a
 * string only the server understands.
 */
export class Path {
  static steps(path) {
    const out = [];
    for (const part of String(path).split('.')) {
      if (!part) continue;
      const name = part.replace(/\[\d+\]/g, '');
      if (name) out.push(name);
      for (const index of part.match(/\[(\d+)\]/g) ?? []) {
        out.push(Number(index.slice(1, -1)));
      }
    }
    return out;
  }

  static join(parent, key) {
    if (!parent) return String(key);
    return typeof key === 'number' ? `${parent}[${key}]` : `${parent}.${key}`;
  }

  static read(root, path) {
    let cursor = root;
    for (const step of Path.steps(path)) {
      if (cursor === null || cursor === undefined) return undefined;
      cursor = cursor[step];
    }
    return cursor;
  }

  static write(root, path, value) {
    const steps = Path.steps(path);
    let cursor = root;
    for (let i = 0; i < steps.length - 1; i++) {
      const step = steps[i];
      if (cursor[step] === null || cursor[step] === undefined) {
        // The shape of what is missing is decided by what comes next: a number
        // needs an array to index into, a name needs an object. Guessing wrong
        // produces an object with keys "0", "1" that validates as neither.
        cursor[step] = typeof steps[i + 1] === 'number' ? [] : {};
      }
      cursor = cursor[step];
    }
    cursor[steps.at(-1)] = value;
  }
}

/**
 * CvDocument is the document, and the subject everything observes.
 *
 * Two events, and the distinction is load-bearing:
 *
 *   "value"  a field's contents changed. The form must NOT redraw — the field
 *            being typed into would lose its cursor on every keystroke.
 *   "shape"  the document's structure changed: a section added, an entry
 *            removed, a template switched. The form MUST redraw, because the
 *            controls that exist are no longer the right ones.
 *
 * They were one event once, and the editor stole the caret after every letter.
 */
export class CvDocument extends Emitter {
  #doc;

  constructor(doc) {
    super();
    this.#doc = doc;
  }

  get raw() { return this.#doc; }

  /** replace swaps the whole document — a template change, a new photo. */
  replace(doc) {
    this.#doc = doc;
    this.emit('shape', this);
  }

  /**
   * adopt takes the stored document back without telling anyone.
   *
   * What a save returns is the document as STORED: the template pinned to a
   * UUID, the time stamped. Keeping our own copy instead would send those back
   * stale on the next save. But this is not a change the person made, and
   * emitting "shape" for it would redraw the form on every save — the editor
   * twitching once a second while someone types.
   */
  adopt(doc) {
    this.#doc = doc;
  }

  get(path) { return Path.read(this.#doc, path); }

  /**
   * set writes a value and says which kind of change it was.
   *
   * The kind is DERIVED, not passed: a caller deciding "this one is structural"
   * is a caller that will one day decide wrong, and the failure — a form that
   * does not redraw after a section is added — looks like the add button being
   * broken.
   */
  set(path, value) {
    const before = this.get(path);
    Path.write(this.#doc, path, value);
    const structural = isContainer(value) || isContainer(before);
    this.emit('value', { path, value });
    if (structural) this.emit('shape', this);
  }

  /** The name on the CV, which several panels need and none should dig for. */
  get name() {
    return String(this.get('content.identity.name') ?? '').trim();
  }

  get sections() {
    const list = this.get('content.sections');
    return Array.isArray(list) ? list : [];
  }

  /** setSections replaces the whole list: adding, removing and reordering. */
  setSections(sections) {
    Path.write(this.#doc, 'content.sections', sections);
    this.emit('shape', this);
  }
}

function isContainer(value) {
  return Array.isArray(value) || (value !== null && typeof value === 'object');
}

/**
 * Fit is what the engine said about the page, as something the interface can
 * show without re-deriving it.
 *
 * A value object rather than the raw response, because "does it fit" is asked
 * in three places and each of them reading `fit.ok === false` off a bare object
 * is three chances to read a field that was renamed.
 */
export class Fit {
  constructor(report = {}) {
    this.ok = report.ok !== false;
    this.summary = report.summary ?? '';
    this.margins = report.margins ?? {};
    this.over = report.over ?? [];
    this.spacing = report.spacing ?? [1, 1];
    this.text = report.text ?? 1;
  }

  /** The room left in each column, rounded to something worth reading. */
  get columns() {
    return Object.entries(this.margins)
      .map(([name, room]) => ({ name, room: Math.round(room) }));
  }

  /**
   * tight is true when the engine had to close the page up a long way to make
   * it hold — which is a CV that is too long, wearing a page that looks fine.
   */
  get tight() {
    return this.ok && Math.min(...this.spacing) < 0.75;
  }
}
