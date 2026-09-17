/**
 * http.ts — one way to reach the service.
 *
 * A facade over `fetch`, and it exists for one reason: the server reports
 * failure as `{ok:false,error:"…"}` with a 200 on some paths and a 4xx on
 * others, and every call site that unpicked that itself would be a call site
 * that eventually forgot the case it did not hit. Here it is unpicked once, and
 * the rest of the interface either gets a typed body or an Error carrying the
 * engine's own message.
 */

/** HttpError carries the status as well as the message, for callers that care. */
export class HttpError extends Error {
  constructor(
    message: string,
    readonly status: number,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = 'HttpError';
  }
}

/** What a failed route answers with. */
interface Failure {
  ok: false;
  error: string;
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
  constructor(private readonly token = '') {}

  get<T>(path: string): Promise<T> {
    return this.send<T>('GET', path);
  }

  post<T>(path: string, body?: unknown, type?: string): Promise<T> {
    return this.send<T>('POST', path, body, type);
  }

  put<T>(path: string, body?: unknown): Promise<T> {
    return this.send<T>('PUT', path, body);
  }

  patch<T>(path: string, body?: unknown): Promise<T> {
    return this.send<T>('PATCH', path, body);
  }

  delete<T>(path: string): Promise<T> {
    return this.send<T>('DELETE', path);
  }

  /**
   * The return type is the caller's claim, not a check.
   *
   * Nothing here validates the shape, and pretending otherwise would be worse
   * than not typing it: the guarantee comes from `model/api.ts` being written
   * against the Go that produces these bodies, and from the two being changed
   * together. What the generic buys is that a renamed field stops compiling at
   * every reader — which is the failure that is otherwise invisible.
   */
  private async send<T>(
    method: string,
    path: string,
    body?: unknown,
    type?: string,
  ): Promise<T> {
    const headers: Record<string, string> = {};
    if (this.token) headers['X-CV-Token'] = this.token;

    let payload: BodyInit | undefined;
    if (body !== undefined) {
      if (type) {
        // A Blob goes as it is, with its own type: that is the portrait, and
        // base64 in JSON would be a third larger for nothing.
        headers['Content-Type'] = type;
        payload = body as BodyInit;
      } else {
        headers['Content-Type'] = 'application/json';
        payload = JSON.stringify(body);
      }
    }

    let response: Response;
    try {
      response = await fetch(path, { method, headers, body: payload });
    } catch (cause) {
      // A network failure is not a server error, and saying so matters: the
      // person is told their connection dropped rather than that their CV was
      // rejected, and the autosave knows to keep the change and try again.
      throw new HttpError('No connection to the server.', 0, { cause });
    }

    const text = await response.text();
    let parsed: unknown = null;
    try {
      parsed = text ? (JSON.parse(text) as unknown) : null;
    } catch {
      /* not JSON: an error page from a proxy, most likely */
    }

    if (!response.ok || isFailure(parsed)) {
      // The engine's own message, not a generic one. Whoever holds this link is
      // the person who can fix the CV that broke, and "something went wrong"
      // tells them nothing they can act on.
      const message = isFailure(parsed)
        ? parsed.error
        : text.slice(0, 200) || response.statusText;
      throw new HttpError(message, response.status);
    }
    return parsed as T;
  }
}

function isFailure(value: unknown): value is Failure {
  return (
    typeof value === 'object' &&
    value !== null &&
    'ok' in value &&
    (value as { ok: unknown }).ok === false
  );
}
