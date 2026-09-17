// api.js — every request the editor makes, in one place.
//
// The token travels in a HEADER rather than in the query string: query strings
// are logged by proxies and kept in browser history, and the token is the only
// secret this service has. The address bar already carries it — that cannot be
// helped, it IS the link — but nothing else has to.

const body = document.body;

export const base = body.dataset.base;
export const slug = body.dataset.slug;
const token = body.dataset.token;

async function call(method, path, payload, type) {
  const options = {
    method,
    headers: { 'X-CV-Token': token },
  };
  if (payload !== undefined) {
    options.headers['Content-Type'] = type || 'application/json';
    options.body = type ? payload : JSON.stringify(payload);
  }
  const response = await fetch(path, options);
  const text = await response.text();
  let parsed = null;
  try { parsed = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!response.ok || (parsed && parsed.ok === false)) {
    // The engine's own message, not a generic one. Whoever is holding this link
    // is the person who can fix the CV that broke, and "something went wrong"
    // tells them nothing they can act on.
    throw new Error((parsed && parsed.error) || text || response.statusText);
  }
  return parsed;
}

const query = (lang) => (lang ? '?lang=' + encodeURIComponent(lang) : '');

export const api = {
  load: (lang) => call('GET', `/api/p/${slug}${query(lang)}`),
  save: (doc, lang) => call('PUT', `/api/p/${slug}${query(lang)}`, doc),
  setTemplate: (uuid, lang) =>
    call('PUT', `/api/p/${slug}/template${query(lang)}`, { template: uuid }),
  reorderSections: (order, lang) =>
    call('PUT', `/api/p/${slug}/sections/order${query(lang)}`, { order }),
  preview: (doc, lang) => call('POST', `/api/p/${slug}/preview${query(lang)}`, doc),
  history: (lang) => call('GET', `/api/p/${slug}/history${query(lang)}`),
  templates: () => call('GET', '/api/templates'),
  languages: () => call('GET', `/api/p/${slug}/languages`),
  addLanguage: (lang, from) =>
    call('POST', `/api/p/${slug}/languages`, { lang, from: from || '' }),
  removeLanguage: (lang) => call('DELETE', `/api/p/${slug}/languages/${lang}`),
  photo: (blob, lang) =>
    call('POST', `/api/p/${slug}/photo${query(lang)}`, blob, blob.type),
};
