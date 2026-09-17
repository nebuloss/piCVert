/**
 * autosave.ts — saving what was typed, without saying so every keystroke.
 *
 * Two problems, and they are not the same one:
 *
 *   COALESCING   a change per letter must not be a request per letter
 *   SERIALISING  two saves in flight can land out of order, and the older one
 *                landing last overwrites the newer — silently, and the person
 *                only finds out when they reload
 *
 * A debounce solves the first and not the second, which is why this queue
 * exists rather than a timer.
 */

import { Emitter } from '../lib/emitter.ts';
import type { Cv, SaveAnswer } from '../model/api.ts';
import type { CvDocument } from '../model/document.ts';

/** The four states a save can be in, as the indicator shows them. */
export type SaveState = 'dirty' | 'saving' | 'saved' | 'error';

export interface StateChange {
  kind: SaveState;
  text: string;
}

export interface AutosaveEvents {
  state: StateChange;
  saved: SaveAnswer;
  [key: string]: unknown;
}

/**
 * Autosave observes the document and writes it back.
 *
 * It never drops a change. A failed save leaves the queue dirty so the next
 * change retries it, because the alternative — reporting the error and moving
 * on — loses whatever was typed while the connection was down, and that is the
 * one thing an editor must not do.
 */
export class Autosave extends Emitter<AutosaveEvents> {
  #timer = 0;
  #running = false;
  #dirty = false;
  #onUnload?: (event: BeforeUnloadEvent) => void;

  constructor(
    private readonly doc: CvDocument,
    private readonly save: (doc: Cv) => Promise<SaveAnswer>,
    private readonly delay = 900,
    private readonly retry = 2500,
  ) {
    super();
    // Both kinds of change are saved. The distinction between them is about
    // whether the FORM redraws, which is no business of this.
    doc.on('value', () => this.schedule());
    doc.on('shape', () => this.schedule());
  }

  get pending(): boolean {
    return this.#dirty || this.#running;
  }

  schedule(): void {
    this.#dirty = true;
    this.emit('state', { kind: 'dirty', text: 'unsaved' });
    clearTimeout(this.#timer);
    this.#timer = window.setTimeout(() => void this.run(), this.delay);
  }

  /** flush saves now, for leaving the page or switching language. */
  flush(): Promise<void> {
    clearTimeout(this.#timer);
    return this.run();
  }

  async run(): Promise<void> {
    if (this.#running || !this.#dirty) return;
    this.#running = true;
    // Cleared BEFORE the request, not after: a change arriving while this one
    // is in flight must mark the document dirty again, and clearing afterwards
    // would wipe that mark and lose the change.
    this.#dirty = false;
    this.emit('state', { kind: 'saving', text: 'saving…' });

    try {
      const answer = await this.save(this.doc.raw);
      this.emit('saved', answer);
      this.emit('state', { kind: 'saved', text: 'saved' });
    } catch (error) {
      this.#dirty = true;
      this.emit('state', {
        kind: 'error',
        text: error instanceof Error ? error.message : String(error),
      });
    } finally {
      this.#running = false;
      if (this.#dirty) window.setTimeout(() => void this.run(), this.retry);
    }
  }

  /**
   * guard warns before leaving with work in flight.
   *
   * The window between the last keystroke and the save landing is about a
   * second, and closing a tab inside it loses that second's typing with no
   * trace. The browser's own dialog is the only thing that can interrupt a
   * navigation.
   */
  guard(): this {
    this.#onUnload = (event: BeforeUnloadEvent): void => {
      if (!this.pending) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', this.#onUnload);
    return this;
  }

  /**
   * release drops the guard and any pending save.
   *
   * For when the document is deliberately gone: after a deletion there is
   * nothing left to save, and a guard still in place would warn the person
   * about losing work they just asked to destroy.
   */
  release(): void {
    clearTimeout(this.#timer);
    this.#dirty = false;
    if (this.#onUnload) window.removeEventListener('beforeunload', this.#onUnload);
  }
}
