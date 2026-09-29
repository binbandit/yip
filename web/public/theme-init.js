// Applies the saved appearance before first paint (external file: the hub's
// Content-Security-Policy forbids inline scripts). Astryx's <Theme> takes over
// once the app mounts; base.css maps data-theme to color-scheme until then.
(function () {
  try {
    var t = localStorage.getItem('yip.theme');
    if (t === 'day') document.documentElement.dataset.theme = 'light';
    if (t === 'night') document.documentElement.dataset.theme = 'dark';
    var d = localStorage.getItem('yip.density');
    if (d === 'compact') document.documentElement.dataset.density = d;
  } catch (e) {}
})();
