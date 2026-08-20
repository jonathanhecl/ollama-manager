"use strict";

// ---------- delete ----------
let pendingDelete = null;
let pendingConfirmResolve = null;

function getSelectedDeleteReason() {
  const checked = document.querySelector("input[name='confirm-delete-reason']:checked");
  return checked ? String(checked.value || "").trim() : "";
}

function closeConfirmModal(result) {
  const reasonWrap = $("confirm-delete-reason-wrap");
  const reason = (reasonWrap && !reasonWrap.hidden) ? getSelectedDeleteReason() : "";
  $("confirm-modal").hidden = true;
  if (pendingConfirmResolve) {
    const resolve = pendingConfirmResolve;
    pendingConfirmResolve = null;
    resolve({ ok: !!result, reason });
  }
}

function askConfirm({ title, text, okText, okClass = "primary", mono = "", showDeleteReason = false }) {
  if (pendingConfirmResolve) closeConfirmModal(false);
  $("confirm-title").textContent = title || t("confirm.title");
  const safe = escapeHtml(text || "").replace(
    mono ? escapeHtml(mono) : "{__NO_MONO__}",
    mono ? `<span class="mono">${escapeHtml(mono)}</span>` : "{__NO_MONO__}",
  );
  $("confirm-text").innerHTML = safe;
  const reasonWrap = $("confirm-delete-reason-wrap");
  if (reasonWrap) {
    reasonWrap.hidden = !showDeleteReason;
    reasonWrap.querySelectorAll("input[name='confirm-delete-reason']").forEach((r) => { r.checked = false; });
  }
  const ok = $("confirm-ok");
  ok.textContent = okText || t("confirm.title");
  ok.className = okClass;
  $("confirm-modal").hidden = false;
  return new Promise((resolve) => { pendingConfirmResolve = resolve; });
}

async function confirmDelete(name) {
  pendingDelete = name;
  // Let the user know how many artifacts this model generated; they will be
  // removed together with the model so no remnants are left behind.
  let artifactNote = "";
  try {
    const d = await api("/api/models/" + encodeURIComponent(name));
    if (d && d.artifact_count > 0) {
      const label = d.artifact_count === 1
        ? t("confirm.delete_artifacts_one")
        : t("confirm.delete_artifacts_other", { count: d.artifact_count });
      artifactNote = `\n\n${label}`;
      if (d.artifact_bytes > 0) artifactNote += ` (${fmtBytes(d.artifact_bytes)})`;
    }
  } catch (e) {
    // Artifact info is best-effort; the delete still works without it.
  }
  // Substitute {name} ourselves so we can wrap it in a mono span.
  const text = t("confirm.delete_text", { name: "{__NAME__}" }) + artifactNote;
  askConfirm({
    title: t("detail.delete_title"),
    text: text.replace("{__NAME__}", name),
    okText: t("action.delete"),
    okClass: "danger",
    mono: name,
    showDeleteReason: true,
  }).then(async ({ ok, reason }) => {
    const delName = pendingDelete;
    pendingDelete = null;
    if (!ok || !delName) return;
    try {
      const res = await api("/api/models/" + encodeURIComponent(delName), {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reason: reason || "" }),
      });
      const extra = (res && res.deleted_artifacts > 0)
        ? ` · ${t("toast.deleted_artifacts", { count: res.deleted_artifacts })}`
        : "";
      toast(t("toast.deleted", { name: delName }) + extra, "success");
      if (activeName === delName) { $("detail-panel").hidden = true; activeName = null; }
      refreshModels();
    } catch (e) {
      toast(t("toast.delete_error", { msg: e.message }), "error");
    }
  });
}
$("confirm-cancel").addEventListener("click", () => { pendingDelete = null; closeConfirmModal(false); });
$("confirm-ok").addEventListener("click", () => closeConfirmModal(true));

