// charts-bootstrap.js — per-page chart init registry shared across /tools,
// /tools/{name}, and future chart pages. Per-page scripts call
// window.kapmCharts.register('<key>', initFn); the bootstrap dispatches the
// matching key based on the [data-charts] attribute placed inside the content
// template (NOT on <body>, since htmx only swaps #content).
(function () {
  const initers = {};
  window.kapmCharts = {
    register(key, fn) { initers[key] = fn; },
    run(key) { const fn = initers[key]; if (fn) fn(); },
  };
  function runFromDOM(root) {
    const el = (root || document).querySelector('[data-charts]');
    if (el) window.kapmCharts.run(el.getAttribute('data-charts'));
  }
  document.body.addEventListener('htmx:afterSwap', function (e) {
    runFromDOM(e.detail.target);
  });
  window.addEventListener('DOMContentLoaded', function () { runFromDOM(); });
})();
