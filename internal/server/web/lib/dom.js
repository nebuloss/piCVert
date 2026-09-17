// dom.js — building elements, and nothing else.
//
// There is no framework here and no virtual DOM. A CV editor draws a form from
// a field tree and a preview from an iframe; the expensive part is laying the
// page out, which happens on the server, and a diffing library would add a
// dependency and a build step to save work that is not being done.
//
// What the absence of a framework costs is discipline, and this module is
// where it is paid: one way to make an element, one way to bind an event, and
// no string-concatenated HTML anywhere in the interface — which is also what
// makes an injected `<script>` in a CV impossible rather than merely unlikely.

/**
 * el builds an element declaratively.
 *
 *   el('button', { class: 'danger', text: 'Delete', onclick: fn }, [child])
 *
 * `text` sets textContent, never innerHTML. That is the whole XSS story of this
 * interface: a CV's fields reach the DOM as text nodes, so a name containing
 * `<script>` is a name containing those nine characters.
 */
export function el(tag, attrs = {}, children = []) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key === 'text') node.textContent = value;
    else if (key === 'dataset') Object.assign(node.dataset, value);
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : String(value));
  }
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
  return node;
}

/** clear empties a node. Named, because `textContent = ''` reads as a typo. */
export function clear(node) {
  node.textContent = '';
  return node;
}

/** replace swaps a node's children for new ones in one go. */
export function replace(node, children) {
  clear(node);
  for (const child of children) if (child) node.appendChild(child);
  return node;
}

/**
 * PageScaler fits a fixed 794×1123 page into whatever room it has.
 *
 * A CSS transform moves pixels without moving layout, so scaling alone leaves
 * the wrapper at its original height and a screenful of white underneath the
 * page. Correcting that is two lines and one of them is easy to forget, which
 * is why the viewer and the editor share this rather than each having their own
 * — they had their own once, and only one of them was right on a phone.
 */
export class PageScaler {
  static PAGE_WIDTH = 794;
  static PAGE_HEIGHT = 1123;

  constructor(stage, scaler, { margin = 32, max = 1 } = {}) {
    this.stage = stage;
    this.scaler = scaler;
    this.margin = margin;
    this.max = max;
    this.onResize = () => this.apply();
  }

  start() {
    window.addEventListener('resize', this.onResize);
    this.apply();
    return this;
  }

  stop() {
    window.removeEventListener('resize', this.onResize);
  }

  apply() {
    const room = this.stage.clientWidth - this.margin;
    const factor = Math.max(0.2, Math.min(this.max, room / PageScaler.PAGE_WIDTH));
    this.scaler.style.transformOrigin = 'top center';
    this.scaler.style.transform = `scale(${factor})`;
    this.scaler.style.width = `${PageScaler.PAGE_WIDTH}px`;
    this.scaler.style.height = `${PageScaler.PAGE_HEIGHT * factor}px`;
  }
}

/**
 * copy puts text on the clipboard, and says whether it managed.
 *
 * It fails on an insecure origin and when permission is refused, and both are
 * ordinary rather than exceptional — a service reached over plain HTTP on a
 * local network hits the first every time. The caller is told, so the button
 * can say "press Ctrl+C" instead of claiming to have copied something it did
 * not.
 */
export async function copy(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
