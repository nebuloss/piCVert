/**
 * app.ts — the editor, assembled.
 *
 * Everything below is WIRING. The document is the subject; the form, the
 * preview, the autosave and the fit report observe it; none of them refers to
 * another. That is what lets the save queue be rewritten without touching a
 * control, and a control be added without touching the save queue.
 *
 *   CvDocument ──"value"──► Preview ──► FitReport
 *              ──"value"──► Autosave ──► FitReport
 *              ──"shape"──► Form
 *
 * The one thing this file owns is the operations that are not a field edit:
 * switching language, switching template, reordering sections. They go straight
 * to the server because each is a whole-document rewrite the server performs
 * and validates — doing it here as well would be doing it twice, in two places,
 * with two chances to differ.
 */

import { PageScaler, need } from '../lib/dom.ts';
import { CvDocument, Fit } from '../model/document.ts';
import type { DeleteAnswer, Template, TemplateSummary } from '../model/api.ts';
import { Api } from './api.ts';
import { Autosave } from './autosave.ts';
import type { SaveState } from './autosave.ts';
import { Form } from './form.ts';
import { FitReport, Preview } from './preview.ts';
import { HistoryPanel, LanguageBar } from './panels.ts';

class Editor {
  private readonly api: Api;
  private readonly report: FitReport;
  private readonly scaler: PageScaler;
  private readonly languages: LanguageBar;
  private readonly historyPanel: HistoryPanel;

  private readonly form: HTMLElement;
  private readonly page: HTMLIFrameElement;
  private readonly state: HTMLElement;
  private readonly links: HTMLAnchorElement[];

  private lang: string;
  private templates: TemplateSummary[] = [];
  /** The revision the open document was loaded or last saved at. */
  private revision = '';

  private doc?: CvDocument;
  private view?: Form;
  private preview?: Preview;
  private autosave?: Autosave;

  constructor(root: Document) {
    this.form = need(root, '#form');
    this.page = need<HTMLIFrameElement>(root, '#page');
    this.state = need(root, '#state');
    this.links = [...root.querySelectorAll<HTMLAnchorElement>('#bar a.button')];

    const { base = '', slug = '', token = '' } = root.body.dataset;
    this.api = new Api({ slug, token, base });
    this.lang = new URLSearchParams(location.search).get('lang') ?? '';

    this.report = new FitReport(need(root, '#fit'));
    this.scaler = new PageScaler(need(root, '#stage'), need(root, '#scaler')).start();

    this.historyPanel = new HistoryPanel(
      need<HTMLDialogElement>(root, '#history'), need(root, '#history-body'), this.api,
    );
    need<HTMLButtonElement>(root, '#history-open')
      .addEventListener('click', () => void this.historyPanel.open());

    this.languages = new LanguageBar(
      need<HTMLSelectElement>(root, '#lang'),
      need<HTMLButtonElement>(root, '#add-lang'),
      (lang) => void this.open(lang),
      () => void this.addLanguage(),
    );
  }

  private say(kind: SaveState, text: string): void {
    this.state.dataset.kind = kind;
    this.state.textContent = text;
  }

  /** fail reports from anywhere: one place, so none of them is silent. */
  private fail = (error: unknown): void => {
    this.say('error', error instanceof Error ? error.message : String(error));
  };

  async start(): Promise<void> {
    try {
      this.templates = (await this.api.templates()).templates;
    } catch {
      // A template list that will not load costs the picker and nothing else.
      // Refusing to open the editor over it would be losing the CV to a
      // cosmetic failure.
      this.templates = [];
    }
    await this.open(this.lang);
  }

  /**
   * open loads a language and builds everything around it.
   *
   * Rebuilt rather than updated in place: another language is another document,
   * with its own sections and its own history. Reusing the observers would mean
   * a save of the French CV landing in the English one, which is the worst kind
   * of fault this interface could have.
   */
  private async open(lang: string): Promise<void> {
    // Anything typed in the language being left is saved before leaving it.
    await this.autosave?.flush();
    this.autosave?.release();
    this.lang = lang;
    this.say('saving', 'loading…');

    let loaded;
    try {
      loaded = await this.api.load(this.lang);
    } catch (error) {
      this.fail(error);
      return;
    }
    const answer = loaded.body;
    this.revision = loaded.revision;

    const template: Template = answer.template;
    this.doc = new CvDocument(answer.doc);
    this.doc.icons = template.icons;

    this.view = new Form(this.form, this.doc, template, this.templates, {
      api: this.api,
      onPhoto: (file) => void this.uploadPhoto(file),
      onTemplate: (uuid) => this.switchTemplate(uuid),
      onReorder: (order) => void this.reorder(order),
      onRemoveSection: (index) => this.removeSection(index),
      onDeleted: (result) => this.deleted(result),
      onError: this.fail,
    });

    this.preview = new Preview(this.doc, this.page, (doc) => this.api.render(doc, this.lang));
    this.preview.onFit = (fit) => this.report.show(fit);
    this.preview.onError = (error) => this.report.error(error.message);

    this.wireAutosave();

    this.languages.show(answer.languages, this.lang);
    this.view.render();
    this.report.show(new Fit(answer.fit));
    this.retarget();
    this.scaler.apply();
    this.say('saved', 'saved');
    void this.preview.refresh();
  }

