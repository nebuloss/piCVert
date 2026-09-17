// emitter.js — the observer, once.
//
// # WHY THE ENGINE HAS NO OBSERVERS AND THIS DOES
//
// docs/DESIGN.md rules events out of the layout engine, and is right to: a
// layout is a pure function of a document and a theme, computed once, and an
// observer there would be indirection buying nothing.
//
// A browser is the opposite case. Typing changes a document; a preview, an
// autosave and a fit report all have to react; they are asynchronous, they run
// at different rates, and none of them is interested in the others. Wiring them
// directly means the control that handles a keystroke has to know about all
// three — and the fourth thing that needs to react is then a change to the
// typing code, which is how an editor ends up with the save logic inside the
// text field.
//
// So: one subject, many observers, and the thing that changes knows about none
// of them.

export class Emitter {
  #listeners = new Map();

  /**
   * on registers a listener and returns the function that removes it.
   *
   * Returning the remover rather than offering `off(name, fn)` means the caller
   * never has to keep the handle it would need to unregister — which is how a
   * listener outlives the thing that registered it and keeps a dead panel
   * updating.
   */
  on(name, listener) {
    if (!this.#listeners.has(name)) this.#listeners.set(name, new Set());
    this.#listeners.get(name).add(listener);
    return () => this.#listeners.get(name)?.delete(listener);
  }

  /**
   * emit calls every listener, and lets none of them stop the others.
   *
   * A listener that throws is a fault in that listener. Left to propagate it
   * would abandon the ones after it in the set — so a broken fit display would
   * silently stop the autosave, and what the person sees is an editor that no
   * longer saves for no visible reason.
   */
  emit(name, payload) {
    for (const listener of this.#listeners.get(name) ?? []) {
      try {
        listener(payload);
      } catch (error) {
        console.error(`listener for "${name}" failed:`, error);
      }
    }
  }
}

/**
 * Debounced runs a function after things have stopped happening.
 *
 * Typing produces an event per keystroke and the work worth doing is per pause,
 * not per letter. Its own class because the naive version — a bare
 * `setTimeout` — has three bugs that all appeared here: a run already in flight
 * being started again, the change that arrived during that run being lost, and
 * a pending run firing after the thing it updates is gone.
 */
export class Debounced {
  #timer = 0;
  #running = false;
  #again = false;

  constructor(task, delay) {
    this.task = task;
    this.delay = delay;
  }

  schedule() {
    clearTimeout(this.#timer);
    this.#timer = setTimeout(() => this.run(), this.delay);
  }

  async run() {
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
        this.run();
      }
    }
  }

  cancel() {
    clearTimeout(this.#timer);
    this.#again = false;
  }
}
