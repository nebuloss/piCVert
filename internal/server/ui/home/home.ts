/**
 * home.ts — making a CV from the front page.
 *
 * The one place a stranger can act on this service, which is why it is the one
 * page carrying a challenge. See internal/server/newcv.go: without Turnstile
 * configured there is no form here and no route behind it.
 */

import { copy, need } from '../lib/dom.ts';
import { HttpClient } from '../lib/http.ts';

interface MadeAnswer {
  ok: true;
  slug: string;
  edit: string;
  read: string;
}

/** What Cloudflare's script puts on the page. */
declare global {
  interface Window {
    turnstile?: { reset(): void };
  }
}

class NewCv {
  private readonly form: HTMLFormElement;
  private readonly note: HTMLElement;
  private readonly made: HTMLElement;
  private readonly api = new HttpClient();

  constructor(root: Document) {
    this.form = need<HTMLFormElement>(root, '#new');
    this.note = need(root, '#new-note');
    this.made = need(root, '#made');

    // The address is offered rather than demanded: typing a name and getting a
    // sensible one is the common case, and correcting it by hand is still
    // there for the rest.
    const name = this.form.elements.namedItem('name') as HTMLInputElement;
    const slug = this.form.elements.namedItem('slug') as HTMLInputElement;
    name.addEventListener('input', () => {
      if (slug.dataset.touched) return;
      slug.value = name.value.toLowerCase().normalize('NFD')
        .replace(/[\u0300-\u036f]/g, '')
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '')
        .slice(0, 60);
    });
    slug.addEventListener('input', () => { slug.dataset.touched = '1'; });

    this.form.addEventListener('submit', (event) => {
      event.preventDefault();
      void this.submit();
    });
  }

  private async submit(): Promise<void> {
    const button = need<HTMLButtonElement>(this.form, 'button[type=submit]');
    button.disabled = true;
    this.note.textContent = '';
    this.note.className = 'note';

    const data = new FormData(this.form);
    try {
      const answer = await this.api.post<MadeAnswer>('/api/new', {
        name: String(data.get('name') ?? ''),
        slug: String(data.get('slug') ?? ''),
        lang: String(data.get('lang') ?? ''),
        // Cloudflare puts its answer in a hidden field of the surrounding
        // form. Read here rather than tracked, because the widget owns it and
        // resets it whenever it likes.
        turnstile: String(data.get('cf-turnstile-response') ?? ''),
      });
      this.show(answer);
    } catch (error) {
      this.note.textContent = error instanceof Error ? error.message : String(error);
      this.note.className = 'note error';
      button.disabled = false;
      // A solved challenge is spent whether the request worked or not, so the
      // widget has to be reset — otherwise a second attempt sends a token
      // Cloudflare has already seen, and is refused for a reason that looks
      // nothing like the real one.
      window.turnstile?.reset();
    }
  }

  private show(answer: MadeAnswer): void {
    this.form.hidden = true;
    this.made.hidden = false;

    const edit = need<HTMLInputElement>(document, '#edit-link');
    const read = need<HTMLInputElement>(document, '#read-link');
    edit.value = answer.edit;
    read.value = answer.read;
    need<HTMLAnchorElement>(document, '#open-editor').href = answer.edit;

    for (const [selector, field] of [['#copy-edit', edit], ['#copy-read', read]] as const) {
      const node = need<HTMLButtonElement>(document, selector);
      node.addEventListener('click', () => {
        field.select();
        void copy(field.value).then((done) => {
          node.textContent = done ? 'Copied' : 'Press Ctrl+C';
          window.setTimeout(() => { node.textContent = 'Copy'; }, 1800);
        });
      });
    }

    // The address follows, so a reload does not lose the link — the one thing
    // on this page that cannot be recovered.
    history.replaceState(null, '', `#${answer.slug}`);
    this.made.scrollIntoView({ behavior: 'smooth' });
  }
}

if (document.body.dataset.siteKey) new NewCv(document);
