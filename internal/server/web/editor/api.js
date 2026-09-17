// api.js — the routes, named once.
//
// A facade over the transport, and its job is that no part of the interface
// spells a URL. A panel building `/api/p/${slug}/sections/${id}/order` itself
// is a panel that keeps working when the route moves and starts 404ing at the
// one moment nobody tests.

import { HttpClient } from '../lib/http.js';

export class Api {
  constructor({ slug, token, base }) {
    this.slug = slug;
    this.base = base;
    this.http = new HttpClient({ token });
  }

  /** The language is a query parameter on nearly everything, so it is built once. */
  #q(lang) {
    return lang ? `?lang=${encodeURIComponent(lang)}` : '';
  }

  #p(suffix = '', lang = '') {
    return `/api/p/${this.slug}${suffix}${this.#q(lang)}`;
  }

  load(lang) { return this.http.get(this.#p('', lang)); }
  save(doc, lang) { return this.http.put(this.#p('', lang), doc); }
  preview(doc, lang) { return this.http.post(this.#p('/preview', lang), doc); }
  fit(lang) { return this.http.get(this.#p('/fit', lang)); }
  history(lang) { return this.http.get(this.#p('/history', lang)); }
  links() { return this.http.get(this.#p('/links')); }
  remove() { return this.http.delete(this.#p()); }

  setTemplate(uuid, lang) {
    return this.http.put(this.#p('/template', lang), { template: uuid });
  }

  reorderSections(order, lang) {
    return this.http.put(this.#p('/sections/order', lang), { order });
  }

  templates() { return this.http.get('/api/templates'); }

  languages() { return this.http.get(this.#p('/languages')); }
  addLanguage(lang, from) {
    return this.http.post(this.#p('/languages'), { lang, from: from || '' });
  }
  removeLanguage(lang) {
    return this.http.delete(`/api/p/${this.slug}/languages/${encodeURIComponent(lang)}`);
  }

  photo(file, lang) {
    return this.http.post(this.#p('/photo', lang), file, file.type);
  }

  /** The links out of the editor, carrying whichever language is being edited. */
  pageUrl(lang) { return `${this.base}/${this.#q(lang)}`; }
  pdfUrl(lang) { return `${this.base}/cv.pdf${this.#q(lang)}`; }
}
