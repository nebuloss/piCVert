// form.js — the left-hand column: the CV as a form.
//
// Its one job is to turn a template description plus a document into controls,
// and to redraw when the SHAPE changes and never when a value does. It is an
// observer of the document like the preview and the autosave, and knows nothing
// about either.

import { el, replace } from '../lib/dom.js';
import { blankOf, build, panel } from './controls.js';
import {
  AddSection, DeletePanel, SectionBar, SharePanel, TemplatePicker,
} from './panels.js';

export class Form {
  constructor(node, doc, { template, templates, actions }) {
    this.node = node;
    this.doc = doc;
    this.template = template;
    this.templates = templates;
    this.actions = actions;

    // Redrawn on shape only. On "value" the field being typed into would be
    // rebuilt under the cursor, and the caret would jump to the end after every
    // letter — which is exactly what happened when the two were one event.
    doc.on('shape', () => this.render());
  }

  setTemplate(template, templates) {
    this.template = template;
    if (templates) this.templates = templates;
  }

  render() {
    // The panels someone opened stay open, and the scroll position stays put.
    // A form that collapses itself on every structural change is a form that
    // loses your place every time you add a bullet.
    const scroll = this.node.scrollTop;
    const open = [...this.node.querySelectorAll('details.panel')].map((d) => d.open);
    // First draw: nothing has been opened yet, so Identity is. A form that
    // opens entirely closed is a column of headings, and the first thing
    // anybody does is click the first one.
    const first = open.length === 0;
    let index = 0;
    const wasOpen = () => open[index++] ?? false;

    const blocks = [
      this.identity(first || wasOpen()),
      ...this.sections(wasOpen),
      panel('Add a section', null, [this.addSection()], false),
      panel('Settings', null, this.settings(), false),
      panel('Share', null, [new SharePanel(this.actions.api).render()], false),
      panel('Delete this CV', null,
        [new DeletePanel(this.doc, this.actions.api, this.actions.onDeleted).render()], false),
    ];

    replace(this.node, blocks);
    this.node.scrollTop = scroll;
  }

  identity(open) {
    const fields = (this.template.identity ?? []).map((field) =>
      build(field, `content.identity.${field.key}`, this.doc, this.actions.onPhoto).render());
    return panel('Identity', null, fields, open);
  }

  sections(wasOpen) {
    const sections = this.doc.sections;
    return sections.map((section, position) => {
      const slot = (this.template.sections ?? []).find((s) => s.type === section.type);
      const title = section.title || slot?.label || section.id;
      const body = [
        new SectionBar(this.doc, position, sections.length, {
          onReorder: this.actions.onReorder,
          onRemove: this.actions.onRemoveSection,
        }).render(),
        ...(slot?.fields ?? []).map((field) =>
          build(field, `content.sections[${position}].${field.key}`, this.doc).render()),
      ];
      return panel(title, section.type, body, wasOpen());
    });
  }

  addSection() {
    return new AddSection(this.template, (type) => {
      const slot = (this.template.sections ?? []).find((s) => s.type === type);
      if (!slot) return;
      const section = {};
      for (const field of slot.fields ?? []) section[field.key] = blankOf(field);
      section.type = slot.type;
      section.column = slot.column;
      // A stable identifier, made here because the history, the overflow report
      // and the reorder route all address a section by it. Time-based rather
      // than counted: two sections of one kind added and one removed would
      // otherwise reuse an id, and the journal would merge two unrelated edits.
      section.id = `${slot.type}-${Date.now().toString(36)}`;
      this.doc.setSections(this.doc.sections.concat([section]));
    }).render();
  }

  settings() {
    return [
      new TemplatePicker(this.templates, this.template.uuid, this.actions.onTemplate).render(),
      ...(this.template.meta ?? []).map((field) =>
        build(field, `meta.${field.key}`, this.doc).render()),
    ];
  }

  /** gone replaces the form once the CV it was editing no longer exists. */
  static gone(root, hours) {
    replace(root, [el('div', { class: 'gone' }, [
      el('h1', { text: 'Deleted.' }),
      el('p', { text:
        `This CV has been set aside and will be erased for good in about ` +
        `${hours} hours. Until then an administrator can put it back. ` +
        `Your links no longer work.` }),
    ])]);
  }
}
