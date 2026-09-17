/**
 * emitter.ts — the observer, once.
 *
 * # WHY THE ENGINE HAS NO OBSERVERS AND THIS DOES
 *
 * docs/DESIGN.md rules events out of the layout engine, and is right to: a
 * layout is a pure function of a document and a theme, computed once, and an
 * observer there would be indirection buying nothing.
 *
 * A browser is the opposite case. Typing changes a document; a preview, an
 * autosave and a fit report all have to react; they are asynchronous, they run
 * at different rates, and none of them is interested in the others. Wiring them
 * directly means the control that handles a keystroke has to know about all
 * three — and the fourth thing that needs to react is then a change to the
 * typing code, which is how an editor ends up with the save logic inside the
 * text field.
 *
 * So: one subject, many observers, and the thing that changes knows about none
 * of them.
 */

/** Removes a listener. Returned by `on`, so the caller need keep nothing. */
export type Unsubscribe = () => void;

/**
 * Emitter is typed by an EVENT MAP: `{ value: ValueChange; shape: CvDocument }`.
 *
 * That is what makes it worth having in TypeScript rather than a bare callback
 * list. `on('shpae', …)` is a compile error, and the listener's argument is
 * known without a cast — so a payload that gains a field is a compile error at
 * every listener that needed it, instead of `undefined` at runtime.
 */
export class Emitter<Events extends Record<string, unknown>> {
  readonly #listeners = new Map<keyof Events, Set<(payload: never) => void>>();

  /**
   * on registers a listener and returns the function that removes it.
   *
   * Returning the remover rather than offering `off(name, fn)` means the caller
   * never has to keep the handle it would need to unregister — which is how a
   * listener outlives the thing that registered it and keeps a dead panel
   * updating.
   */
  on<K extends keyof Events>(name: K, listener: (payload: Events[K]) => void): Unsubscribe {
    let set = this.#listeners.get(name);
    if (!set) {
      set = new Set();
      this.#listeners.set(name, set);
    }
    set.add(listener as (payload: never) => void);
    return () => {
      this.#listeners.get(name)?.delete(listener as (payload: never) => void);
    };
  }

  /**
   * emit calls every listener, and lets none of them stop the others.
   *
   * A listener that throws is a fault in that listener. Left to propagate it
   * would abandon the ones after it in the set — so a broken fit display would
   * silently stop the autosave, and what the person sees is an editor that no
   * longer saves for no visible reason.
   */
  protected emit<K extends keyof Events>(name: K, payload: Events[K]): void {
    const set = this.#listeners.get(name);
    if (!set) return;
    for (const listener of set) {
      try {
        (listener as (value: Events[K]) => void)(payload);
      } catch (error) {
        console.error(`listener for "${String(name)}" failed:`, error);
      }
    }
  }
}

/**
 * Debounced runs a task after things have stopped happening.
 *
 * Typing produces an event per keystroke and the work worth doing is per pause,
 * not per letter. Its own class because the naive version — a bare `setTimeout`
 * — has three faults that all appeared here: a run already in flight being
 * started again, the change that arrived during that run being lost, and a
 * pending run firing after the thing it updates is gone.
 */
export class Debounced {
  #timer = 0;
  #running = false;
  #again = false;

  constructor(
    private readonly task: () => Promise<void> | void,
    private readonly delay: number,
  ) {}

  schedule(): void {
    clearTimeout(this.#timer);
    this.#timer = window.setTimeout(() => void this.run(), this.delay);
  }

  async run(): Promise<void> {
    // Already in flight: remember that the world moved, and run once more when
    // it finishes. Running concurrently would let the older answer land last.
    if (this.#running) {
      this.#again = true;
      return;
    }
    this.#running = true;
    try {
      await this.task();
    } finally {
      this.#running = false;
      if (this.#again) {
        this.#again = false;
        void this.run();
      }
    }
  }

  cancel(): void {
    clearTimeout(this.#timer);
    this.#again = false;
  }
}
