// jsdom lacks a few browser APIs the client uses; provide minimal versions.
if (typeof window !== 'undefined') {
  if (!window.matchMedia) {
    window.matchMedia = ((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  }
  Element.prototype.scrollIntoView ??= function () {};
  Element.prototype.scrollTo ??= function () {} as typeof Element.prototype.scrollTo;
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
