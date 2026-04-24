// Format <time class="js-localtime"> elements in the browser's locale.
// Element shape: <time class="js-localtime" datetime="<ISO8601>" data-format="datetime|date|time">fallback</time>.
// Idempotent: elements are marked with data-localized="1" after processing.
(function () {
  var optsByFormat = {
    datetime: { dateStyle: "medium", timeStyle: "medium" },
    date: { dateStyle: "medium" },
    time: { timeStyle: "medium" },
  };

  function format(root) {
    var scope = root && root.querySelectorAll ? root : document;
    var nodes = scope.querySelectorAll('time.js-localtime:not([data-localized="1"])');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var iso = el.getAttribute("datetime");
      if (!iso) continue;
      var d = new Date(iso);
      if (isNaN(d.getTime())) continue;
      var fmt = el.getAttribute("data-format") || "datetime";
      var opts = optsByFormat[fmt] || optsByFormat.datetime;
      try {
        el.textContent = d.toLocaleString(undefined, opts);
      } catch (e) {
        el.textContent = d.toLocaleString();
      }
      el.setAttribute("data-localized", "1");
    }
  }

  // Pre-localize the incoming fragment BEFORE morph runs, so unchanged <time>
  // elements have matching textContent and morph skips them (no UTC flash).
  document.addEventListener("DOMContentLoaded", function () {
    format(document);
    document.body.addEventListener("htmx:beforeSwap", function (evt) {
      var xhr = evt && evt.detail && evt.detail.xhr;
      if (!xhr || !xhr.responseText) return;
      var tpl = document.createElement("template");
      tpl.innerHTML = xhr.responseText;
      format(tpl.content);
      evt.detail.serverResponse = tpl.innerHTML;
    });
    document.body.addEventListener("htmx:afterSettle", function (evt) {
      format((evt && evt.detail && evt.detail.elt) || document);
    });
    document.body.addEventListener("htmx:sseMessage", function () { format(document); });
  });
})();
