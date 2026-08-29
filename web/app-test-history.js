"use strict";

// ---------- per-test benchmark leaderboard & history modal ----------

function openTestHistoryModal(testId) {
  const test = tests.find((t) => t.id === testId);
  const titleEl = $("test-history-modal-title");
  if (titleEl && test) titleEl.textContent = t("tests.leaderboard_title") + " — " + escapeHtml(test.name);
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
      body.innerHTML = `
        <div class="battery-empty">
          <div style="font-size:32px;margin-bottom:8px;">🏆</div>
          <div>${t("tests.no_models_tested")}</div>
        </div>`;
      return;
    }

    // Deduplicate/rank by model taking the latest/best run for each model
    const modelMap = new Map();
    for (const h of history) {
      if (!h.model) continue;
      if (!modelMap.has(h.model)) {
        modelMap.set(h.model, h);
      }
    }

    const ranked = Array.from(modelMap.values()).map((h) => {
      let passedCount = 0;
      let totalCount = 1;
      let pct = 0;
      let isHuman = false;

      if (h.sub_results && h.sub_results.length > 0) {
        totalCount = h.sub_results.length;
        passedCount = h.sub_results.filter((s) => s.passed === true).length;
        pct = Math.round((passedCount / totalCount) * 100);
      } else if (h.passed === true) {
        passedCount = 1;
        totalCount = 1;
        pct = 100;
      } else if (h.passed === false) {
        passedCount = 0;
        totalCount = 1;
        pct = 0;
      } else {
        isHuman = true;
        pct = -1;
      }

      return {
        ...h,
        passedCount,
        totalCount,
        pct,
        isHuman,
        tps: h.tokens_per_sec || 0,
        ms: h.response_time_ms || 0,
      };
    });

    // Sort ranked: highest score % first, then highest TPS, then lowest latency
    ranked.sort((a, b) => {
      if (b.pct !== a.pct) return b.pct - a.pct;
      if (b.tps !== a.tps) return b.tps - a.tps;
      return a.ms - b.ms;
    });

    const maxTps = Math.max(...ranked.map((r) => r.tps), 1);

    // Leader banner if models exist
    const leader = ranked[0];
    let topPodiumHtml = "";
    if (leader) {
      const leaderShort = escapeHtml(leader.model).replace(/^[^/]+\//, "");
      const leaderScorePill = leader.pct === 100
        ? `<span class="pill pill-good" style="font-size:11px;">${leader.passedCount}/${leader.totalCount} · 100%</span>`
        : (leader.pct > 0 ? `<span class="pill pill-warn" style="font-size:11px;">${leader.pct}%</span>` : "");
      const leaderTps = leader.tps > 0 ? `⚡ ${leader.tps.toFixed(1)} tok/s` : "";

      topPodiumHtml = `
        <div class="test-leader-banner">
          <div class="test-leader-badge">🥇 1º ${t("battery.podium_winner")}</div>
          <div class="test-leader-model" title="${escapeHtml(leader.model)}">${leaderShort}</div>
          <div class="test-leader-stats">
            ${leaderScorePill}
            ${leaderTps ? `<span class="mono bold" style="color:var(--text-accent);">${escapeHtml(leaderTps)}</span>` : ""}
            <span class="mono muted">⏱️ ${fmtDuration(leader.ms)}</span>
          </div>
        </div>
      `;
    }

    let rows = "";
    ranked.forEach((r, idx) => {
      let rankBadge = "";
      if (idx === 0) rankBadge = `<span class="rank-medal rank-medal-gold" title="1º">🥇 1º</span>`;
      else if (idx === 1) rankBadge = `<span class="rank-medal rank-medal-silver" title="2º">🥈 2º</span>`;
      else if (idx === 2) rankBadge = `<span class="rank-medal rank-medal-bronze" title="3º">🥉 3º</span>`;
      else rankBadge = `<span class="rank-num">#${idx + 1}</span>`;

      let scoreBadge = "";
      if (r.error) {
        scoreBadge = `<span class="badge badge-na" title="${escapeHtml(r.error)}">${t("battery.error")}</span>`;
      } else if (r.isHuman) {
        scoreBadge = `<span class="badge badge-human">${t("battery.human_review")}</span>`;
      } else if (r.totalCount > 1) {
        const pillClass = r.passedCount === r.totalCount ? "pill-good" : (r.passedCount > 0 ? "pill-warn" : "pill-bad");
        scoreBadge = `<span class="pill ${pillClass}">✔ ${r.passedCount}/${r.totalCount} <span style="font-size:10px;opacity:0.85;">(${r.pct}%)</span></span>`;
      } else if (r.pct === 100) {
        scoreBadge = `<span class="badge badge-pass">${t("battery.pass")}</span>`;
      } else {
        scoreBadge = `<span class="badge badge-fail">${t("battery.fail")}</span>`;
      }

      const tpsVal = r.tps > 0 ? `${r.tps.toFixed(1)} tok/s` : "—";
      const tpsBarWidth = r.tps > 0 ? Math.min(Math.round((r.tps / maxTps) * 100), 100) : 0;
      const tpsColor = (typeof getToksRecordColor === "function" && r.tps > 0) ? getToksRecordColor(r.tps) : "var(--accent)";

      const reasoning = r.reasoning_used ? "🧠" : "";
      const modelShort = escapeHtml(r.model).replace(/^[^/]+\//, "");
      const date = fmtDate(r.timestamp);

      const resp = r.model_response || "";
      const respPreview = escapeHtml(resp.slice(0, 60).replace(/\s+/g, " "));
      const respKey = `th-${testId}-${escapeHtml(r.model).replace(/[^a-zA-Z0-9]/g, "-")}-${new Date(r.timestamp).getTime()}`;
      testHistoryResponses.set(respKey, resp);

      rows += `
        <tr class="${idx === 0 ? "row-leader" : ""}">
          <td class="cell-rank">${rankBadge}</td>
          <td class="cell-model" title="${escapeHtml(r.model)}">
            <strong>${modelShort}</strong>
            <div class="muted" style="font-size:10px;">${escapeHtml(date)}</div>
          </td>
          <td class="cell-score">${scoreBadge}</td>
          <td class="cell-speed">
            <div class="speed-bar-container">
              <div class="speed-bar-fill" style="width:${tpsBarWidth}%; background-color:${tpsColor};"></div>
            </div>
            <div class="mono" style="font-size:11px; margin-top:2px;">
              <span style="color:${tpsColor}; font-weight:600;">⚡ ${escapeHtml(tpsVal)}</span>
            </div>
          </td>
          <td class="cell-time mono" style="font-size:12px;">
            ⏱️ ${fmtDuration(r.ms)} ${reasoning}
          </td>
          <td class="cell-response-compact">
            ${resp.trim().length > 0
              ? `<button type="button" class="resp-view-btn ghost" data-resp-key="${respKey}" title="${escapeHtml(respPreview)}">${t("action.view")} ↗</button>`
              : `<span class="muted">—</span>`
            }
          </td>
        </tr>
      `;
    });

    body.innerHTML = `
      <div class="test-leaderboard-wrap">
        ${topPodiumHtml}
        <div class="test-leaderboard-count muted" style="margin-bottom:8px; font-size:12px;">
          ${t("tests.tested_models_count", { count: ranked.length })}
        </div>
        <div class="battery-table-wrap battery-table-wrap--compact">
          <table class="battery-table battery-table--compact leaderboard-table">
            <thead>
              <tr>
                <th style="width:70px;">${t("tests.rank_col")}</th>
                <th>${t("chat.model")}</th>
                <th>${t("tests.score_col")}</th>
                <th style="min-width:140px;">${t("tests.speed_col")}</th>
                <th>${t("battery.response_time")}</th>
                <th style="width:70px;">${t("chat.response")}</th>
              </tr>
            </thead>
            <tbody>${rows}</tbody>
          </table>
        </div>
      </div>
    `;

    body.querySelectorAll(".resp-view-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        const row = btn.closest("tr");
        const model = row?.querySelector(".cell-model")?.getAttribute("title") || "";
        const fullResp = testHistoryResponses.get(btn.dataset.respKey) || "";
        const testObj = tests.find((t) => t.id === testId);
        const titleEl = $("response-view-modal-title");
        if (titleEl && testObj) titleEl.textContent = `${testObj.name} — ${model}`;
        openResponseViewModal(model, fullResp);
      });
    });
  } catch (err) {
    body.innerHTML = `<div class="muted">${escapeHtml(err.message)}</div>`;
  }
}
