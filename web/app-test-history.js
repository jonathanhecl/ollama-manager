"use strict";

// ---------- per-test history modal ----------

function openTestHistoryModal(testId) {
  const test = tests.find((t) => t.id === testId);
  const titleEl = $("test-history-modal-title");
  if (titleEl && test) titleEl.textContent = t("tests.history_title") + " — " + escapeHtml(test.name);
  $("test-history-modal").hidden = false;
  void renderTestHistoryModal(testId);
}

function closeTestHistoryModal() {
  $("test-history-modal").hidden = true;
}

async function renderTestHistoryModal(testId) {
  const body = $("test-history-modal-body");
  if (!body) return;
  body.innerHTML = `<div class="muted">${t("status.loading")}</div>`;
  try {
    const data = await api("/api/runner/test-history/" + encodeURIComponent(testId));
    const history = data.history || [];
    if (history.length === 0) {
      body.innerHTML = `<div class="battery-empty">${t("tests.no_history")}</div>`;
      return;
    }
    let rows = "";
    for (const h of history) {
      const date = fmtDateTimeFull(h.timestamp);
      let badge = "";
      const hasRealResponse = (h.tokens_per_sec || 0) > 0 && (h.model_response || "").trim().length > 0;
      if (h.error) {
        badge = `<span class="badge badge-na" title="${escapeHtml(h.error)}">${t("battery.error")}</span>`;
      } else if (!hasRealResponse && h.passed === false) {
        badge = `<span class="badge badge-na" title="${escapeHtml(h.model_response || t("battery.no_response"))}">${t("battery.error")}</span>`;
      } else if (h.passed === true) {
        badge = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
      } else if (h.passed === false) {
        badge = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
      } else {
        badge = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
      }
      const reasoning = h.reasoning_used ? "🧠" : "";
      const tps = h.tokens_per_sec ? `${h.tokens_per_sec.toFixed(1)} tok/s` : "";
      const resp = h.model_response || "";
      const respPreview = escapeHtml(resp.slice(0, 60).replace(/\s+/g, " "));
      const respKey = `th-${testId}-${escapeHtml(h.model).replace(/[^a-zA-Z0-9]/g, "-")}-${new Date(h.timestamp).getTime()}`;
      const sys = h.sys_info || {};
      const sysLabel = sys.os ? escapeHtml(sys.os + (sys.ram_gb ? ` · ${sys.ram_gb}GB` : "")) : "—";
      const modelShort = escapeHtml(h.model).replace(/^[^/]+\//, "");
      testHistoryResponses.set(respKey, resp);
      rows += `
        <tr>
          <td class="cell-time" title="${escapeHtml(date)}">${escapeHtml(date.split(" ")[0])}<br><span class="muted" style="font-size:10px">${escapeHtml(date.split(" ")[1] || "")}</span></td>
          <td class="cell-model" title="${escapeHtml(h.model)}">${escapeHtml(modelShort)}</td>
          <td>${badge}</td>
          <td class="cell-time">${fmtDuration(h.response_time_ms)} ${reasoning}<br><span class="muted" style="font-size:10px">${escapeHtml(tps)}</span></td>
          <td class="cell-response-compact">
            <span class="resp-preview">${respPreview}${resp.length > 60 ? "…" : ""}</span>
            ${resp.trim().length > 0 ? `<button type="button" class="resp-view-btn" data-resp-key="${respKey}">${t("action.view")}</button>` : ""}
          </td>
          <td class="cell-sys-compact" title="${escapeHtml(h.model)}">${sysLabel}</td>
        </tr>
      `;
    }
    body.innerHTML = `
      <div class="battery-table-wrap battery-table-wrap--compact">
        <table class="battery-table battery-table--compact">
          <thead>
            <tr>
              <th>${t("battery.date")}</th>
              <th>${t("chat.model")}</th>
              <th>${t("battery.results")}</th>
              <th>${t("battery.response_time")}</th>
              <th>${t("chat.response")}</th>
              <th>${t("battery.sys_info")}</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;
    body.querySelectorAll(".resp-view-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        const model = btn.closest("tr")?.querySelector(".cell-model")?.getAttribute("title") || "";
        const fullResp = testHistoryResponses.get(btn.dataset.respKey) || "";
        openResponseViewModal(model, fullResp);
      });
    });
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}

