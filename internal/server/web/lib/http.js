// http.js — one way to reach the service.
//
// A facade over `fetch`, and it exists for one reason: the server answers
// `{ok:false,error:"…"}` with a 200 in some paths and a 4xx in others, and
// every call site that unpicked that itself would be a call site that
// eventually forgot the case it did not hit. Here it is unpicked once, and the
// rest of the interface either gets a body or an Error carrying the engine's
// own message.

/** HttpError carries the status as well as the message, for callers that care. */
export class HttpError extends Error {
  constructor(message, status) {
    super(message);
    this.name = 'HttpError';
    this.status = status;
  }
}

export class HttpClient {
  /**
   * The token travels in a HEADER, never in the query string.
   *
   * Query strings are written to proxy logs and kept in browser history, and
   * this token is the only secret the service has. The address bar already
   * carries it — that cannot be helped, it IS the link — but nothing else has
   * to, and a request logged by an intermediary should not be a request that
   * hands over the CV.
   */
  constructor({ token = '' } = {}) {
    this.token = token;
  }

  get(path) { return this.send('GET', path); }
  post(path, body, type) { return this.send('POST', path, body, type); }
  put(path, body) { return this.send('PUT', path, body); }
  patch(path, body) { return this.send('PATCH', path, body); }
  delete(path) { return this.send('DELETE', path); }

  async send(method, path, body, type) {
    const headers = {};
    if (this.token) headers['X-CV-Token'] = this.token;

    let payload;
    if (body !== undefined) {
      // A Blob is sent as it is, with its own type: that is the portrait, and
      // base64 in JSON would be a third larger for nothing.
      if (type) {
        headers['Content-Type'] = type;
        payload = body;
      } else {
        headers['Content-Type'] = 'application/json';
        payload = JSON.stringify(body);
      }
    }

    let response;
    try {
      response = await fetch(path, { method, headers, body: payload });
    } catch (cause) {
      // A network failure is not a server error, and saying so matters: the
      // person is told their connection dropped rather than that their CV was
      // rejected, and the autosave knows to keep the change and try again.
      throw new HttpError('No connection to the server.', 0, { cause });
    }

    const text = await response.text();
    let parsed = null;
    try {
      parsed = text ? JSON.parse(text) : null;
    } catch {
      /* not JSON: an error page from a proxy, most likely */
    }

    if (!response.ok || parsed?.ok === false) {
      // The engine's own message, not a generic one. Whoever holds this link is
      // the person who can fix the CV that broke, and "something went wrong"
      // tells them nothing they can act on.
      throw new HttpError(
        parsed?.error || text.slice(0, 200) || response.statusText,
        response.status,
      );
    }
    return parsed;
  }
}
