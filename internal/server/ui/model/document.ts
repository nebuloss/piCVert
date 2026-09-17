/**
 * document.ts — the CV in the browser, and the only copy of it.
 *
 * ONE DOCUMENT, and everything else observes it. The form writes to it, the
 * preview and the autosave read it, and none of the three knows the others
 * exist. A second copy — a control holding its own value, a panel caching a
 * section — is how an editor comes to show one thing and save another, and the
 * fault is invisible until somebody reloads.
 */

import { Emitter } from '../lib/emitter.ts';
import type { Cv, CvSection, FitReportData } from './api.ts';

/** A step of a path: a key, or an index into a list. */
type Step = string | number;

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
  static steps(path: string): Step[] {
    const out: Step[] = [];
    for (const part of path.split('.')) {
      if (!part) continue;
      const name = part.replace(/\[\d+\]/g, '');
      if (name) out.push(name);
      for (const index of part.match(/\[(\d+)\]/g) ?? []) {
        out.push(Number(index.slice(1, -1)));
      }
    }
    return out;
  }

  static join(parent: string, key: Step): string {
    if (!parent) return String(key);
    return typeof key === 'number' ? `${parent}[${key}]` : `${parent}.${key}`;
  }

  static read(root: unknown, path: string): unknown {
    let cursor: unknown = root;
    for (const step of Path.steps(path)) {
      if (cursor === null || cursor === undefined) return undefined;
      cursor = (cursor as Record<Step, unknown>)[step];
    }
    return cursor;
  }

  static write(root: unknown, path: string, value: unknown): void {
    const steps = Path.steps(path);
    const last = steps.at(-1);
    if (last === undefined) return;

    let cursor = root as Record<Step, unknown>;
    for (let i = 0; i < steps.length - 1; i++) {
      const step = steps[i]!;
      if (cursor[step] === null || cursor[step] === undefined) {
        // The shape of what is missing is decided by what comes NEXT: a number
        // needs an array to index into, a name needs an object. Guessing wrong
        // produces an object with keys "0" and "1" that validates as neither.
        cursor[step] = typeof steps[i + 1] === 'number' ? [] : {};
      }
      cursor = cursor[step] as Record<Step, unknown>;
    }
    cursor[last] = value;
  }
}

/** What a value change reports. */
export interface ValueChange {
  path: string;
  value: unknown;
}

/**
 * The two events, and the distinction is load-bearing:
 *
 *   value   a field's contents changed. The form must NOT redraw — the field
 *           being typed into would lose its cursor on every keystroke.
 *   shape   the structure changed: a section added, an entry removed, a
 *           template switched. The form MUST redraw, because the controls that
 *           exist are no longer the right ones.
 *
 * They were one event once, and the editor stole the caret after every letter.
 */
export interface DocumentEvents {
  value: ValueChange;
  shape: CvDocument;
  [key: string]: unknown;
}

export class CvDocument extends Emitter<DocumentEvents> {
  #doc: Cv;

  /**
   * The icon names the template ships, hung here because every control already
   * receives the document and threading a second argument through the composite
   * for one control is worse.
   */
  icons: string[] = [];

  constructor(doc: Cv) {
    super();
    this.#doc = doc;
  }

  get raw(): Cv {
    return this.#doc;
  }

  /** replace swaps the whole document — a template change, a new photo. */
  replace(doc: Cv): void {
    this.#doc = doc;
    this.emit('shape', this);
  }

  /**
   * adopt takes the stored document back without telling anyone.
   *
   * What a save returns is the document as STORED: the template pinned to a
   * UUID, the time stamped. Keeping our own copy instead would send those back
   * stale on the next save. But this is not a change the person made, and
   * emitting `shape` for it would redraw the form on every save — the editor
   * twitching once a second while somebody types.
   */
  adopt(doc: Cv): void {
    this.#doc = doc;
  }

  get(path: string): unknown {
    return Path.read(this.#doc, path);
  }

  /**
   * set writes a value and says which kind of change it was.
   *
   * The kind is DERIVED, not passed: a caller deciding "this one is structural"
   * is a caller that will one day decide wrong, and the failure — a form that
   * does not redraw after a section is added — looks like the add button being
   * broken.
   */
  set(path: string, value: unknown): void {
    const before = this.get(path);
    Path.write(this.#doc, path, value);
    this.emit('value', { path, value });
    if (isContainer(value) || isContainer(before)) this.emit('shape', this);
  }

  /** The name on the CV, which several panels need and none should dig for. */
  get name(): string {
    return (this.#doc.content?.identity?.name ?? '').trim();
  }

  get sections(): CvSection[] {
    return this.#doc.content?.sections ?? [];
  }

  /** setSections replaces the whole list: adding, removing and reordering. */
  setSections(sections: CvSection[]): void {
    this.#doc.content.sections = sections;
    this.emit('shape', this);
  }
}

function isContainer(value: unknown): boolean {
  return Array.isArray(value) || (value !== null && typeof value === 'object');
}

/**
 * Fit is what the engine said about the page, as something the interface can
 * show without re-deriving it.
 *
 * A value object rather than the raw response, because "does it fit" is asked
 * in three places and each of them reading `fit.ok === false` off a bare record
 * is three chances to read a field that was renamed.
 */
export class Fit {
  readonly ok: boolean;
  readonly summary: string;
  readonly margins: Record<string, number>;
  readonly over: string[];
  readonly spacing: [number, number];
  readonly text: number;

  constructor(report?: Partial<FitReportData>) {
    this.ok = report?.ok !== false;
    this.summary = report?.summary ?? '';
    this.margins = report?.margins ?? {};
    this.over = report?.over ?? [];
    this.spacing = report?.spacing ?? [1, 1];
    this.text = report?.text ?? 1;
  }

  /** The room left in each column, rounded to something worth reading. */
  get columns(): Array<{ name: string; room: number }> {
    return Object.entries(this.margins).map(([name, room]) => ({
      name,
      room: Math.round(room),
    }));
  }

  /**
   * tight is true when the engine had to close the page up a long way to make
   * it hold — which is a CV that is too long, wearing a page that looks fine.
   */
  get tight(): boolean {
    return this.ok && Math.min(...this.spacing) < 0.75;
  }
}
