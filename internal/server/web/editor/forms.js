// forms.js — the field tree, turned into controls.
//
// THERE IS NO PER-SECTION CODE HERE, and that is the whole design. The server
// sends a description of the document — categories, sub-categories, leaves and
// their kinds — and this walks it. A field added to the engine's vocabulary
// appears in the editor without a line changing here, and a template that
// composes its sections differently is described in its own terms.
//
// Every control reports through ONE callback, with the path it changed. The
// alternative — each control knowing how to write itself back into the document
// — is how a form ends up with a field that saves to the wrong place.

export function element(tag, attrs, children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  for (const child of children || []) if (child) node.appendChild(child);
  return node;
}

// at reads a value out of a document by path, and set writes one in.
//
// Paths are the SAME notation the server reports errors and history in, so a
// message about content.sections[2].items[0].role names something one can find.
export function at(root, path) {
  let cursor = root;
  for (const step of steps(path)) {
    if (cursor === null || cursor === undefined) return undefined;
    cursor = cursor[step];
  }
  return cursor;
}

export function set(root, path, value) {
  const parts = steps(path);
  let cursor = root;
  for (let i = 0; i < parts.length - 1; i++) {
    const step = parts[i];
    if (cursor[step] === undefined || cursor[step] === null) {
      cursor[step] = typeof parts[i + 1] === 'number' ? [] : {};
    }
    cursor = cursor[step];
  }
  cursor[parts[parts.length - 1]] = value;
}

function steps(path) {
  const out = [];
  for (const part of String(path).split('.')) {
    if (!part) continue;
    const name = part.replace(/\[\d+\]/g, '');
    if (name) out.push(name);
    for (const index of part.match(/\[(\d+)\]/g) || []) out.push(Number(index.slice(1, -1)));
  }
  return out;
}

// blank is what a newly added entry of a field looks like.
//
// Built from the field rather than left empty, because a group appearing as
// undefined would render as nothing at all and the person who clicked "add"
// would think the button was broken.
export function blank(field) {
  switch (field.kind) {
    case 'group': {
      const out = {};
      for (const child of field.fields || []) out[child.key] = blank(child);
      return out;
    }
    case 'list': return [];
    case 'bool': return false;
    case 'number': return field.min || 0;
    case 'enum': return field.default !== undefined ? field.default : '';
    default: return '';
  }
}

/**
 * control renders one field, at one path.
 *
 * onChange is called with (path, value) and nothing else: this module never
 * touches the document it is drawing.
 */
export function control(field, path, value, onChange) {
  switch (field.kind) {
    case 'group': return groupControl(field, path, value, onChange);
    case 'list': return listControl(field, path, value, onChange);
    case 'bool': return boolControl(field, path, value, onChange);
    case 'enum': return enumControl(field, path, value, onChange);
    case 'number': return numberControl(field, path, value, onChange);
    case 'rich': return textControl(field, path, value, onChange, true);
    default: return textControl(field, path, value, onChange, false);
  }
}

function labelled(field, inner, extra) {
  return element('div', { class: 'row' + (extra || '') }, [
    element('label', { text: field.label + (field.required ? ' *' : '') }),
    inner,
    field.help ? element('div', { class: 'help', text: field.help }) : null,
  ]);
}

function textControl(field, path, value, onChange, rich) {
  const common = {
    value: undefined,
    maxlength: field.max || undefined,
    placeholder: field.placeholder || '',
    oninput: (event) => onChange(path, event.target.value),
  };
  let input;
  if (rich) {
    input = element('textarea', { ...common, rows: field.rows || 4 });
  } else {
    input = element('input', { ...common, type: 'text' });
  }
  input.value = value === undefined || value === null ? '' : String(value);
  return labelled(field, input);
}

function numberControl(field, path, value, onChange) {
  const input = element('input', {
    type: 'number',
    min: field.min !== undefined ? field.min : undefined,
    max: field.max !== undefined ? field.max : undefined,
    // An empty number field means "no value", not zero. Writing zero back would
    // silently turn a blank gauge into a gauge reading nothing out of ten.
    oninput: (event) => onChange(path,
      event.target.value === '' ? null : Number(event.target.value)),
  });
  input.value = value === undefined || value === null ? '' : String(value);
  return labelled(field, input);
}

function boolControl(field, path, value, onChange) {
  const input = element('input', {
    type: 'checkbox',
    onchange: (event) => onChange(path, event.target.checked),
  });
  input.checked = !!value;
  return element('div', { class: 'row bool' }, [
    input, element('label', { text: field.label }),
  ]);
}

function enumControl(field, path, value, onChange) {
  const select = element('select', {
    onchange: (event) => onChange(path, event.target.value),
  });
  for (const option of field.values || []) {
    const node = element('option', { value: option.value, text: option.label || option.value });
    if (String(value || '') === String(option.value)) node.selected = true;
    select.appendChild(node);
  }
  return labelled(field, select);
}

function groupControl(field, path, value, onChange) {
  const children = (field.fields || []).map((child) =>
    control(child, path ? `${path}.${child.key}` : child.key,
      (value || {})[child.key], onChange));
  return element('div', {}, children);
}

/**
 * listControl draws an ordered series, with the controls that make it ordered.
 *
 * Move and remove act on the LIST as a whole and hand the new array back in one
 * change. Reporting "element 3 moved" would make every consumer reconstruct the
 * array, and the two reconstructions would eventually differ.
 */
function listControl(field, path, value, onChange) {
  const items = Array.isArray(value) ? value : [];
  const replace = (next) => onChange(path, next);

  const entries = items.map((item, index) => {
    const move = (to) => {
      if (to < 0 || to >= items.length) return;
      const next = items.slice();
      next.splice(to, 0, next.splice(index, 1)[0]);
      replace(next);
    };
    const bar = element('div', { class: 'entry-bar' }, [
      element('button', { type: 'button', title: 'Move up', text: '↑', onclick: () => move(index - 1) }),
      element('button', { type: 'button', title: 'Move down', text: '↓', onclick: () => move(index + 1) }),
      element('button', {
        type: 'button', title: 'Remove', text: '✕',
        onclick: () => replace(items.filter((_, i) => i !== index)),
      }),
    ]);
    return element('div', { class: 'entry' }, [
      bar, control(field.of, `${path}[${index}]`, item, onChange),
    ]);
  });

  const add = element('button', {
    type: 'button',
    text: field.addLabel || ('Add ' + field.label.toLowerCase()),
    onclick: () => replace(items.concat([blank(field.of)])),
  });

  return element('fieldset', { class: 'list' },
    [element('legend', { text: field.label })].concat(entries, [add]));
}

// panel is a collapsible block of the form.
export function panel(title, tag, children, open) {
  return element('details', { class: 'panel', open: open || undefined }, [
    element('summary', {}, [
      element('span', { class: 'grow', text: title }),
      tag ? element('span', { class: 'tag', text: tag }) : null,
    ]),
    element('div', { class: 'panel-body' }, children),
  ]);
}
