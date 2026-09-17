// app.js — the editor.
//
// ONE DOCUMENT IN MEMORY, one save queue, one preview. Every control reports a
// path and a value; the document is updated, the preview is redrawn after a
// pause, and the save follows. Nothing else holds state, so there is no second
// copy of the CV to fall out of step with the first.

import { api, base } from './api.js';
import { at, blank, control, element, panel, set } from './forms.js';

const view = {
  form: document.getElementById('form'),
  fit: document.getElementById('fit'),
  page: document.getElementById('page'),
  scaler: document.getElementById('scaler'),
  stage: document.getElementById('stage'),
  state: document.getElementById('state'),
  lang: document.getElementById('lang'),
  addLang: document.getElementById('add-lang'),
  history: document.getElementById('history'),
  historyBody: document.getElementById('history-body'),
  historyOpen: document.getElementById('history-open'),
};

const state = {
  doc: null,
  template: null,
  templates: [],
  languages: [],
  lang: '',
};

// --- saying what is happening ----------------------------------------------

function say(kind, text) {
  view.state.dataset.kind = kind;
  view.state.textContent = text;
}

// --- the save queue ---------------------------------------------------------
//
// Saves are COALESCED and SERIALISED. Typing produces a change per keystroke,
// and firing a request for each would both flood the service and let two writes
// land out of order — the second-to-last keystroke overwriting the last. So a
// change schedules one save, and a save that finds another change waiting runs
// again when it is done.

const save = {
  timer: 0,
  running: false,
  dirty: false,
};

function schedule() {
  save.dirty = true;
  clearTimeout(save.timer);
  save.timer = setTimeout(run, 900);
  preview.schedule();
}

async function run() {
  if (save.running || !save.dirty) return;
  save.running = true;
  save.dirty = false;
  say('saving', 'saving…');
  try {
    const answer = await api.save(state.doc, state.lang);
    // The stored document comes back, and it is what we keep: the store pins
    // the template as a UUID and stamps the time, and a client holding its own
    // idea of the document would send those back stale on the next save.
    state.doc = answer.doc;
    showFit(answer.fit);
    say('saved', 'saved');
  } catch (error) {
    // Left dirty on purpose: the next change retries, and nothing typed is
    // dropped because one request failed.
    save.dirty = true;
    say('error', error.message);
  } finally {
    save.running = false;
    if (save.dirty) setTimeout(run, 1500);
  }
}

// --- the live preview -------------------------------------------------------
//
// Drawn from the UNSAVED document, so what is on screen is what is being typed
// rather than what was last stored. This is the hot path of the whole service —
// it runs on every pause in typing — and it is the reason the engine lays out
// once instead of twice.

const preview = {
  timer: 0,
  running: false,
  again: false,
  schedule() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.run(), 250);
  },
  async run() {
    if (this.running) { this.again = true; return; }
    this.running = true;
    try {
      const answer = await api.preview(state.doc, state.lang);
      draw(answer.html);
      showFit(answer.fit);
    } catch (error) {
      showFit({ ok: false, summary: error.message, margins: {} });
    } finally {
      this.running = false;
      if (this.again) { this.again = false; this.run(); }
    }
  },
};

function draw(html) {
  // Written into the frame rather than pointed at a URL: the page has no
  // address until it is saved, and a preview that had to be saved first would
  // not be a preview.
  const frame = view.page.contentDocument;
  frame.open();
  frame.write(html);
  frame.close();
}

function showFit(fit) {
  if (!fit) return;
  view.fit.dataset.ok = fit.ok === false ? 'false' : 'true';
  view.fit.textContent = '';
  view.fit.appendChild(element('b', { text: fit.summary || '' }));
  const margins = Object.entries(fit.margins || {});
  if (margins.length) {
    view.fit.appendChild(document.createTextNode('  ·  ' + margins
      .map(([name, room]) => `${name}: ${Math.round(room)} px left`)
      .join('  ·  ')));
  }
  if (fit.over && fit.over.length) {
    view.fit.appendChild(document.createTextNode('  ·  shorten: ' + fit.over.join(', ')));
  }
}

// Scale the preview to the room it has, and correct the height the transform
// did not change — a transform moves pixels without moving layout.
function scale() {
  const room = view.stage.clientWidth - 32;
  const factor = Math.max(0.2, Math.min(1, room / 794));
  view.scaler.style.transform = `scale(${factor})`;
  view.scaler.style.width = '794px';
  view.scaler.style.height = `${1123 * factor}px`;
}
window.addEventListener('resize', scale);

