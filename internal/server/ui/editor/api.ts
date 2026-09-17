/**
 * api.ts — the routes, named once.
 *
 * A facade over the transport, and its job is that no part of the interface
 * spells a URL. A panel building `/api/p/${slug}/sections/${id}/order` itself is
 * a panel that keeps working when the route moves and starts 404ing at the one
 * moment nobody tests.
 *
 * Every method states what it answers with, from `model/api.ts`. That is the
 * seam with the Go service, and it is the one place the two languages have to
 * agree.
 */

import { HttpClient } from '../lib/http.ts';
import type { Tagged } from '../lib/http.ts';
import type { LeaseState } from './lease.ts';
import type {
  Cv, DeleteAnswer, LanguagesAnswer, LinksAnswer, LoadAnswer,
  PreviewAnswer, SaveAnswer, TemplatesAnswer, HistoryAnswer,
} from '../model/api.ts';

export interface ApiOptions {
  slug: string;
  token: string;
  base: string;
}

export class Api {
  readonly slug: string;
  readonly base: string;
  private readonly token: string;
  private readonly http: HttpClient;

  /** Which tab this is; see lease.ts for why the cookie alone is not enough. */
  window = '';

  constructor({ slug, token, base }: ApiOptions) {
    this.slug = slug;
    this.base = base;
    this.token = token;
    this.http = new HttpClient(token);
  }

  /** The language is a query parameter on nearly everything, so it is built once. */
  #q(lang: string): string {
    return lang ? `?lang=${encodeURIComponent(lang)}` : '';
  }

  #p(suffix = '', lang = ''): string {
    return `/api/p/${this.slug}${suffix}${this.#q(lang)}`;
  }

  /**
   * load brings back the document AND the revision it is at.
   *
   * The revision is what makes a save safe: sent back with the next write, it
   * lets the server refuse a change built on a document that has since moved,
   * rather than letting it overwrite whatever moved it.
   */
  load(lang: string): Promise<Tagged<LoadAnswer>> {
    return this.http.tagged<LoadAnswer>('GET', this.#p('', lang));
  }

  /**
   * save stores the document. It does NOT draw the page.
   *
   * That separation is the whole reason a change can be saved the moment it is
   * made: writing a few kilobytes of JSON takes microseconds, where laying the
   * page out takes most of a second. The page is drawn by render() below, on a
   * pause rather than on a letter.
   */
  save(doc: Cv, lang: string, revision: string): Promise<Tagged<SaveAnswer>> {
    return this.http.tagged<SaveAnswer>('PUT', this.#p('', lang), doc, revision, this.window);
  }

  /**
   * render lays the document out and brings back the page and the fit.
   *
   * The expensive call, and the one the editor makes on a pause rather than on
   * every change. It takes the document rather than reading the stored one so
   * that it still works while a save is in flight — the two are independent by
   * design, and a render that had to wait for a save would reintroduce exactly
   * the coupling this separation removed.
   */
  render(doc: Cv, lang: string): Promise<PreviewAnswer> {
    return this.http.post<PreviewAnswer>(this.#p('/preview', lang), doc);
  }

  history(lang = ''): Promise<HistoryAnswer> {
    return this.http.get<HistoryAnswer>(this.#p('/history', lang));
  }

  links(): Promise<LinksAnswer> {
    return this.http.get<LinksAnswer>(this.#p('/links'));
  }

  remove(): Promise<DeleteAnswer> {
    return this.http.delete<DeleteAnswer>(this.#p());
  }

  setTemplate(uuid: string, lang: string): Promise<SaveAnswer> {
    return this.http.put<SaveAnswer>(this.#p('/template', lang), { template: uuid });
  }

  reorderSections(order: number[], lang: string): Promise<SaveAnswer> {
    return this.http.put<SaveAnswer>(this.#p('/sections/order', lang), { order });
  }

  templates(): Promise<TemplatesAnswer> {
    return this.http.get<TemplatesAnswer>('/api/templates');
  }

  addLanguage(lang: string, from: string): Promise<SaveAnswer> {
    return this.http.post<SaveAnswer>(this.#p('/languages'), { lang, from });
  }

  removeLanguage(lang: string): Promise<LanguagesAnswer> {
    return this.http.delete<LanguagesAnswer>(
      `/api/p/${this.slug}/languages/${encodeURIComponent(lang)}`,
    );
  }

  photo(file: File, lang: string): Promise<SaveAnswer> {
    return this.http.post<SaveAnswer>(this.#p('/photo', lang), file, file.type);
  }

  /**
   * lease asks to edit, or says this window is still here.
   *
   * WHICH window is not stated anywhere here. The server gives a browser an
   * identity as a cookie it cannot read, and the browser sends it back by
   * itself — so this code neither knows nor can invent one, and a reload gets
   * the same lease back rather than being told it is somebody else.
   */
  async lease(lang: string, renew: boolean): Promise<LeaseState> {
    const path = this.#p('/lease', lang) + (lang ? '&' : '?') + `renew=${renew ? '1' : '0'}`;
    return this.http.post<LeaseState>(path, undefined, undefined, this.window);
  }

  /**
   * releaseLease gives it up as the tab closes.
   *
   * A beacon, because a browser may cancel an ordinary request when the page
   * goes away — and this one is sent at exactly that moment. Nothing depends
   * on it: the lease lapses by itself in under a minute. It is the difference
   * between the next person waiting a moment and waiting the timeout.
   *
   * Same-origin, so it carries the identity cookie by itself. The token goes
   * in the query string because a beacon cannot set a header, and this is the
   * one request that has no alternative.
   */
  releaseLease(lang: string): void {
    const path = this.#p('/lease', lang) + (lang ? '&' : '?') +
      `token=${encodeURIComponent(this.token)}&release=1` +
      `&window=${encodeURIComponent(this.window)}`;
    navigator.sendBeacon(path);
  }

  /** The links out of the editor, carrying whichever language is being edited. */
  pageUrl(lang: string): string {
    return `${this.base}/${this.#q(lang)}`;
  }

  pdfUrl(lang: string): string {
    return `${this.base}/cv.pdf${this.#q(lang)}`;
  }
}
