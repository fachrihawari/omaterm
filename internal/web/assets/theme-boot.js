// Before the page draws: the theme it last had, so that a light one
// doesn't show dark while app.js, a module, loads; app.js keeps it up.
(function () {
  try {
    var t = JSON.parse(localStorage.getItem("omaterm.theme"));
    if (!t || !t.tokens) return;
    var root = document.documentElement;
    for (var k in t.tokens) {
      root.style.setProperty("--" + k.replace(/([A-Z])/g, "-$1").toLowerCase(), t.tokens[k]);
    }
    root.style.colorScheme = t.dark ? "dark" : "light";
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.content = t.tokens.wash || t.tokens.sidebar || (t.dark ? "#0c0f14" : "#ededee");
  } catch (e) {
    // No storage, or nothing kept: the stylesheet's own colors.
  }
})();