// --- building the form ------------------------------------------------------

function change(path, value) {
  set(state.doc, path, value);
  schedule();
  // A change to the shape of the document — a list gaining or losing an entry —
  // has to redraw the form; a change to a value must NOT, or the field being
  // typed into would lose its cursor on every keystroke.
  if (Array.isArray(value) || (value && typeof value === 'object')) build();
}

function build() {
  const scroll = view.form.scrollTop;
  const open = [...view.form.querySelectorAll('details.panel')].map((d) => d.open);
  view.form.textContent = '';
  let index = 0;
  const next = () => (open.length ? open[index++] !== false : index++ === 0);

  view.form.appendChild(panel('Identity', null, [
    photoRow(),
    ...(state.template.identity || []).map((field) =>
      control(field, `content.identity.${field.key}`,
        at(state.doc, `content.identity.${field.key}`), change)),
  ], next()));

  const sections = (state.doc.content && state.doc.content.sections) || [];
  sections.forEach((section, position) => {
    const slot = (state.template.sections || []).find((s) => s.type === section.type);
    const fieldList = slot ? slot.fields : [];
    const title = section.title || (slot && slot.label) || section.id;
    const body = [
      sectionBar(position, sections.length),
      ...fieldList.map((field) =>
        control(field, `content.sections[${position}].${field.key}`,
          at(state.doc, `content.sections[${position}].${field.key}`), change)),
    ];
    view.form.appendChild(panel(title, section.type, body, next()));
  });

  view.form.appendChild(panel('Add a section', null, [addSection()], false));
  view.form.appendChild(panel('Settings', null, [
    templateRow(),
    ...(state.template.meta || []).map((field) =>
      control(field, `meta.${field.key}`, at(state.doc, `meta.${field.key}`), change)),
  ], false));

  view.form.scrollTop = scroll;
}

function sectionBar(position, total) {
  const move = (to) => {
    if (to < 0 || to >= total) return;
    const order = [...Array(total).keys()];
    order.splice(to, 0, order.splice(position, 1)[0]);
    api.reorderSections(order, state.lang).then((answer) => {
      state.doc = answer.doc;
      showFit(answer.fit);
      build();
      preview.schedule();
    }).catch((error) => say('error', error.message));
  };
  return element('div', { class: 'entry-bar' }, [
    element('button', { type: 'button', text: '↑ section', onclick: () => move(position - 1) }),
    element('button', { type: 'button', text: '↓ section', onclick: () => move(position + 1) }),
    element('button', {
      type: 'button', text: '✕ section',
      onclick: () => {
        // Asked for, because a section is a great deal of typing and the
        // difference between this button and the one beside it is one pixel.
        if (!confirm('Remove this section and everything in it?')) return;
        const sections = state.doc.content.sections.slice();
        sections.splice(position, 1);
        state.doc.content.sections = sections;
        schedule();
        build();
      },
    }),
  ]);
}

function addSection() {
  const select = element('select', {});
  for (const slot of state.template.sections || []) {
    select.appendChild(element('option', {
      value: slot.type, text: slot.label || slot.type,
    }));
  }
  const add = element('button', {
    type: 'button', text: 'Add',
    onclick: () => {
      const slot = (state.template.sections || []).find((s) => s.type === select.value);
      if (!slot) return;
      const section = {};
      for (const field of slot.fields || []) section[field.key] = blank(field);
      section.type = slot.type;
      section.column = slot.column;
      // A stable identifier, made here: it is what the history, the layout and
      // the reorder API all address a section by.
      section.id = `${slot.type}-${Date.now().toString(36)}`;
      state.doc.content.sections = (state.doc.content.sections || []).concat([section]);
      schedule();
      build();
    },
  });
  return element('div', { class: 'row' }, [select, add]);
}

