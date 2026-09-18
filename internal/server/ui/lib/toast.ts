/**
 * toast.ts — the passing message.
 *
 * `alert()` is what this replaces, and it is worse than it looks: it blocks
 * the thread, it has to be dismissed, and it interrupts whatever the person
 * was doing to report something they usually already expected. A confirmation
 * that demands a click is a confirmation that trains people to click without
 * reading.
 *
 * An error stays up more than twice as long as a success. "Saved" is read out
 * of the corner of the eye; an error message is read properly, and often
 * twice.
 */

import { el } from './dom.ts';

const OK_MS = 2600;
const ERROR_MS = 6000;

const CSS = `
  .pv-toast {
    position: fixed; bottom: 18px; left: 50%; transform: translateX(-50%);
    background: #1b1b1f; color: #fff;
    padding: 11px 20px; border-radius: 999px;
    font: 13px/1.4 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
    box-shadow: 0 8px 24px rgb(0 0 0 / 30%);
    opacity: 0; transition: opacity .2s; pointer-events: none;
    max-width: calc(100% - 32px); text-align: center; z-index: 100;
  }
  .pv-toast[data-show] { opacity: 1; }
  .pv-toast.err { background: #b3261e; }
  @media (prefers-reduced-motion: reduce) { .pv-toast { transition: none; } }`;

/**
 * Toast is the one banner on the page.
 *
 * One, not a stack: two messages at once are two things nobody reads, and the
 * second is always the one that mattered. A new message replaces the old.
 */
class Toast {
  private node: HTMLElement | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;

  show(message: string, isError = false): void {
    if (!this.node) {
      document.head.appendChild(el('style', { text: CSS }));
      // Announced to assistive technology, which is the part `alert()` got
      // right by accident and a silently appearing <div> gets wrong.
      // `assertive` for errors: a failure interrupts, a success waits.
      this.node = el('div', { class: 'pv-toast', role: 'status' });
      document.body.appendChild(this.node);
    }
    this.node.textContent = message;
    this.node.classList.toggle('err', isError);
    this.node.setAttribute('aria-live', isError ? 'assertive' : 'polite');
    this.node.setAttribute('data-show', '');

    clearTimeout(this.timer);
    this.timer = setTimeout(
      () => this.node?.removeAttribute('data-show'),
      isError ? ERROR_MS : OK_MS,
    );
  }

  error(message: string): void { this.show(message, true); }

  /** what turns an unknown thrown value into something worth showing. */
  failure(error: unknown): void {
    this.error(error instanceof Error ? error.message : String(error));
  }
}

export const toast = new Toast();
