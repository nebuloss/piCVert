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
  private readonly http: HttpClient;

  constructor({ slug, token, base }: ApiOptions) {
    this.slug = slug;
    this.base = base;
    this.http = new HttpClient(token);
  }

  /** The language is a query parameter on nearly everything, so it is built once. */
  #q(lang: string): string {
    return lang ? `?lang=${encodeURIComponent(lang)}` : '';
  }

  #p(suffix = '', lang = ''): string {
    return `/api/p/${this.slug}${suffix}${this.#q(lang)}`;
  }

  load(lang: string): Promise<LoadAnswer> {
    return this.http.get<LoadAnswer>(this.#p('', lang));
  }

  save(doc: Cv, lang: string): Promise<SaveAnswer> {
    return this.http.put<SaveAnswer>(this.#p('', lang), doc);
  }

  preview(doc: Cv, lang: string): Promise<PreviewAnswer> {
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

  /** The links out of the editor, carrying whichever language is being edited. */
  pageUrl(lang: string): string {
    return `${this.base}/${this.#q(lang)}`;
  }

  pdfUrl(lang: string): string {
    return `${this.base}/cv.pdf${this.#q(lang)}`;
  }
}
