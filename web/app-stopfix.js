// Stop-token auto-fix: detects the stop sequences that make an imported model
// answer with an empty reply and rebuilds the model in place with the ones that
// do not cut the answer. Backed by /api/model-stop-fix/*.

let stopFixState = null;

function stopFixVerdictClass(verdict) {
  if (verdict === "safe") return "pill-good";
  if (verdict === "partial") return "pill-warn";
  if (verdict === "inactive") return "pill-muted";
  return "pill-bad";
}

function stopFixDiagnosisClass(diagnosis) {
  if (diagnosis === "stops") return "pill-warn";
  if (diagnosis === "ok") return "pill-good";
  if (diagnosis === "unsupported") return "pill-muted";
  return "pill-bad";
}

function stopFixDiagnosisLabel(diagnosis) {
  const key = `stopfix.diagnosis.${diagnosis}`;
  return t(key) || diagnosis;
}

function probePreview(probe) {
  const value = String(probe?.content || "").trim();
  if (!value) {
    const label = probe?.reasoning ? t("stopfix.reasoning_only") : t("stopfix.blank");
    return `<span class="stopfix-empty">${escapeHtml(label)}</span>`;
  }
  const capped = value.length > 600 ? value.slice(0, 600) + "…" : value;
  return `<pre class="stopfix-reply">${escapeHtml(capped)}</pre>`;
}

function renderStopFixShell(model) {
  return `<div class="repair-card stopfix-card">
    <div class="stopfix-model mono">${escapeHtml(model)}</div>
    <div id="stopfix-status" class="muted stopfix-status">
      <span class="stopfix-spinner" aria-hidden="true"></span>${escapeHtml(t("stopfix.running"))}
    </div>
    <div id="stopfix-result"></div>
  </div>`;
}

function renderStopFixPreview(preview) {
  const host = $("stopfix-result");
  if (!host) return;
  const status = $("stopfix-status");
  if (status) status.textContent = "";

  const notes = (preview.notes || []).map((n) => `<p class="stopfix-note">${escapeHtml(n)}</p>`).join("");
  const warnings = (preview.warnings || []).map((w) => `<p class="stopfix-warning">⚠️ ${escapeHtml(w)}</p>`).join("");

  const probeBlock = `<div class="stopfix-probes">
    <div class="stopfix-probe">
      <div class="stopfix-probe-title">${escapeHtml(t("stopfix.probe_current"))}</div>
      ${probePreview(preview.current_probe)}
    </div>
    <div class="stopfix-probe">
      <div class="stopfix-probe-title">${escapeHtml(t("stopfix.probe_nostop"))}</div>
      ${probePreview(preview.no_stop_probe)}
    </div>
  </div>`;

  const rows = (preview.candidates || []).map((c) => `<tr>
      <td class="mono stopfix-stop">${escapeHtml(c.stop)}</td>
      <td>${escapeHtml(t(`stopfix.source.${c.source}`) || c.source)}</td>
      <td><span class="pill stopfix-verdict ${stopFixVerdictClass(c.verdict)}">${escapeHtml(t(`stopfix.verdict.${c.verdict}`) || c.verdict)}</span></td>
      <td class="muted">${escapeHtml(c.evidence)}</td>
    </tr>`).join("");
  const table = rows
    ? `<table class="stopfix-table">
        <thead><tr>
          <th>${escapeHtml(t("stopfix.col_stop"))}</th>
          <th>${escapeHtml(t("stopfix.col_source"))}</th>
          <th>${escapeHtml(t("stopfix.col_verdict"))}</th>
          <th>${escapeHtml(t("stopfix.col_evidence"))}</th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>`
    : "";

  const stopsValue = escapeHtml((preview.proposed_stops || []).join("\n"));
  const stopsBlock = preview.supported
    ? `<label class="stopfix-stops-label">
        <span>${escapeHtml(t("stopfix.stops_label"))}</span>
        <textarea id="stopfix-stops" class="repair-stops stopfix-stops" spellcheck="false" placeholder="${escapeHtml(t("stopfix.stops_placeholder"))}">${stopsValue}</textarea>
      </label>`
    : "";

  const diff = [
    ...(preview.removed_stops || []).map((s) => `<span class="pill stopfix-removed" title="${escapeHtml(t("stopfix.removed"))}">− ${escapeHtml(s)}</span>`),
    ...(preview.added_stops || []).map((s) => `<span class="pill stopfix-added" title="${escapeHtml(t("stopfix.added"))}">+ ${escapeHtml(s)}</span>`),
  ].join("");

  const modelfile = preview.modelfile
    ? `<details class="stopfix-details">
        <summary>${escapeHtml(t("stopfix.modelfile"))}</summary>
        <textarea class="repair-preview stopfix-modelfile" readonly spellcheck="false">${escapeHtml(preview.modelfile)}</textarea>
      </details>`
    : "";

  const canApply = !!preview.needs_fix && !!preview.supported;
  const applyLabel = preview.needs_fix ? t("stopfix.apply") : t("stopfix.nothing_to_fix");

  host.innerHTML = `
    <div class="stopfix-head">
      <span class="pill stopfix-badge ${stopFixDiagnosisClass(preview.diagnosis)}">${escapeHtml(stopFixDiagnosisLabel(preview.diagnosis))}</span>
      ${diff ? `<div class="stopfix-diff">${diff}</div>` : ""}
    </div>
    ${notes}
    ${warnings}
    ${probeBlock}
    ${table}
    ${stopsBlock}
    ${modelfile}
    <div class="repair-actions stopfix-actions">
      <button type="button" class="primary" id="stopfix-apply-btn"${canApply ? "" : " disabled"}>${escapeHtml(applyLabel)}</button>
      <button type="button" class="ghost" id="stopfix-retry-btn" hidden>${escapeHtml(t("stopfix.retry"))}</button>
    </div>
    <div id="stopfix-apply-result" class="stopfix-apply-result"></div>`;

  $("stopfix-apply-btn")?.addEventListener("click", applyStopFix);
  $("stopfix-retry-btn")?.addEventListener("click", retryStopFixChat);
}

