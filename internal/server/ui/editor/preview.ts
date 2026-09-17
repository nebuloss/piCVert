/**
 * preview.ts — the page, drawn on a pause rather than on a letter.
 *
 * # IT RUNS AT ITS OWN RATE, AND THAT IS THE POINT
 *
 * Saving and drawing used to be one thing, because a save reported the fit and
 * the fit is a layout. That made a keystroke cost a full render — measured at
 * 193 ms against a render's 182 — so the service could sustain about five
 * characters a second in total, for everybody.
 *
 * They are now separate and run at different rates on purpose:
 *
 *   SAVING     every change, immediately. Microseconds: validate and write.
 *   DRAWING    after a pause. Hundreds of milliseconds: the whole engine.
 *
 * Nothing is lost by drawing late. The document is already safe on disk; what
 * arrives a quarter of a second behind is only the picture of it.
 */

import { Debounced } from '../lib/emitter.ts';
import { Fit } from '../model/document.ts';
import type { CvDocument } from '../model/document.ts';
import type { Cv, PreviewAnswer } from '../model/api.ts';

export class Preview {
  private readonly task: Debounced;

  onFit?: (fit: Fit) => void;
  onError?: (error: Error) => void;

  constructor(
    private readonly doc: CvDocument,
    private readonly frame: HTMLIFrameElement,
    private readonly render: (doc: Cv) => Promise<PreviewAnswer>,
    delay = 400,
  ) {
    // Debounced, and it is the only thing here that is. Typing produces a
    // change per letter and a picture per letter is a picture nobody sees —
    // the layout takes longer than the gap between keystrokes, so most of them
    // would be drawn and replaced before a screen refresh.
    this.task = new Debounced(() => this.draw(), delay);
    doc.on('value', () => this.task.schedule());
    doc.on('shape', () => this.task.schedule());
  }

  refresh(): Promise<void> {
    return this.task.run();
  }

  private async draw(): Promise<void> {
    try {
      const answer = await this.render(this.doc.raw);
      this.write(answer.html);
      this.onFit?.(new Fit(answer.fit));
    } catch (error) {
      // Shown where the fit is shown, and the LAST GOOD PAGE is left on screen.
      // Blanking it would mean a momentarily invalid document — a half-typed
      // number, an empty required field — replaces the preview with nothing,
      // and the person loses their place while they finish the word.
      this.onError?.(error instanceof Error ? error : new Error(String(error)));
    }
  }

  /**
   * write puts the page into the frame directly.
   *
   * Written rather than pointed at a URL, because the document being drawn has
   * no address: it is what is being typed, and it has not been saved. A preview
   * that had to be saved first would not be a preview.
   */
  write(html: string): void {
    const inner = this.frame.contentDocument;
    if (!inner) return;
    inner.open();
    inner.write(html);
    inner.close();
  }
}

/**
 * FitReport is the line above the preview: does it hold, and what is left.
 *
 * It says the same thing the `fit` command says, in the same words, because
 * somebody reading both should not have to work out that they agree.
 */
export class FitReport {
  constructor(private readonly node: HTMLElement) {}

  show(fit: Fit): void {
    this.node.dataset.ok = fit.ok ? 'true' : 'false';
    this.node.dataset.tight = fit.tight ? 'true' : 'false';
    this.node.textContent = '';

    const summary = document.createElement('b');
    summary.textContent = fit.summary;
    this.node.append(summary);

    const room = fit.columns
      .map(({ name, room: left }) => `${name}: ${left} px left`)
      .join('  ·  ');
    if (room) this.node.append(`  ·  ${room}`);

    if (fit.over.length) {
      this.node.append(`  ·  shorten: ${fit.over.join(', ')}`);
    } else if (fit.tight) {
      // The page holds, and it holds by being set tighter than the design
      // intends. Saying so is the difference between a document that fits and
      // one that has been made to.
      this.node.append('  ·  the page is set tight — consider shortening it');
    }
  }

  error(message: string): void {
    this.node.dataset.ok = 'false';
    this.node.textContent = message;
  }
}
