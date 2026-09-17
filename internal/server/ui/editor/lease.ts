/**
 * lease.ts — holding the right to edit, and noticing when it is lost.
 *
 * One editor at a time per document. The server grants a lease; this keeps it
 * alive, gives it up when the tab closes, and says so when it goes away.
 *
 * # TWO WAYS TO LOSE IT, AND THEY LOOK THE SAME FROM HERE
 *
 * The heartbeat can be refused because the lease lapsed — this tab was asleep,
 * or the network went — or because somebody else has taken it since. Either
 * way this tab may no longer save, and the difference is of no use to the
 * person: what they need to know is that they must stop.
 *
 * # WHY THE HEARTBEAT IS NOT THE SAVE
 *
 * Saving already happens on every change, and it renews the lease as a side
 * effect. But somebody reading their own CV, or thinking about a sentence,
 * types nothing for minutes — and a lease that only a save could renew would
 * be taken away from them mid-thought. The heartbeat says "I am still here";
 * the save says "I am still working". The server keeps two clocks because
 * those are two different facts.
 */

import { Emitter } from '../lib/emitter.ts';
import type { Api } from './api.ts';

/** What the server says about a request for the lease. */
export interface LeaseState {
  held: boolean;
  editor: string;
  heartbeatMs: number;
  untilMs?: number;
  heldBy?: string;
  freeInMs?: number;
}

export interface LeaseEvents {
  /** The lease was lost: somebody else has it, or it lapsed. */
  lost: void;
  /** It was asked for and refused — the state carries who holds it. */
  refused: LeaseState;
  /** It was granted, having been refused before. */
  granted: void;
  [key: string]: unknown;
}

export class Lease extends Emitter<LeaseEvents> {
  #holder = '';
  #timer = 0;
  #held = false;
  #released = false;

  constructor(
    private readonly api: Api,
    private readonly lang: string,
  ) {
    super();
  }

  get holder(): string { return this.#holder; }
  get held(): boolean { return this.#held; }

  /** ask requests the lease, minting an identity on the first attempt. */
  async ask(): Promise<LeaseState> {
    const state = await this.api.lease(this.lang, this.#holder, false);
    // The server mints the identity, and this tab keeps it. A client that
    // chose its own could name itself whatever the current holder is called
    // and take the lease from them.
    this.#holder = state.editor;
    const was = this.#held;
    this.#held = state.held;
    if (state.held) {
      this.start(state.heartbeatMs);
      if (!was) this.emit('granted', undefined);
    } else {
      this.emit('refused', state);
    }
    return state;
  }

  /** start begins the heartbeat. */
  private start(everyMs: number): void {
    clearInterval(this.#timer);
    this.#timer = window.setInterval(() => void this.beat(), everyMs);
  }

  private async beat(): Promise<void> {
    if (this.#released) return;
    try {
      const state = await this.api.lease(this.lang, this.#holder, true);
      if (!state.held) this.drop();
    } catch {
      // A failed heartbeat is not a lost lease: the connection may be down for
      // a moment, and the server's timeout is three beats long precisely so
      // that two can be missed. Announcing a loss here would throw somebody
      // out of the editor over a dropped packet.
    }
  }

  private drop(): void {
    if (!this.#held) return;
    this.#held = false;
    clearInterval(this.#timer);
    this.emit('lost', undefined);
  }

  /**
   * waitForIt polls until the lease comes free, and calls back each time with
   * how things stand.
   *
   * Polling rather than a push: a socket for a page somebody will look at for
   * thirty seconds is a connection, a reconnection policy and a proxy
   * configuration, to avoid one small request every few seconds.
   */
  async waitForIt(onState: (state: LeaseState) => void, everyMs = 3000): Promise<void> {
    for (;;) {
      if (this.#released) return;
      let state: LeaseState;
      try {
        state = await this.ask();
      } catch {
        // Keep trying. Whoever is waiting has nothing else to do, and a
        // service that is briefly unreachable is not a reason to give up on
        // the CV.
        await pause(everyMs);
        continue;
      }
      onState(state);
      if (state.held) return;
      await pause(everyMs);
    }
  }

  /**
   * release gives the lease up, for a tab that is closing.
   *
   * sendBeacon rather than fetch: a browser is free to cancel an ordinary
   * request as the page goes away, and this one is sent at exactly that
   * moment. Nothing depends on it arriving — the lease lapses on its own in
   * under a minute — but arriving is what lets the next person in at once
   * rather than after the timeout.
   */
  release(): void {
    this.#released = true;
    clearInterval(this.#timer);
    if (!this.#holder) return;
    this.api.releaseLease(this.lang, this.#holder);
  }
}

function pause(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}