async function runStopFixPreview() {
  if (!stopFixState) return;
  const status = $("stopfix-status");
  try {
    const preview = await api("/api/model-stop-fix/preview", {
      method: "POST",
      body: { model: stopFixState.model, prompt: stopFixState.prompt || "" },
    });
    if (!stopFixState) return;
    stopFixState.preview = preview;
    renderStopFixPreview(preview);
  } catch (err) {
    if (status) status.textContent = t("state.error_prefix") + (err.message || String(err));
  }
}

async function applyStopFix() {
  if (!stopFixState || stopFixState.applying) return;
  const model = stopFixState.model;
  const input = $("stopfix-stops");
  const stops = input
    ? input.value.split("\n").map((s) => s.trim()).filter((s) => s.length > 0)
    : (stopFixState.preview?.proposed_stops || []);

  const { ok } = await askConfirm({
    title: t("stopfix.apply"),
    text: t("stopfix.confirm", { name: model }),
    okText: t("stopfix.apply"),
    okClass: "primary",
    mono: model,
  });
  if (!ok) return;

  const btn = $("stopfix-apply-btn");
  const resultHost = $("stopfix-apply-result");
  stopFixState.applying = true;
  if (btn) {
    btn.disabled = true;
    btn.textContent = t("stopfix.applying");
  }
  try {
    const res = await api("/api/model-stop-fix/apply", {
      method: "POST",
      body: { model, prompt: stopFixState.prompt || "", stops, confirm: true },
    });
    if (resultHost) {
      const reply = res?.verify?.content || "";
      resultHost.innerHTML = `
        <p class="stopfix-ok">✅ ${escapeHtml(t("stopfix.applied", { name: model }))}</p>
        <div class="stopfix-probe">
          <div class="stopfix-probe-title">${escapeHtml(t("stopfix.probe_after"))}</div>
          ${probePreview({ content: reply })}
        </div>
        ${res?.backup_path ? `<p class="muted stopfix-backup">${escapeHtml(t("stopfix.backup", { path: res.backup_path }))}</p>` : ""}`;
    }
    if (stopFixState.msgId) {
      const retry = $("stopfix-retry-btn");
      if (retry) retry.hidden = false;
    }
    if (typeof refreshModels === "function") await refreshModels().catch(() => {});
  } catch (err) {
    if (resultHost) {
      resultHost.innerHTML = `<p class="stopfix-warning">⚠️ ${escapeHtml(t("state.error_prefix") + (err.message || String(err)))}</p>`;
    }
  } finally {
    if (stopFixState) stopFixState.applying = false;
    if (btn) {
      btn.disabled = false;
      btn.textContent = t("stopfix.apply");
    }
  }
}

function retryStopFixChat() {
  const msgId = stopFixState?.msgId || "";
  closeStopFixModal();
  if (msgId && typeof regenerateLastAssistantMessage === "function") {
    void regenerateLastAssistantMessage(msgId);
  }
}

function openStopFixModal(model, prompt = "", msgId = "") {
  const modal = $("stopfix-modal");
  if (!modal) return;
  const name = String(model || "").trim();
  if (!name) return;
  stopFixState = { model: name, prompt: prompt || "", msgId: msgId || "", preview: null, applying: false };
  $("stopfix-modal-title").textContent = `${t("stopfix.title")} · ${name}`;
  $("stopfix-modal-body").innerHTML = renderStopFixShell(name);
  modal.hidden = false;
  void runStopFixPreview();
}

function closeStopFixModal() {
  const modal = $("stopfix-modal");
  if (!modal) return;
  modal.hidden = true;
  const body = $("stopfix-modal-body");
  if (body) body.innerHTML = "";
  stopFixState = null;
}

window.openStopFixModal = openStopFixModal;
window.closeStopFixModal = closeStopFixModal;

document.addEventListener("DOMContentLoaded", () => {
  $("stopfix-modal-x")?.addEventListener("click", closeStopFixModal);
  $("stopfix-modal-close")?.addEventListener("click", closeStopFixModal);
});
