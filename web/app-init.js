"use strict";

// ---------- init ----------
// Tests remains desktop-only, but Analytics is useful on mobile too.
if (window.innerWidth > 900) {
  $("tests-btn").hidden = false;
}
$("analytics-btn").hidden = false;
window.addEventListener("resize", () => {
  const btn = $("tests-btn");
  if (btn) btn.hidden = window.innerWidth <= 900;
  const abtn = $("analytics-btn");
  if (abtn) abtn.hidden = false;
});
