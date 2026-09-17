/**
 * preview.ts — the page as it would be, from what has not been saved yet.
 *
 * This is the hot path of the whole service. It runs on every pause in typing,
 * and each run is a complete layout of the CV — which is affordable only
 * because the engine lays out ONCE and both renderings come from that single
 * result. The engine this replaced ran a browser layout and a separate PDF
 * layout here, and they disagreed by about a line.
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
    delay = 250,
  ) {
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
  private write(html: string): void {
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
