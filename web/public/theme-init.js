// Applies the saved appearance before first paint (external file: the hub's
// Content-Security-Policy forbids inline scripts).
(function () {
  try {
    var t = localStorage.getItem('yip.theme');
    if (t === 'day' || t === 'night') document.documentElement.dataset.theme = t;
    var d = localStorage.getItem('yip.density');
    if (d === 'compact') document.documentElement.dataset.density = d;
  } catch (e) {}
})();
