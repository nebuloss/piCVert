/**
 * panes.ts — one pane at a time, on a screen too narrow for two.
 *
 * # WHY THIS EXISTS
 *
 * The editor is a form beside a preview. Below about 900 px they stacked, and
 * measured on a 375x667 phone the result was 97 pixels of form above 573
 * pixels of preview: the field being typed into was a sliver, under a
 * rendering of an A4 page far too small to read. Neither pane was usable, so
 * the editor was desktop-only without ever saying so.
 *
 * Both panes still exist and both still update — the preview is not paused or
 * unmounted, because a preview that has to be rebuilt when it is revealed is a
 * preview that is stale exactly when somebody wants to look at it. Only the
 * display is switched.
 *
 * # WHY `hidden` AND NOT A CLASS
 *
 * `hidden` takes the pane out of the accessibility tree as well as out of the
 * layout, so a screen reader is not offered a form nobody can see. The
 * stylesheet only has to say `display: none` for it, which it must, since the
 * panes are flex children and flex overrides `hidden`'s default.
 */

import { need } from '../lib/dom.ts';

/** Which pane the phone layout is showing. */
export type Pane = 'edit' | 'preview';

export class Panes {
  private readonly tabs: { button: HTMLButtonElement; panel: HTMLElement; name: Pane }[];

  constructor(
    root: Document,
    /** Called when the preview becomes visible, so it can be re-measured. */
    private readonly onShow?: (pane: Pane) => void,
  ) {
    this.tabs = [
      {
        name: 'edit',
        button: need<HTMLButtonElement>(root, '#pane-edit'),
        panel: need(root, '#form'),
      },
      {
        name: 'preview',
        button: need<HTMLButtonElement>(root, '#pane-preview'),
        panel: need(root, '#preview'),
      },
    ];
    for (const tab of this.tabs) {
      tab.button.addEventListener('click', () => this.show(tab.name));
    }

    // The switch is only meaningful while the panes are stacked. Above that
    // width both are visible and neither may be hidden, or a window resized
    // from narrow to wide would keep one pane switched off for good.
    this.narrow.addEventListener('change', () => this.apply());
    this.apply();
  }

  private readonly narrow = window.matchMedia('(max-width: 760px)');
  private current: Pane = 'edit';

  show(pane: Pane): void {
    this.current = pane;
    this.apply();
    if (this.narrow.matches) this.onShow?.(pane);
  }

  private apply(): void {
    const wide = !this.narrow.matches;
    for (const tab of this.tabs) {
      const on = wide || tab.name === this.current;
      tab.panel.hidden = !on;
      tab.button.setAttribute('aria-selected', String(tab.name === this.current));
    }
  }
}
