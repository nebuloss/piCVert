/**
 * autosave.ts — every change is saved, and none is lost.
 *
 * # NO TIMER
 *
 * This waited 900 ms after the last keystroke before saving. That window is
 * exactly as long as the work somebody loses when a tab closes or a laptop
 * lid comes down, and there is no reason to hold it: a save is one request,
 * and the thing that must not happen is not "too many requests" but "a change
 * that was never sent".
 *
 * So a change starts a save immediately.
 *
 * # AND YET NOT A REQUEST PER LETTER
 *
 * The coalescing comes from SERIALISING rather than from waiting. One save is
 * in flight at a time; changes arriving during it mark the document dirty, and
 * when it lands the next save goes out at once — carrying every change made
 * meanwhile, because what is sent is the document as it now stands rather than
 * a diff of one edit.
 *
 * The effect is that the save rate settles at one per round trip. A fast server
 * saves nearly every keystroke; a slow one batches more into each save. Nobody
 * has to choose a number, and the number chosen for them is never wrong for
 * their connection.
 *
 * Serialising is also what stops two saves landing out of order, with the older
 * one last — silent, and only noticed on a reload.
 */

import { Emitter } from '../lib/emitter.ts';
import { isConflict } from '../lib/http.ts';
import type { Tagged } from '../lib/http.ts';
import type { Cv, SaveAnswer } from '../model/api.ts';
import type { CvDocument } from '../model/document.ts';

/** The four states a save can be in, as the indicator shows them. */
export type SaveState = 'dirty' | 'saving' | 'saved' | 'error' | 'conflict';

export interface StateChange {
  kind: SaveState;
  text: string;
}

export interface AutosaveEvents {
  state: StateChange;
  /**
   * A save landed. It carries the revision the document is now at as well as
   * the document itself, because the next save is checked against THIS one
   * rather than against whatever was first loaded — a client that kept the
   * original would conflict with itself on its second keystroke.
   */
  saved: Tagged<SaveAnswer>;
  /**
   * Somebody else changed this CV while it was open. Its own event, because
   * the answer is a question for the person rather than anything the autosave
   * can decide.
   */
  conflict: void;
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
  #retryTimer = 0;
  #running = false;
  #dirty = false;
  #onUnload?: (event: BeforeUnloadEvent) => void;
  #stopped = false;
  /** Whether the last attempt failed, which is the only thing that waits. */
  #failed = false;

  constructor(
    private readonly doc: CvDocument,
    private readonly save: (doc: Cv) => Promise<Tagged<SaveAnswer>>,
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

  /** schedule marks the document changed and saves it, now. */
  schedule(): void {
    // Stopped means a conflict is waiting to be answered. Carrying on saving
    // would be the interface deciding, on the person's behalf, to overwrite
    // somebody else's work.
    if (this.#stopped) return;
    this.#dirty = true;
    void this.run();
  }

  /** flush saves anything outstanding, for leaving or switching language. */
  flush(): Promise<void> {
    clearTimeout(this.#retryTimer);
    return this.run();
  }

  async run(): Promise<void> {
    // Already saving: the change is remembered, and goes out the moment the
    // current request lands. This is the whole of the coalescing.
    if (this.#running || !this.#dirty) return;
    this.#running = true;
    // Cleared BEFORE the request, not after: a change arriving while this one
    // is in flight must mark the document dirty again, and clearing afterwards
    // would wipe that mark and lose the change.
    this.#dirty = false;
    this.emit('state', { kind: 'saving', text: 'saving…' });

    try {
      const answer = await this.save(this.doc.raw);
      this.#failed = false;
      this.emit('saved', answer);
      this.emit('state', { kind: 'saved', text: 'saved' });
    } catch (error) {
      if (isConflict(error)) {
        // NOT retried. The document on the server is not the one this was
        // built from, so every retry conflicts exactly as the first did — and
        // a save that cannot succeed, repeated every two seconds, is an
        // editor that looks broken while quietly asking to overwrite somebody.
        this.#stopped = true;
        this.emit('state', {
          kind: 'conflict',
          text: 'changed by someone else',
        });
        this.emit('conflict', undefined);
        return;
      }
      this.#dirty = true;
      this.#failed = true;
      this.emit('state', {
        kind: 'error',
        text: error instanceof Error ? error.message : String(error),
      });
    } finally {
      this.#running = false;
      if (this.#dirty) {
        // Straight on, with whatever was typed during the request just
        // finished. The delay is only for a save that FAILED — retrying a
        // dropped connection as fast as the browser allows would be a denial
        // of service aimed at oneself.
        if (this.#failed) window.setTimeout(() => void this.run(), this.retry);
        else void this.run();
      }
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
    this.#stopped = true;
    clearTimeout(this.#retryTimer);
    this.#dirty = false;
    if (this.#onUnload) window.removeEventListener('beforeunload', this.#onUnload);
  }
}
