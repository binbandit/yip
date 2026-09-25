// Escape closes the topmost dismissible layer (menu, dialog, drawer, panel),
// and focus returns to whatever opened it.

interface Layer {
  id: number;
  close: () => void;
  returnFocus: HTMLElement | null;
}

const stack: Layer[] = [];
let nextId = 1;
let installed = false;

function install(): void {
  if (installed || typeof document === 'undefined') return;
  installed = true;
  document.addEventListener(
    'keydown',
    (e) => {
      if (e.key !== 'Escape' || e.defaultPrevented || stack.length === 0) return;
      // Let an open combobox/listbox handle its own Escape first.
      const t = e.target as HTMLElement | null;
      if (t?.getAttribute('aria-expanded') === 'true' && t.getAttribute('role') === 'combobox') return;
      const top = stack[stack.length - 1];
      e.preventDefault();
      e.stopPropagation();
      top.close();
    },
    true,
  );
}

/** Registers a layer; returns a function that unregisters it and restores focus. */
export function pushLayer(close: () => void, opts: { returnFocus?: HTMLElement | null } = {}): (restore?: boolean) => void {
  install();
  const layer: Layer = {
    id: nextId++,
    close,
    returnFocus: opts.returnFocus ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null),
  };
  stack.push(layer);
  return (restore = true) => {
    const i = stack.findIndex((l) => l.id === layer.id);
    if (i >= 0) stack.splice(i, 1);
    if (restore && layer.returnFocus && layer.returnFocus.isConnected) {
      const target = layer.returnFocus;
      queueMicrotask(() => target.focus({ preventScroll: true }));
    }
  };
}

export function layerCount(): number {
  return stack.length;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), summary';

export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => !el.hasAttribute('inert') && !el.closest('[inert]') && el.getClientRects().length > 0,
  );
}

const traps: HTMLElement[] = [];

/**
 * Keeps Tab focus inside root. Traps stack: only the most recently opened one
 * acts, so a confirmation dialog over an overlaid drawer works as expected.
 * Returns a cleanup function.
 */
export function trapFocus(root: HTMLElement): () => void {
  traps.push(root);
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== 'Tab' || traps[traps.length - 1] !== root) return;
    const els = focusables(root);
    if (els.length === 0) {
      e.preventDefault();
      root.focus();
      return;
    }
    const first = els[0];
    const last = els[els.length - 1];
    const active = document.activeElement as HTMLElement | null;
    if (e.shiftKey && (active === first || !root.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !root.contains(active))) {
      e.preventDefault();
      first.focus();
    }
  };
  document.addEventListener('keydown', onKey, true);
  return () => {
    document.removeEventListener('keydown', onKey, true);
    const i = traps.lastIndexOf(root);
    if (i >= 0) traps.splice(i, 1);
  };
}

export function focusFirst(root: HTMLElement, selector?: string): void {
  const target = (selector && root.querySelector<HTMLElement>(selector)) || focusables(root)[0] || root;
  target.focus({ preventScroll: true });
}
