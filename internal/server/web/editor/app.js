// app.js — the editor, assembled.
//
// Everything below is WIRING. The document is the subject; the form, the
// preview, the autosave and the fit report observe it; none of them refers to
// another. That is what lets the save queue be rewritten without touching a
// control, and a control be added without touching the save queue.
//
//   CvDocument ──"value"──► Preview ──► FitReport
//              ──"value"──► Autosave ──► FitReport
//              ──"shape"──► Form
//
// The one thing this file owns is the operations that are not a field edit:
// switching language, switching template, reordering sections. They go straight
// to the server because each is a whole-document rewrite the server performs
// and validates — sending the result of doing it here would be doing it twice,
// in two places, with two chances to differ.

import { PageScaler, el } from '../lib/dom.js';
import { CvDocument, Fit } from '../model/document.js';
import { Api } from './api.js';
import { Autosave } from './autosave.js';
import { Form } from './form.js';
import { FitReport, Preview } from './preview.js';
import { HistoryPanel, LanguageBar } from './panels.js';

class Editor {
  constructor(root) {
    this.node = {
      form: root.querySelector('#form'),
      page: root.querySelector('#page'),
      scaler: root.querySelector('#scaler'),
      stage: root.querySelector('#stage'),
      fit: root.querySelector('#fit'),
      state: root.querySelector('#state'),
      lang: root.querySelector('#lang'),
      addLang: root.querySelector('#add-lang'),
      history: root.querySelector('#history'),
      historyBody: root.querySelector('#history-body'),
      historyOpen: root.querySelector('#history-open'),
      links: [...root.querySelectorAll('#bar a.button')],
    };

    const { base, slug, token } = document.body.dataset;
    this.api = new Api({ slug, token, base });
    this.lang = new URLSearchParams(location.search).get('lang') ?? '';
    this.templates = [];

    this.report = new FitReport(this.node.fit);
    this.scaler = new PageScaler(this.node.stage, this.node.scaler, { margin: 32 }).start();
    this.historyPanel = new HistoryPanel(this.node.history, this.node.historyBody, this.api);
    this.node.historyOpen.addEventListener('click', () => this.historyPanel.open());

    this.languages = new LanguageBar(this.node.lang, this.node.addLang, {
      onSwitch: (lang) => this.open(lang),
      onAdd: () => this.addLanguage(),
    });
  }

  say(kind, text) {
    this.node.state.dataset.kind = kind;
    this.node.state.textContent = text;
  }

  /** report an error from anywhere: one place, so none of them is silent. */
  fail(error) {
    this.say('error', error.message ?? String(error));
  }

  async start() {
    try {
      this.templates = (await this.api.templates()).templates ?? [];
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
   * of bug this interface could have.
   */
  async open(lang) {
    // Anything typed in the language being left is saved before leaving it.
    await this.autosave?.flush();
    this.lang = lang ?? '';
    this.say('saving', 'loading…');

    let answer;
    try {
      answer = await this.api.load(this.lang);
    } catch (error) {
      return this.fail(error);
    }

    this.doc = new CvDocument(answer.doc);
    // The icon control needs the set the template ships; hung on the document
    // because that is what every control already receives, and threading a
    // second argument through the composite for one control is worse.
    this.doc.icons = answer.template.icons ?? [];
    this.template = answer.template;

    this.form = new Form(this.node.form, this.doc, {
      template: this.template,
      templates: this.templates,
      actions: {
        api: this.api,
        onPhoto: (file) => this.uploadPhoto(file),
        onTemplate: (uuid) => this.switchTemplate(uuid),
        onReorder: (order) => this.reorder(order),
        onRemoveSection: (index) => this.removeSection(index),
        onDeleted: (result) => this.deleted(result),
      },
    });

    this.preview = new Preview(this.doc, this.node.page, (doc) => this.api.preview(doc, this.lang));
    this.preview.onFit = (fit) => this.report.show(fit);
    this.preview.onError = (error) => this.report.error(error.message);

    this.autosave = new Autosave(this.doc, (doc) => this.api.save(doc, this.lang)).guard();
    this.autosave.on('state', ({ kind, text }) => this.say(kind, text));
    this.autosave.on('saved', (result) => {
      // The STORED document comes back and replaces ours. The store pins the
      // template as a UUID and stamps the time, so a client keeping its own
      // idea of the document would send those back stale on the next save.
      // Replacing quietly: this is not a change the person made, and redrawing
      // the form under them would be the editor twitching on every save.
      this.doc.adopt(result.doc);
      this.report.show(new Fit(result.fit));
    });

    this.languages.show(answer.languages ?? [], this.lang);
    this.form.render();
    this.report.show(new Fit(answer.fit));
    this.retarget();
    this.say('saved', 'saved');
    this.preview.refresh();
  }

  /** The header links carry the language on screen, not the default one. */
  retarget() {
    for (const link of this.node.links) {
      const pdf = link.dataset.kind === 'pdf';
      link.href = pdf ? this.api.pdfUrl(this.lang) : this.api.pageUrl(this.lang);
    }
  }

  async uploadPhoto(file) {
    this.say('saving', 'uploading…');
    try {
      const answer = await this.api.photo(file, this.lang);
      this.doc.replace(answer.doc);
      this.report.show(new Fit(answer.fit));
      this.say('saved', 'saved');
    } catch (error) {
      this.fail(error);
    }
  }

  async switchTemplate(uuid) {
    const answer = await this.api.setTemplate(uuid, this.lang);
    this.doc.replace(answer.doc);
    // Another template is another set of sections and another field tree, so
    // the description is fetched again rather than assumed unchanged.
    await this.open(this.lang);
  }

  async reorder(order) {
    try {
      const answer = await this.api.reorderSections(order, this.lang);
      this.doc.replace(answer.doc);
      this.report.show(new Fit(answer.fit));
    } catch (error) {
      this.fail(error);
    }
  }

  removeSection(index) {
    const sections = this.doc.sections;
    const name = sections[index]?.title || 'this section';
    // Asked for, because a section is a great deal of typing and the button
    // that removes it is a pixel from the one that moves it.
    if (!confirm(`Remove “${name}” and everything in it?`)) return;
    this.doc.setSections(sections.filter((_, i) => i !== index));
  }

  async addLanguage() {
    const code = prompt('Two-letter code for the new language (en, fr, …)');
    if (!code) return;
    try {
      await this.api.addLanguage(code.trim().toLowerCase(), this.lang);
      await this.open(code.trim().toLowerCase());
    } catch (error) {
      this.fail(error);
    }
  }

  deleted(result) {
    this.autosave.release();
    Form.gone(document.body, result.grace ?? 24);
  }
}

new Editor(document).start();