function templateRow() {
  const select = element('select', {
    onchange: async () => {
      try {
        const answer = await api.setTemplate(select.value, state.lang);
        state.doc = answer.doc;
        showFit(answer.fit);
        await load(state.lang);
      } catch (error) {
        // Refused rather than applied: a template that cannot draw one of the
        // sections would lose it, and losing a section is losing work.
        say('error', error.message);
        select.value = state.template.uuid;
      }
    },
  });
  for (const t of state.templates) {
    const option = element('option', { value: t.uuid, text: t.title || t.name });
    if (t.uuid === state.template.uuid) option.selected = true;
    select.appendChild(option);
  }
  return element('div', { class: 'row' }, [
    element('label', { text: 'Template' }), select,
  ]);
}

function photoRow() {
  const input = element('input', {
    type: 'file', accept: 'image/png,image/jpeg,image/webp',
    onchange: async (event) => {
      const file = event.target.files && event.target.files[0];
      if (!file) return;
      say('saving', 'uploading…');
      try {
        const answer = await api.photo(file, state.lang);
        state.doc = answer.doc;
        showFit(answer.fit);
        say('saved', 'saved');
        preview.schedule();
      } catch (error) {
        say('error', error.message);
      }
    },
  });
  return element('div', { class: 'row' }, [
    element('label', { text: 'Portrait' }), input,
  ]);
}

// --- languages --------------------------------------------------------------

function showLanguages() {
  view.lang.textContent = '';
  for (const entry of state.languages) {
    const option = element('option', {
      value: entry.variant || '',
      text: entry.lang.toUpperCase() + (entry.isDefault ? ' (default)' : ''),
    });
    if ((entry.variant || '') === state.lang) option.selected = true;
    view.lang.appendChild(option);
  }
}

view.lang.addEventListener('change', () => load(view.lang.value));

view.addLang.addEventListener('click', async () => {
  const code = prompt('Two-letter code for the new language (en, fr, …)');
  if (!code) return;
  try {
    await api.addLanguage(code.trim().toLowerCase(), state.lang);
    await load(code.trim().toLowerCase());
  } catch (error) {
    say('error', error.message);
  }
});

// --- history ----------------------------------------------------------------

view.historyOpen.addEventListener('click', async () => {
  view.historyBody.textContent = 'loading…';
  view.history.showModal();
  try {
    const answer = await api.history();
    view.historyBody.textContent = '';
    if (!answer.entries.length) {
      view.historyBody.appendChild(element('p', { text: 'Nothing recorded yet.' }));
      return;
    }
    for (const entry of answer.entries) {
      view.historyBody.appendChild(entryLine(entry));
    }
  } catch (error) {
    view.historyBody.textContent = error.message;
  }
});

function entryLine(entry) {
  const trail = (entry.trail || [])
    .map((crumb) => crumb.label + (crumb.n ? ` #${crumb.n}` : ''))
    .join(' › ');
  const line = element('div', { class: 'entryline' }, [
    element('div', { class: 'trail', text: `${when(entry.at)} — ${trail}` }),
  ]);
  const shown = (value) => (value === null || value === undefined || value === ''
    ? '(nothing)' : String(value));
  if (entry.kind === 'move') {
    line.appendChild(element('div', { text: `moved from ${entry.before} to ${entry.after}` }));
  } else {
    line.appendChild(element('div', {}, [
      element('del', { text: shown(entry.before) }),
      document.createTextNode(' → '),
      element('ins', { text: shown(entry.after) }),
    ]));
  }
  return line;
}

function when(stamp) {
  const date = new Date(stamp);
  return isNaN(date) ? stamp : date.toLocaleString();
}

// --- loading ----------------------------------------------------------------

async function load(lang) {
  state.lang = lang || '';
  say('saving', 'loading…');
  try {
    const answer = await api.load(state.lang);
    state.doc = answer.doc;
    state.template = answer.template;
    state.languages = answer.languages || [];
    showLanguages();
    showFit(answer.fit);
    build();
    say('saved', 'saved');
    preview.run();
  } catch (error) {
    say('error', error.message);
  }
}

(async function start() {
  try {
    const answer = await api.templates();
    state.templates = answer.templates || [];
  } catch { state.templates = []; }
  await load(new URLSearchParams(location.search).get('lang') || '');
  scale();
  // The links out of the editor carry the language being edited, so opening the
  // PDF from here shows the document on screen rather than the default one.
  for (const link of document.querySelectorAll('#bar a.button')) {
    const url = new URL(link.href, location.origin);
    if (state.lang) url.searchParams.set('lang', state.lang);
    link.href = url.pathname + url.search;
  }
})();

export { base };