  /** The header links carry the language on screen, not the default one. */
  private retarget(): void {
    for (const link of this.links) {
      link.href = link.dataset.kind === 'pdf'
        ? this.api.pdfUrl(this.lang)
        : this.api.pageUrl(this.lang);
    }
  }

  private async uploadPhoto(file: File): Promise<void> {
    this.say('saving', 'uploading…');
    try {
      const answer = await this.api.photo(file, this.lang);
      this.doc?.replace(answer.doc);
      this.say('saved', 'saved');
      // The fit comes with the redraw, like every other change: a portrait
      // changes the page, and the page is what reports on itself.
      void this.preview?.refresh();
    } catch (error) {
      this.fail(error);
    }
  }

  private async switchTemplate(uuid: string): Promise<void> {
    await this.api.setTemplate(uuid, this.lang);
    // Another template is another set of sections and another field tree, so
    // everything is loaded again rather than assumed unchanged.
    await this.open(this.lang);
  }

  private async reorder(order: number[]): Promise<void> {
    try {
      const answer = await this.api.reorderSections(order, this.lang);
      this.doc?.replace(answer.doc);
      void this.preview?.refresh();
    } catch (error) {
      this.fail(error);
    }
  }

  private removeSection(index: number): void {
    if (!this.doc) return;
    const sections = this.doc.sections;
    const name = sections[index]?.title || 'this section';
    // Asked for, because a section is a great deal of typing and the button
    // that removes it is a pixel from the one that moves it.
    if (!confirm(`Remove “${name}” and everything in it?`)) return;
    this.doc.setSections(sections.filter((_, i) => i !== index));
  }

  private async addLanguage(): Promise<void> {
    const code = prompt('Two-letter code for the new language (en, fr, …)');
    if (!code) return;
    const wanted = code.trim().toLowerCase();
    try {
      await this.api.addLanguage(wanted, this.lang);
      await this.open(wanted);
    } catch (error) {
      this.fail(error);
    }
  }

  /**
   * resolveConflict asks the person what to do, and does nothing until they say.
   *
   * There is no right answer to pick for them. Reloading throws away what they
   * have typed; overwriting throws away what somebody else typed. The interface
   * knows which is worse in neither case, so it describes both and waits — and
   * the autosave stays stopped meanwhile, so nothing is decided by default.
   */
  private resolveConflict(): void {
    const keep = confirm(
      'Someone else has changed this CV since you opened it.\n\n' +
      'OK — reload theirs, and lose what you have typed here.\n' +
      'Cancel — keep yours, and overwrite theirs.',
    );
    if (keep) {
      void this.open(this.lang);
      return;
    }
    // Overwriting is deliberate, so it is done without a revision at all —
    // which is the API's way of saying "I am not making a claim about what I
    // am replacing".
    void this.overwrite();
  }

  private async overwrite(): Promise<void> {
    if (!this.doc) return;
    this.say('saving', 'overwriting…');
    try {
      const answer = await this.api.save(this.doc.raw, this.lang, '');
      this.doc.adopt(answer.body.doc);
      this.revision = answer.revision;
      void this.preview?.refresh();
      this.say('saved', 'saved — theirs was overwritten');
      // A new queue: the old one stopped itself at the conflict and will not
      // start again, which is what kept it from deciding this on its own.
      this.wireAutosave();
    } catch (error) {
      this.fail(error);
    }
  }

  /**
   * wireAutosave builds the save queue and attaches it to the document.
   *
   * Built afresh rather than reset, and that is deliberate: a queue stops
   * itself on a conflict and must NOT start again on its own, so "carry on
   * saving" is a new object rather than a flag somebody could clear by
   * accident from anywhere else.
   */
  private wireAutosave(): void {
    if (!this.doc) return;
    this.autosave?.release();
    this.autosave = new Autosave(
      this.doc, (doc) => this.api.save(doc, this.lang, this.revision),
    ).guard();
    this.autosave.on('state', ({ kind, text }) => this.say(kind, text));
    this.autosave.on('conflict', () => this.resolveConflict());
    this.autosave.on('saved', ({ body, revision }) => {
      // The STORED document comes back and replaces ours. The store pins the
      // template as a UUID and stamps the time, so a client keeping its own
      // idea of the document would send those back stale on the next save.
      // Adopted quietly: this is not a change the person made, and redrawing
      // the form under them would be the editor twitching on every save.
      this.doc?.adopt(body.doc);
      // And the revision moves with it, so the next save is checked against
      // what this client itself wrote.
      this.revision = revision;
    });
  }

  private deleted(answer: DeleteAnswer): void {
    this.autosave?.release();
    this.scaler.stop();
    Form.gone(document.body, answer.grace);
  }
}

void new Editor(document).start();
