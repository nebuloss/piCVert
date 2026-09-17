/**
 * viewer.ts — the frame around a CV.
 *
 * It draws nothing itself: the page is a self-contained document in an iframe,
 * and this is the chrome around it — a language picker, a link to the PDF, and
 * the arithmetic that fits a fixed A4 page onto a phone.
 */

import { PageScaler, el, need } from '../lib/dom.ts';
import type { LanguageEntry } from '../model/api.ts';

class Viewer {
  private readonly base: string;
  private readonly frame: HTMLIFrameElement;
  private readonly pdf: HTMLAnchorElement;
  private readonly picker: HTMLSelectElement;
  private readonly scaler: PageScaler;
  private readonly languages: LanguageEntry[];
  private lang: string;

  constructor(root: Document) {
    this.base = root.body.dataset.base ?? '';
    this.lang = root.body.dataset.lang ?? '';
    this.frame = need<HTMLIFrameElement>(root, '#cv');
    this.pdf = need<HTMLAnchorElement>(root, '#pdf');
    this.picker = need<HTMLSelectElement>(root, '#lang');
    this.scaler = new PageScaler(need(root, '#stage'), need(root, '#fit'), 40);

    const raw = need(root, '#languages').textContent ?? '[]';
    this.languages = JSON.parse(raw) as LanguageEntry[];
  }

  start(): void {
    this.offerLanguages();
    this.scaler.start();
    this.show();
  }

  /**
   * A picker only appears when there is a choice to make.
   *
   * One entry teaches people the control does nothing, and they stop looking at
   * it — including on the CV where there are three.
   */
  private offerLanguages(): void {
    if (this.languages.length < 2) return;
    for (const entry of this.languages) {
      const option = el('option', {
        value: entry.variant ?? '',
        text: entry.lang.toUpperCase() + (entry.isDefault ? ' (default)' : ''),
      });
      if (option.value === this.lang) option.selected = true;
      this.picker.appendChild(option);
    }
    this.picker.hidden = false;
    this.picker.addEventListener('change', () => {
      this.lang = this.picker.value;
      this.show();
      // The address follows what is on screen, so a reload and a copied link
      // both land on the language being read rather than the default one.
      const url = new URL(location.href);
      if (this.lang) url.searchParams.set('lang', this.lang);
      else url.searchParams.delete('lang');
      history.replaceState(null, '', url);
    });
  }

  private get query(): string {
    return this.lang ? `?lang=${encodeURIComponent(this.lang)}` : '';
  }

  private show(): void {
    this.frame.src = `${this.base}/cv.html${this.query}`;
    this.pdf.href = `${this.base}/cv.pdf${this.query}`;
  }
}

new Viewer(document).start();
