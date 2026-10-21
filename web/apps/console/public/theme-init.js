// Applied before the first paint, so a member who chose dark never sees a
// white flash. It is a file rather than an inline script because the
// content security policy allows only same-origin scripts.
// Its behaviour is asserted in src/lib/theme.test.ts, against this file.
(function () {
  var stored = null;
  try {
    stored = localStorage.getItem("flagnetic.theme");
  } catch (e) {
    stored = null;
  }
  var theme = stored === "light" || stored === "dark" ? stored : null;
  if (theme === null) {
    theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
})();
