/**
 * form.ts — the left-hand column: the CV as a form.
 *
 * Its one job is to turn a template description plus a document into controls,
 * and to redraw when the SHAPE changes and never when a value does. It is an
 * observer of the document like the preview and the autosave, and knows nothing
 * about either.
 */

import { el, replace } from '../lib/dom.ts';
import { blankOf, build, panel } from './controls.ts';
import type { UploadPhoto } from './controls.ts';
import { AddSection, DeletePanel, SectionBar, SharePanel, TemplatePicker } from './panels.ts';
import type { Api } from './api.ts';
import type { CvDocument } from '../model/document.ts';
import type { CvSection, DeleteAnswer, Template, TemplateSummary } from '../model/api.ts';

/** What the form can ask the editor to do, stated as one contract. */
export interface FormActions {
  api: Api;
  onPhoto: UploadPhoto;
  onTemplate: (uuid: string) => Promise<void>;
  onReorder: (order: number[]) => void;
  onRemoveSection: (index: number) => void;
  onDeleted: (answer: DeleteAnswer) => void;
  onError: (error: unknown) => void;
}

export class Form {
  constructor(
    private readonly node: HTMLElement,
    private readonly doc: CvDocument,
    private readonly template: Template,
    private readonly templates: TemplateSummary[],
    private readonly actions: FormActions,
  ) {
    // Redrawn on shape only. On `value` the field being typed into would be
    // rebuilt under the cursor, and the caret would jump to the end after every
    // letter — which is exactly what happened when the two were one event.
    doc.on('shape', () => this.render());
  }

  render(): void {
    // The panels somebody opened stay open, and the scroll position stays put.
    // A form that collapses itself on every structural change is a form that
    // loses your place every time you add a bullet.
    const scroll = this.node.scrollTop;
    const open = [...this.node.querySelectorAll('details.panel')].map(
      (d) => (d as HTMLDetailsElement).open,
    );
    // First draw: nothing has been opened yet, so Identity is. A form that
    // opens entirely closed is a column of headings, and the first thing
    // anybody does is click the first one.
    const first = open.length === 0;
    let index = 0;
    const wasOpen = (): boolean => open[index++] ?? false;

    replace(this.node, [
      this.identity(first || wasOpen()),
      ...this.sections(wasOpen),
      panel('Add a section', null, [this.addSection()], false),
      panel('Settings', null, this.settings(), false),
      panel('Share', null, [new SharePanel(this.actions.api).render()], false),
      panel('Delete this CV', null, [
        new DeletePanel(
          this.doc, this.actions.api, this.actions.onDeleted, this.actions.onError,
        ).render(),
      ], false),
    ]);
    this.node.scrollTop = scroll;
  }

  private identity(open: boolean): HTMLElement {
    const fields = this.template.identity.map((field) =>
      build(field, `content.identity.${field.key}`, this.doc, this.actions.onPhoto).render());
    return panel('Identity', null, fields, open);
  }

  private sections(wasOpen: () => boolean): HTMLElement[] {
    const sections = this.doc.sections;
    return sections.map((section: CvSection, position: number) => {
      const slot = this.template.sections.find((s) => s.type === section.type);
      const title = section.title || slot?.label || section.id;
      const body = [
        new SectionBar(
          position, sections.length,
          this.actions.onReorder, this.actions.onRemoveSection,
        ).render(),
        ...(slot?.fields ?? []).map((field) =>
          build(field, `content.sections[${position}].${field.key}`, this.doc).render()),
      ];
      return panel(title, section.type, body, wasOpen());
    });
  }

  private addSection(): HTMLElement {
    return new AddSection(this.template, (type) => {
      const slot = this.template.sections.find((s) => s.type === type);
      if (!slot) return;
      const section: Record<string, unknown> = {};
      for (const field of slot.fields) section[field.key] = blankOf(field);
      section.type = slot.type;
      section.column = slot.column;
      // A stable identifier, made here because the history, the overflow report
      // and the reorder route all address a section by it. Time-based rather
      // than counted: two sections of one kind added and one removed would
      // otherwise reuse an id, and the journal would merge two unrelated edits.
      section.id = `${slot.type}-${Date.now().toString(36)}`;
      this.doc.setSections(this.doc.sections.concat([section as unknown as CvSection]));
    }).render();
  }

  private settings(): HTMLElement[] {
    return [
      new TemplatePicker(
        this.templates, this.template.uuid, this.actions.onTemplate,
      ).render(),
      ...this.template.meta.map((field) =>
        build(field, `meta.${field.key}`, this.doc).render()),
    ];
  }

  /** gone replaces the form once the CV it was editing no longer exists. */
  static gone(root: HTMLElement, hours: number): void {
    replace(root, [
      el('div', { class: 'gone' }, [
        el('h1', { text: 'Deleted.' }),
        el('p', { text:
          `This CV has been set aside and will be erased for good in about ` +
          `${hours} hours. Until then an administrator can put it back. ` +
          `Your links no longer work.` }),
      ]),
    ]);
  }
}
