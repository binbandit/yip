// jsdom lacks a few browser APIs the client uses; provide minimal versions.
if (typeof window !== 'undefined') {
  // Width queries answer from window.innerWidth and notify on 'resize', so a
  // test that calls setViewport() moves AppShell's breakpoint and the store's
  // `narrow` together, as a browser would. Everything else never matches.
  if (!window.matchMedia) {
    const evaluate = (query: string) => {
      const max = /\(max-width:\s*(\d+)px\)/.exec(query);
      const min = /\(min-width:\s*(\d+)px\)/.exec(query);
      if (!max && !min) return false;
      return (!max || window.innerWidth <= Number(max[1])) && (!min || window.innerWidth >= Number(min[1]));
    };
    type Listener = (e: MediaQueryListEvent) => void;
    // Only lists someone is listening to are re-evaluated, so unused ones can be collected.
    const watched = new Set<() => void>();
    window.addEventListener('resize', () => {
      for (const update of watched) update();
    });
    window.matchMedia = ((query: string) => {
      const listeners = new Set<Listener>();
      const update = () => {
        const matches = evaluate(query);
        if (matches === mql.matches) return;
        mql.matches = matches;
        for (const fn of listeners) fn({ matches, media: query } as MediaQueryListEvent);
      };
      const add = (fn: Listener) => {
        listeners.add(fn);
        watched.add(update);
      };
      const remove = (fn: Listener) => {
        listeners.delete(fn);
        if (!listeners.size) watched.delete(update);
      };
      const mql = {
        matches: evaluate(query),
        media: query,
        onchange: null,
        addListener: add,
        removeListener: remove,
        addEventListener: (_: string, fn: Listener) => add(fn),
        removeEventListener: (_: string, fn: Listener) => remove(fn),
        dispatchEvent: () => false,
      };
      return mql;
    }) as unknown as typeof window.matchMedia;
  }
  // Astryx measures overflow (tab lists, truncation); jsdom has no layout, so
  // nothing is ever observed to change.
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  Element.prototype.scrollIntoView ??= function () {};
  Element.prototype.scrollTo ??= function () {} as typeof Element.prototype.scrollTo;
  // jsdom defines window.scrollTo only to report it as unimplemented (Astryx's
  // scroll lock restores the page position with it).
  window.scrollTo = (() => {}) as typeof window.scrollTo;
  if (typeof CSS === 'undefined' || !CSS.escape) {
    (globalThis as { CSS?: unknown }).CSS = { escape: (s: string) => s.replace(/["\\]/g, '\\$&') };
  }
  const dlg = (globalThis as { HTMLDialogElement?: typeof HTMLDialogElement }).HTMLDialogElement;
  if (dlg && !dlg.prototype.showModal) {
    dlg.prototype.showModal = function (this: HTMLDialogElement) {
      this.setAttribute('open', '');
    };
    dlg.prototype.close = function (this: HTMLDialogElement) {
      this.removeAttribute('open');
    };
  }
  window.requestAnimationFrame ??= ((cb: FrameRequestCallback) => setTimeout(() => cb(Date.now()), 0)) as typeof window.requestAnimationFrame;
}

/** Resizes the jsdom window the way a browser would (resize event included). */
export function setViewport(width: number): void {
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: width });
  window.dispatchEvent(new Event('resize'));
}
