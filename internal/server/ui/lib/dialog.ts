/**
 * dialog.ts — asking before doing something irreversible.
 *
 * Replaces `confirm()`, which has three problems. Its appearance is the
 * browser's and matches nothing around it; it blocks the thread; and some
 * browsers suppress it silently when the page does not have focus — so a
 * deletion appears to do nothing at all, which is the worst possible outcome
 * for the one call that most needs an answer.
 *
 * `<dialog>` is used as it comes: the focus trap, Escape, the inert background
 * and returning focus to the button that opened it are all native. Rewriting
 * those by hand would be more code and less accessible.
 *
 * Shared between the editor and the administration page, because deleting a CV
 * and deleting a language deserve the same warning — and two copies of that
 * warning would drift apart.
 */

import { el } from './dom.ts';

/**
 * The stylesheet, added on first use rather than at load: a page that never
 * asks anything carries no trace of this.
 *
 * The colours are the two surfaces' shared neutrals, declared here rather than
 * inherited. A shared component cannot assume its host's variables exist —
 * this is opened from the editor, which has its own palette, and from the
 * administration page, which has another.
 */
const CSS = `
  dialog.pv-dialog {
    position: fixed; inset: 0; margin: auto;
    width: calc(100% - 32px); max-width: 440px;
    height: fit-content; max-height: calc(100% - 32px); overflow: auto;
    border: 0; border-radius: 18px; padding: 0;
    background: Canvas; color: CanvasText;
    box-shadow: 0 18px 50px rgb(0 0 0 / 28%);
    font: 14px/1.55 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  }
  dialog.pv-dialog::backdrop { background: rgb(10 20 40 / 45%); }
  .pv-dialog .body { padding: 22px 24px 6px; }
  .pv-dialog h2 { font-size: 16px; font-weight: 650; margin: 0 0 10px; }
  .pv-dialog p { font-size: 13.5px; line-height: 1.55; margin: 0 0 8px; opacity: .8; }
  .pv-dialog .target {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12.5px;
    background: color-mix(in srgb, CanvasText 6%, Canvas);
    border: 1px solid color-mix(in srgb, CanvasText 15%, Canvas);
    border-radius: 10px; padding: 8px 12px; margin: 12px 0; word-break: break-all;
  }
  .pv-dialog .foot {
    display: flex; gap: 10px; justify-content: flex-end; padding: 14px 24px 20px;
  }
  .pv-dialog button {
    font: inherit; font-size: 13.5px; font-weight: 550; border: 0;
    border-radius: 999px; padding: 10px 20px; cursor: pointer;
    background: color-mix(in srgb, CanvasText 8%, Canvas); color: CanvasText;
  }
  .pv-dialog button:hover { filter: brightness(1.1); }
  .pv-dialog button.danger { background: #b3261e; color: #fff; }
  .pv-dialog button.go { background: #0b57d0; color: #fff; }
  .pv-dialog button:focus-visible { outline: 2px solid currentColor; outline-offset: 2px; }
  @media (max-width: 480px) {
    .pv-dialog .foot { flex-direction: column-reverse; }
    .pv-dialog button { width: 100%; padding: 13px 20px; }
  }`;

export interface DialogOptions {
  /** What is about to happen. */
  title: string;
  /** What is lost by doing it. */
  body?: string;
  /**
   * The thing being acted on, named as it is named elsewhere in the interface.
   *
   * This is what makes a confirmation worth reading. "Delete this CV?" is a
   * question nobody answers carefully; the same question with `data/marie/`
   * under it is one they do.
   */
  target?: string;
  confirm?: string;
  cancel?: string;
  /** False for an action that destroys nothing, which colours the button. */
  dangerous?: boolean;
}

let styled = false;

function ensureStyle(): void {
  if (styled) return;
  document.head.appendChild(el('style', { text: CSS }));
  styled = true;
}

/** confirmDialog asks, and resolves to what was chosen. */
export function confirmDialog(options: DialogOptions): Promise<boolean> {
  ensureStyle();

  const cancel = el('button', { type: 'button', text: options.cancel ?? 'Cancel' });
  const ok = el('button', {
    type: 'button',
    class: options.dangerous === false ? 'go' : 'danger',
    text: options.confirm ?? 'Confirm',
  });

  const dialog = el('dialog', { class: 'pv-dialog' }, [
    el('div', { class: 'body' }, [
      el('h2', { text: options.title }),
      options.body ? el('p', { text: options.body }) : null,
      options.target ? el('div', { class: 'target', text: options.target }) : null,
    ]),
    el('div', { class: 'foot' }, [cancel, ok]),
  ]) as HTMLDialogElement;

  document.body.appendChild(dialog);

  return new Promise<boolean>((resolve) => {
    let answer = false;
    cancel.addEventListener('click', () => dialog.close());
    ok.addEventListener('click', () => { answer = true; dialog.close(); });
    // `close` rather than the button handlers, so Escape and the backdrop
    // resolve too — otherwise a dismissed dialog leaves a promise that never
    // settles, and the caller waits forever.
    dialog.addEventListener('close', () => {
      dialog.remove();
      resolve(answer);
    });
    dialog.showModal();
    // The safe choice is focused, so Enter on a dialog nobody read cancels.
    cancel.focus();
  });
}
