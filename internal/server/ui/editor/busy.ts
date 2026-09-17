/**
 * busy.ts — what a second person sees.
 *
 * # WHY A NOTICE AND NOT A WAITING PAGE
 *
 * A page that says "waiting…" is a page somebody stares at, and most of the
 * time what they actually wanted was to LOOK at the CV rather than change it.
 * Making them wait for a lease they did not need is making them wait for
 * nothing.
 *
 * So the notice offers both, and picks one if it is ignored: after a few
 * seconds it takes them to the CV, read-only. That is the safe default in the
 * sense that matters — it cannot lose anybody's work, and it is where somebody
 * who wandered in was going anyway.
 *
 * ANY interaction cancels the countdown. Somebody who is reading the notice is
 * somebody deciding, and a page that navigates out from under a person mid
 * sentence is worse than one that waits.
 */

import { el, replace } from '../lib/dom.ts';
import type { LeaseState } from './lease.ts';

/** How long the notice waits before taking an undecided person to the viewer. */
const FALLBACK_SECONDS = 10;

export class BusyNotice {
  #countdown = 0;
  #left = FALLBACK_SECONDS;
  #decided = false;

  constructor(
    private readonly dialog: HTMLDialogElement,
    private readonly body: HTMLElement,
    private readonly viewerUrl: string,
  ) {}

  /**
   * show tells somebody the CV is being edited, and starts the countdown.
   *
   * onWait is called if they choose to wait; the caller is what actually polls,
   * because it is the caller that knows what to do when the lease arrives.
   */
  show(state: LeaseState, onWait: () => void): void {
    this.#decided = false;
    this.#left = FALLBACK_SECONDS;

    const who = state.heldBy ? `“${state.heldBy}” is being edited` : 'This CV is being edited';
    const countdown = el('strong', { text: String(this.#left) });

    const wait = el('button', {
      type: 'button', class: 'primary', text: 'Wait for it',
      onclick: () => {
        this.decide();
        this.waiting();
        onWait();
      },
    });
    const view = el('button', {
      type: 'button', text: 'Just view it',
      onclick: () => {
        this.decide();
        location.href = this.viewerUrl;
      },
    });

    replace(this.body, [
      el('p', { text:
        `${who} in another window. Only one person can change a CV at a time, ` +
        `so that two people cannot overwrite each other.` }),
      el('p', { class: 'muted' }, [
        'Taking you to view it in ', countdown, ' seconds…',
      ]),
      el('div', { class: 'choices' }, [wait, view]),
    ]);

    if (!this.dialog.open) this.dialog.showModal();
    // Any interaction at all counts as deciding. Somebody reading the notice is
    // somebody thinking about it, and navigating out from under them is the one
    // thing this must not do.
    for (const event of ['pointerdown', 'keydown']) {
      this.dialog.addEventListener(event, () => this.decide(), { once: true });
    }

    clearInterval(this.#countdown);
    this.#countdown = window.setInterval(() => {
      if (this.#decided) {
        clearInterval(this.#countdown);
        // The countdown is gone, so the line promising it would be a lie.
        countdown.parentElement?.replaceChildren('Waiting for it to be free…');
        return;
      }
      this.#left -= 1;
      countdown.textContent = String(this.#left);
      if (this.#left <= 0) {
        clearInterval(this.#countdown);
        location.href = this.viewerUrl;
      }
    }, 1000);
  }

  /** decide stops the countdown: the person is engaged with the notice. */
  private decide(): void {
    this.#decided = true;
  }

  /** waiting replaces the choices with a report on the wait. */
  private waiting(): void {
    replace(this.body, [
      el('p', { text: 'Waiting for the other window to finish…' }),
      el('p', { class: 'muted', id: 'busy-progress', text:
        'It will free up on its own if they close the tab or leave it alone.' }),
      el('div', { class: 'choices' }, [
        el('button', {
          type: 'button', text: 'Stop waiting and just view it',
          onclick: () => { location.href = this.viewerUrl; },
        }),
      ]),
    ]);
  }

  /** progress reports each poll, so a wait does not look like a hung page. */
  progress(state: LeaseState): void {
    const line = this.body.querySelector('#busy-progress');
    if (!line) return;
    const seconds = Math.ceil((state.freeInMs ?? 0) / 1000);
    line.textContent = seconds > 0
      ? `Free in about ${seconds} second${seconds === 1 ? '' : 's'}, unless they carry on.`
      : 'It will free up on its own if they close the tab or leave it alone.';
  }

  close(): void {
    clearInterval(this.#countdown);
    if (this.dialog.open) this.dialog.close();
  }

  /**
   * lost is the other direction: this tab HAD the lease and no longer does.
   *
   * No countdown and no choice. Whatever is on screen can no longer be saved,
   * so leaving somebody in an editor that silently refuses every keystroke
   * would be worse than telling them plainly.
   */
  lost(): void {
    replace(this.body, [
      el('p', { text:
        'Someone else is editing this CV now, so your changes can no longer ' +
        'be saved from this window.' }),
      el('p', { class: 'muted', text:
        'Everything you typed before this was saved as you went.' }),
      el('div', { class: 'choices' }, [
        el('button', {
          type: 'button', class: 'primary', text: 'View the CV',
          onclick: () => { location.href = this.viewerUrl; },
        }),
        el('button', {
          type: 'button', text: 'Try to take it back',
          onclick: () => location.reload(),
        }),
      ]),
    ]);
    if (!this.dialog.open) this.dialog.showModal();
  }
}
