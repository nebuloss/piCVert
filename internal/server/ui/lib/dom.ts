/**
 * dom.ts — building elements, and nothing else.
 *
 * There is no framework here and no virtual DOM. A CV editor draws a form from
 * a field tree and a preview from an iframe; the expensive part is laying the
 * page out, which happens on the server, and a diffing library would add a
 * dependency to save work that is not being done.
 *
 * What the absence of a framework costs is discipline, and this module is where
 * it is paid: one way to make an element, one way to bind an event, and no
 * string-concatenated HTML anywhere in the interface — which is also what makes
 * an injected `<script>` in a CV impossible rather than merely unlikely.
 */

/** What `el` accepts as a child: an element, some text, or nothing. */
export type Child = Node | string | null | undefined | false;

/**
 * Attributes, typed loosely on purpose.
 *
 * The keys are HTML attribute names, event handlers (`onclick`) and two
 * shorthands (`text`, `dataset`). A union naming every valid attribute would be
 * hundreds of lines restating the DOM, and would still have to allow `data-*`.
 */
export interface Attrs {
  class?: string;
  text?: string;
  dataset?: Record<string, string>;
  [key: string]: unknown;
}

/**
 * el builds an element declaratively.
 *
 *   el('button', { class: 'danger', text: 'Delete', onclick: fn }, [child])
 *
 * `text` sets textContent, never innerHTML. That is the whole XSS story of this
 * interface: a CV's fields reach the DOM as text nodes, so a name containing
 * `<script>` is a name containing those nine characters.
 *
 * Generic over the tag, so `el('input', …)` is an HTMLInputElement and reading
 * `.value` off it compiles — where a bare `HTMLElement` would need a cast at
 * every call site, and a cast is a place to be wrong.
 */
export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Attrs = {},
  children: Child[] = [],
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'class') node.className = String(value);
    else if (key === 'text') node.textContent = String(value);
    else if (key === 'dataset') Object.assign(node.dataset, value);
    else if (key.startsWith('on')) {
      node.addEventListener(key.slice(2), value as EventListener);
    } else node.setAttribute(key, value === true ? '' : String(value));
  }
  append(node, children);
  return node;
}

/** append adds children, skipping the ones a conditional left empty. */
export function append(node: Node, children: Child[]): void {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
}

/** clear empties a node. Named, because `textContent = ''` reads as a typo. */
export function clear<T extends Node>(node: T): T {
  node.textContent = '';
  return node;
}

/** replace swaps a node's children for new ones in one go. */
export function replace<T extends Node>(node: T, children: Child[]): T {
  clear(node);
  append(node, children);
  return node;
}

/**
 * need finds an element that the page is required to contain.
 *
 * Throwing beats returning null. Every one of these is in a template shipped
 * beside this code, so a miss is a typo in one of the two and not a condition
 * to handle — and `querySelector` returning `T | null` would otherwise put a
 * `!` on every lookup, which is the same assertion made silently.
 */
export function need<T extends Element = HTMLElement>(
  root: ParentNode,
  selector: string,
): T {
  const found = root.querySelector<T>(selector);
  if (!found) throw new Error(`the page is missing ${selector}`);
  return found;
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
  static readonly PAGE_WIDTH = 794;
  static readonly PAGE_HEIGHT = 1123;

  private readonly onResize = (): void => this.apply();

  constructor(
    private readonly stage: HTMLElement,
    private readonly scaler: HTMLElement,
    private readonly margin = 32,
    private readonly max = 1,
  ) {}

  start(): this {
    window.addEventListener('resize', this.onResize);
    this.apply();
    return this;
  }

  stop(): void {
    window.removeEventListener('resize', this.onResize);
  }

  apply(): void {
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
 * local network hits the first every time. The caller is told, so a button can
 * say "press Ctrl+C" instead of claiming to have copied something it did not.
 */
export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}
