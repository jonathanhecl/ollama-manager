"use strict";

// ---------- model artifact list / load ----------

let artifactCountReqSeq = 0;

async function refreshModelArtifactCount() {
  const btn = $("chat-model-artifacts-btn");
  const model = $("chat-model")?.value;
  if (!btn) return;
  // Only models that can create artifacts (tools capability) show the link.
  if (!model || !modelCaps(model).has("tools")) {
    btn.hidden = true;
    btn.textContent = "";
    return;
  }
  const seq = ++artifactCountReqSeq;
  try {
    const data = await api("/api/models/" + encodeURIComponent(model) + "/artifacts");
    if (seq !== artifactCountReqSeq || $("chat-model")?.value !== model) {
      return;
    }
    const n = (data && Array.isArray(data.artifacts)) ? data.artifacts.length : 0;
    if (n === 0) {
      btn.hidden = true;
      btn.textContent = "";
      return;
    }
    btn.textContent = n === 1
      ? t("chat.model_artifacts_link_one")
      : t("chat.model_artifacts_link_other", { count: n });
    btn.hidden = false;
  } catch (e) {
    if (seq === artifactCountReqSeq) {
      btn.hidden = true;
    }
  }
}

function updateArtifactResourceBtn() {
  const btn = $("chat-artifact-btn");
  if (!btn) return;
  if (activeArtifactTimestamp) {
    btn.hidden = false;
    const name = activeArtifactName || "Artifact";
    const titleText = t("chat.artifact.active_resource", { name });
    btn.setAttribute("title", titleText);
    btn.setAttribute("aria-label", titleText);
  } else {
    btn.hidden = true;
  }
}

function unloadSessionArtifact() {
  const confirmed = window.confirm(t("chat.artifact.unload_confirm"));
  if (!confirmed) return;
  activeArtifactTimestamp = null;
  activeArtifactName = null;
  activeArtifactUrl = null;
  const cb = $("chat-artifacts");
  if (cb && cb.checked) {
    cb.checked = false;
    saveChatOptionsForCurrentModel();
  }
  hideArtifactPanel();
  updateArtifactResourceBtn();
  toast(t("chat.artifact.unloaded"), "success");
}

let loadArtifactModalReqSeq = 0;

function openLoadArtifactModal() {
  const model = $("chat-model")?.value;
  if (!model) return;
  $("load-artifact-model").textContent = model;
  const list = $("load-artifact-list");
  const empty = $("load-artifact-empty");
  list.innerHTML = "";
  empty.hidden = false;
  empty.textContent = t("state.loading") || "Loading…";
  $("load-artifact-modal").hidden = false;

  const seq = ++loadArtifactModalReqSeq;
  api("/api/models/" + encodeURIComponent(model) + "/artifacts").then((data) => {
    if (seq !== loadArtifactModalReqSeq || $("chat-model")?.value !== model) return;
    const arts = (data && Array.isArray(data.artifacts)) ? data.artifacts : [];
    list.innerHTML = "";
    if (arts.length === 0) {
      empty.hidden = false;
      empty.textContent = t("chat.load_artifact_none");
      const btn = $("chat-model-artifacts-btn");
      if (btn) { btn.hidden = true; btn.textContent = ""; }
      return;
    }
    empty.hidden = true;
    for (const a of arts) {
      const meta = [];
      if (a.name && a.name !== a.date) {
        meta.push(escapeHtml(a.date));
      }
      if (a.file_count != null) {
        meta.push(a.file_count === 1 ? t("chat.load_artifact_file_one") : t("chat.load_artifact_files", { count: a.file_count }));
      }
      if (a.size) meta.push(fmtBytes(a.size));
      const displayName = a.name || a.date;
      const row = document.createElement("div");
      row.className = "running-item";
      row.innerHTML = `
        <div class="running-main">
          <div class="running-name">${escapeHtml(displayName)}</div>
          <div class="running-meta">${meta.join(" · ")}</div>
        </div>
        <div class="running-actions">
          <button type="button" class="primary" data-load="${escapeHtml(a.id)}">${escapeHtml(t("chat.load_artifact"))}</button>
          <button type="button" class="ghost danger-text" data-delete="${escapeHtml(a.id)}" data-i18n-attr="title" data-i18n="chat.delete_artifact" title="${escapeHtml(t("chat.delete_artifact"))}">🗑️</button>
        </div>`;
      row.querySelector("[data-load]").addEventListener("click", () => loadExistingArtifact(a.id, a.date, a.name));
      row.querySelector("[data-delete]").addEventListener("click", () => deleteExistingArtifact(a.id, displayName));
      list.appendChild(row);
    }
    const btn = $("chat-model-artifacts-btn");
    if (btn) {
      btn.textContent = arts.length === 1
        ? t("chat.model_artifacts_link_one")
        : t("chat.model_artifacts_link_other", { count: arts.length });
      btn.hidden = false;
    }
  }).catch((e) => {
    if (seq !== loadArtifactModalReqSeq) return;
    empty.hidden = false;
    empty.textContent = t("state.error_prefix") + e.message;
  });
}

async function deleteExistingArtifact(id, name) {
  const confirmed = window.confirm(t("chat.delete_artifact_confirm", { name: name || id }));
  if (!confirmed) return;
  try {
    await api("/api/artifacts/" + id, { method: "DELETE" });
    toast(t("chat.delete_artifact_success", { name: name || id }), "success");
    if (activeArtifactTimestamp === id) {
      activeArtifactTimestamp = null;
      activeArtifactName = null;
      activeArtifactUrl = null;
      hideArtifactPanel();
      updateArtifactResourceBtn();
    }
    openLoadArtifactModal();
    void refreshModelArtifactCount();
  } catch (err) {
    toast(t("toast.delete_error", { msg: err.message }), "error");
  }
}

function loadExistingArtifact(id, label, name) {
  const cb = $("chat-artifacts");
  if (cb) cb.checked = true;
  saveChatOptionsForCurrentModel();
  activeArtifactTimestamp = id;
  activeArtifactName = name || label || "Artifact";
  activeArtifactUrl = "/api/artifacts/" + id + "/";
  updateArtifactResourceBtn();
  $("load-artifact-modal").hidden = true;
  showArtifactPanel(activeArtifactUrl, activeArtifactName, false);
  toast(t("chat.load_artifact_loaded"), "success");
  void refreshModelArtifactCount();
}

function applyArtifactWidth(cv, desiredWidth) {
  if (!cv) cv = $("chat-view");
  if (!cv) return 300;
  const rect = cv.getBoundingClientRect();
  const totalW = rect.width > 0 ? rect.width : (window.innerWidth - 36);
  // Chat shell can comfortably shrink down to 260px (or 35% on wider screens)
  const minChatW = Math.max(260, Math.min(380, totalW * 0.35));
  const maxArtifactW = Math.max(220, totalW - minChatW - 10);
  const minArtifactW = Math.max(220, totalW * 0.20);

  let numW;
  if (typeof desiredWidth === "number" && !isNaN(desiredWidth)) {
    numW = desiredWidth;
  } else if (typeof desiredWidth === "string" && desiredWidth.endsWith("%")) {
    const pct = parseFloat(desiredWidth) / 100;
    numW = totalW * pct;
  } else if (typeof desiredWidth === "string" && desiredWidth.endsWith("px")) {
    numW = parseFloat(desiredWidth);
  } else {
    numW = totalW * 0.5;
  }

  const clamped = Math.max(minArtifactW, Math.min(maxArtifactW, numW));
  cv.style.setProperty("--chat-right-width", `${Math.round(clamped)}px`);
  return clamped;
}

function showArtifactPanel(url, name, generating) {
  const panel = $("chat-artifact-panel");
  const frame = $("chat-artifact-frame");
  const title = $("chat-artifact-title");
  const splitter = $("chat-splitter");
  const chatView = $("chat-view");
  if (!panel || !frame) return;
  console.log("[artifact] showArtifactPanel", { url, name, generating, panelHidden: panel.hidden, frameSrc: frame.src, frameSrcdoc: frame.srcdoc ? "(set)" : "(none)" });

  if (url) {
    activeArtifactUrl = url;
    const match = String(url).match(/\/api\/artifacts\/(.+)\//);
    if (match) {
      activeArtifactTimestamp = match[1];
    }
  }
  if (name) {
    activeArtifactName = name;
  }
  updateArtifactResourceBtn();

  if (title && name) title.textContent = name;
  if (generating) {
    const loadingText = escapeHtml(t("chat.artifact.generating") || "Generating artifact…");
    frame.removeAttribute("src");
    frame.srcdoc = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body {
  width: 100%;
  height: 100%;
  background: #090a0f;
  background-image: 
    radial-gradient(circle at 50% 45%, rgba(139, 92, 246, 0.2) 0%, rgba(59, 130, 246, 0.12) 35%, transparent 70%),
    radial-gradient(circle at 80% 20%, rgba(236, 72, 153, 0.08) 0%, transparent 50%),
    linear-gradient(rgba(255, 255, 255, 0.02) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.02) 1px, transparent 1px);
  background-size: 100% 100%, 100% 100%, 32px 32px, 32px 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Inter, sans-serif;
  overflow: hidden;
  color: #fff;
}
.container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  position: relative;
  user-select: none;
}
.ambient-glow {
  position: absolute;
  width: 280px;
  height: 280px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(168, 85, 247, 0.35) 0%, rgba(59, 130, 246, 0.2) 50%, transparent 75%);
  filter: blur(40px);
  animation: pulse-glow 4s ease-in-out infinite alternate;
  pointer-events: none;
}
.orb-stage {
  position: relative;
  width: 130px;
  height: 130px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 2rem;
  perspective: 800px;
}
.core-sparkle {
  position: absolute;
  width: 48px;
  height: 48px;
  border-radius: 14px;
  background: linear-gradient(135deg, #a855f7, #3b82f6);
  box-shadow: 0 0 30px rgba(168, 85, 247, 0.8), 0 0 60px rgba(59, 130, 246, 0.4);
  display: flex;
  align-items: center;
  justify-content: center;
  animation: float-core 3s ease-in-out infinite alternate, rotate-core 12s linear infinite;
  z-index: 5;
}
.core-sparkle svg {
  width: 26px;
  height: 26px;
  fill: #ffffff;
  filter: drop-shadow(0 2px 6px rgba(0,0,0,0.4));
  animation: pulse-icon 2s ease-in-out infinite;
}
.ring {
  position: absolute;
  border-radius: 50%;
  border: 2px solid transparent;
  pointer-events: none;
}
.ring-1 {
  width: 90px;
  height: 90px;
  border-top-color: #ec4899;
  border-bottom-color: #8b5cf6;
  animation: spin-ring-1 3s linear infinite;
  box-shadow: 0 0 15px rgba(236, 72, 153, 0.4);
}
.ring-2 {
  width: 114px;
  height: 114px;
  border-left-color: #3b82f6;
  border-right-color: #06b6d4;
  animation: spin-ring-2 4.5s linear infinite reverse;
  box-shadow: 0 0 15px rgba(6, 182, 212, 0.35);
}
.ring-3 {
  width: 136px;
  height: 136px;
  border-top-color: rgba(168, 85, 247, 0.7);
  border-right-color: rgba(236, 72, 153, 0.5);
  border-style: dashed;
  animation: spin-ring-3 8s linear infinite;
}
.satellite {
  position: absolute;
  width: 100%;
  height: 100%;
  animation: spin-satellite 5s linear infinite;
}
.satellite::after {
  content: '';
  position: absolute;
  top: -4px;
  left: 50%;
  transform: translateX(-50%);
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow: 0 0 12px #fff, 0 0 20px #a855f7;
}
.text-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  z-index: 10;
}
.title {
  font-size: 1.15rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  background: linear-gradient(90deg, #f1f5f9 0%, #cbd5e1 30%, #a855f7 50%, #60a5fa 70%, #f1f5f9 100%);
  background-size: 200% auto;
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  animation: shimmer-text 3s linear infinite;
}
.sub-pill {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  font-size: 0.75rem;
  font-weight: 500;
  letter-spacing: 0.03em;
  color: #94a3b8;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  padding: 0.25rem 0.75rem;
  border-radius: 9999px;
  backdrop-filter: blur(8px);
}
.pulsing-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #10b981;
  box-shadow: 0 0 8px #10b981;
  animation: blink 1.5s ease-in-out infinite;
}
@keyframes pulse-glow {
  0% { transform: scale(0.9); opacity: 0.6; }
  100% { transform: scale(1.15); opacity: 0.95; }
}
@keyframes float-core {
  0% { transform: translateY(-4px) scale(0.98); }
  100% { transform: translateY(4px) scale(1.02); }
}
@keyframes rotate-core {
  0% { border-radius: 14px; }
  50% { border-radius: 20px; }
  100% { border-radius: 14px; }
}
@keyframes pulse-icon {
  0%, 100% { transform: scale(1); opacity: 0.9; }
  50% { transform: scale(1.12); opacity: 1; }
}
@keyframes spin-ring-1 {
  0% { transform: rotateX(65deg) rotateY(15deg) rotateZ(0deg); }
  100% { transform: rotateX(65deg) rotateY(15deg) rotateZ(360deg); }
}
@keyframes spin-ring-2 {
  0% { transform: rotateX(-55deg) rotateY(25deg) rotateZ(0deg); }
  100% { transform: rotateX(-55deg) rotateY(25deg) rotateZ(360deg); }
}
@keyframes spin-ring-3 {
  0% { transform: rotateX(75deg) rotateY(-10deg) rotateZ(0deg); }
  100% { transform: rotateX(75deg) rotateY(-10deg) rotateZ(360deg); }
}
@keyframes spin-satellite {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}
@keyframes shimmer-text {
  0% { background-position: 0% center; }
  100% { background-position: 200% center; }
}
@keyframes blink {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.4; transform: scale(0.85); }
}
</style>
</head>
<body>
  <div class="container">
    <div class="ambient-glow"></div>
    <div class="orb-stage">
      <div class="ring ring-3"></div>
      <div class="ring ring-2"></div>
      <div class="ring ring-1"></div>
      <div class="satellite"></div>
      <div class="core-sparkle">
        <svg viewBox="0 0 24 24">
          <path d="M12 2L14.4 8.6L21 11L14.4 13.4L12 20L9.6 13.4L3 11L9.6 8.6L12 2Z"/>
        </svg>
      </div>
    </div>
    <div class="text-box">
      <div class="title">${loadingText}</div>
      <div class="sub-pill">
        <span class="pulsing-dot"></span>
        <span>Synthesizing code & preview</span>
      </div>
    </div>
  </div>
</body>
</html>`;
  } else if (url) {
    frame.removeAttribute("srcdoc");
    frame.src = url;
  }
  panel.hidden = false;
  // Close options panel so the artifact gets the full right/top area.
  chatView?.classList.remove("chat-options-open");
  // Show splitter on desktop and set default width on open (clamped so chat has >= 35%).
  if (chatView && splitter) {
    const savedWidth = localStorage.getItem("ollama_manager_artifact_width");
    applyArtifactWidth(chatView, savedWidth || "50%");
    chatArtifactWidthSet = true;
    splitter.hidden = false;
  }
  syncChatPanels(chatView);
}

function hideArtifactPanel() {
  const panel = $("chat-artifact-panel");
  const frame = $("chat-artifact-frame");
  const splitter = $("chat-splitter");
  const chatView = $("chat-view");
  if (!panel) return;
  panel.hidden = true;
  if (frame) frame.src = "about:blank";
  if (splitter) splitter.hidden = true;
  // Restore default options sidebar width.
  if (chatView) {
    chatView.style.setProperty("--chat-right-width", "300px");
  }
  chatArtifactVisibleBeforeOptions = false;
  syncChatPanels(chatView);
}

function swapToOptions(cv) {
  const artifactPanel = $("chat-artifact-panel");
  const splitter = $("chat-splitter");
  const backBtn = $("chat-artifact-back");
  const artifactVisible = artifactPanel && !artifactPanel.hidden;
  chatArtifactVisibleBeforeOptions = artifactVisible;
  cv.classList.add("chat-options-open");
  if (artifactPanel) artifactPanel.hidden = true;
  if (splitter) splitter.hidden = true;
  cv.style.setProperty("--chat-right-width", "300px");
  if (backBtn) backBtn.hidden = !artifactVisible;
  syncChatPanels(cv);
}

function swapToArtifact(cv) {
  const artifactPanel = $("chat-artifact-panel");
  const splitter = $("chat-splitter");
  const backBtn = $("chat-artifact-back");
  cv.classList.remove("chat-options-open");
  if (chatArtifactVisibleBeforeOptions && artifactPanel) {
    artifactPanel.hidden = false;
    if (splitter) splitter.hidden = false;
    const savedWidth = localStorage.getItem("ollama_manager_artifact_width");
    applyArtifactWidth(cv, savedWidth || "50%");
  } else {
    cv.style.setProperty("--chat-right-width", "300px");
  }
  if (backBtn) backBtn.hidden = true;
  syncChatPanels(cv);
}

// Keeps the .artifact-open class (used by CSS instead of :has()) in sync with
// the real visibility of the artifact panel, and mirrors options state into
// the toggle's aria-expanded attribute.
function syncChatPanels(cv) {
  if (!cv) cv = $("chat-view");
  if (!cv) return;
  const panel = $("chat-artifact-panel");
  const artifactOpen = !!(panel && !panel.hidden);
  cv.classList.toggle("artifact-open", artifactOpen);
  if (artifactOpen) {
    const currentW = cv.style.getPropertyValue("--chat-right-width") || localStorage.getItem("ollama_manager_artifact_width") || "50%";
    applyArtifactWidth(cv, currentW);
  }
  const toggle = $("chat-options-toggle");
  if (toggle) toggle.setAttribute("aria-expanded", cv.classList.contains("chat-options-open") ? "true" : "false");
}

async function handleArtifactEvalRequest(data) {
  const reqID = data?.request_id;
  if (!reqID) return;

  const panel = $("chat-artifact-panel");
  const frame = $("chat-artifact-frame");
  if (!panel || panel.hidden || !frame) {
    void api("/api/artifacts/eval", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, error: "Artifact preview is currently closed or hidden in the browser." }),
    });
    return;
  }

  const win = frame.contentWindow;
  if (!win) {
    void api("/api/artifacts/eval", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, error: "Artifact preview frame is not accessible." }),
    });
    return;
  }

  const code = String(data?.code || "").trim();
  try {
    let fn;
    let isDirectExpr = false;
    if (!code.includes("return ") && !code.includes("\n") && !code.includes(";")) {
      try {
        fn = win.Function(`return (${code});`);
        isDirectExpr = true;
      } catch {}
    }
    if (!isDirectExpr) {
      try {
        fn = win.Function(`return (async () => {\n${code}\n})();`);
      } catch {
        fn = () => win.eval(code);
      }
    }

    const rawResult = await fn.call(win);

    let resultStr = "";
    if (rawResult === undefined) {
      resultStr = "undefined (executed successfully)";
    } else if (rawResult === null) {
      resultStr = "null";
    } else if (typeof rawResult === "string") {
      resultStr = rawResult;
    } else if (typeof rawResult === "number" || typeof rawResult === "boolean" || typeof rawResult === "bigint") {
      resultStr = String(rawResult);
    } else if (rawResult instanceof win.Element || (rawResult && rawResult.nodeType === 1)) {
      resultStr = `<${rawResult.tagName.toLowerCase()}${rawResult.id ? ' id="' + rawResult.id + '"' : ''}${rawResult.className ? ' class="' + rawResult.className + '"' : ''}>` + (rawResult.innerText ? ` ${rawResult.innerText.trim().slice(0, 200)}` : "");
    } else if (rawResult instanceof win.NodeList || rawResult instanceof win.HTMLCollection) {
      resultStr = `[${Array.from(rawResult).map(el => el.tagName ? `<${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}>` : String(el)).join(", ")}]`;
    } else {
      try {
        resultStr = JSON.stringify(rawResult, (key, value) => {
          if (value instanceof win.Element || (value && value.nodeType === 1)) {
            return `<${value.tagName.toLowerCase()}${value.id ? '#' + value.id : ''}>`;
          }
          return value;
        }, 2);
      } catch {
        resultStr = String(rawResult);
      }
    }

    void api("/api/artifacts/eval", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, result: resultStr }),
    });
  } catch (err) {
    void api("/api/artifacts/eval", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, error: err?.message || String(err) || "Evaluation failed" }),
    });
  }
}

async function handleArtifactScreenshotRequest(data) {
  const reqID = data?.request_id;
  if (!reqID) return;

  const panel = $("chat-artifact-panel");
  const frame = $("chat-artifact-frame");
  if (!panel || panel.hidden || !frame) {
    void api("/api/artifacts/screenshot", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, error: "Artifact preview is currently closed or hidden in the browser." }),
    });
    return;
  }

  try {
    const base64 = await captureIframeContent(frame);
    if (!base64) {
      throw new Error("Could not capture preview image");
    }
    const fullDataUrl = "data:image/jpeg;base64," + base64;
    const lastMsg = chatMessages[chatMessages.length - 1];
    if (lastMsg && lastMsg.role === "assistant" && Array.isArray(lastMsg.toolLog)) {
      for (let i = lastMsg.toolLog.length - 1; i >= 0; i--) {
        const entry = lastMsg.toolLog[i];
        if (entry.name === "take_artifact_screenshot") {
          entry.image = fullDataUrl;
          break;
        }
      }
      scheduleRenderChatMessages();
      requestAnimationFrame(() => scrollChatToBottom());
    }
    void api("/api/artifacts/screenshot", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, image: base64 }),
    });
  } catch (err) {
    void api("/api/artifacts/screenshot", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ request_id: reqID, error: err.message || "Failed to capture preview image" }),
    });
  }
}

async function captureIframeContent(frame) {
  const doc = frame.contentDocument || frame.contentWindow?.document;
  if (!doc || !doc.body) throw new Error("Iframe document not loaded");

  const w = Math.max(frame.clientWidth || 800, 400);
  const h = Math.max(frame.clientHeight || 600, 300);

  const origCanvases = doc.querySelectorAll("canvas");

  // If document contains a standalone single canvas with no text, capture directly
  if (origCanvases.length === 1 && !doc.body.innerText.trim()) {
    try {
      const d = origCanvases[0].toDataURL("image/jpeg", 0.85);
      return d.replace(/^data:image\/[a-z]+;base64,/, "");
    } catch {}
  }

  const clone = doc.documentElement.cloneNode(true);

  // Canvas elements in cloneNode don't retain pixel data; convert them to <img> in the clone
  const cloneCanvases = clone.querySelectorAll("canvas");
  for (let i = 0; i < origCanvases.length && i < cloneCanvases.length; i++) {
    try {
      const orig = origCanvases[i];
      const cl = cloneCanvases[i];
      const dataUrl = orig.toDataURL("image/png");
      const img = doc.createElement("img");
      img.src = dataUrl;
      img.style.cssText = orig.style.cssText;
      if (orig.width) img.width = orig.width;
      if (orig.height) img.height = orig.height;
      if (orig.className) img.className = orig.className;
      cl.parentNode?.replaceChild(img, cl);
    } catch {}
  }

  // Remove script tags from cloned DOM to prevent XMLSerializer or foreignObject issues
  clone.querySelectorAll("script").forEach((s) => s.remove());

  const baseHref = frame.src || window.location.href;
  const base = doc.createElement("base");
  base.href = baseHref;
  const head = clone.querySelector("head");
  if (head) {
    head.insertBefore(base, head.firstChild);
  }

  const docHtml = new XMLSerializer().serializeToString(clone);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}">`
    + `<foreignObject width="100%" height="100%">`
    + `<div xmlns="http://www.w3.org/1999/xhtml" style="width:${w}px;height:${h}px;background:#ffffff;overflow:hidden;">`
    + docHtml
    + `</div></foreignObject></svg>`;

  const blob = new Blob([svg], { type: "image/svg+xml;charset=utf-8" });
  const url = URL.createObjectURL(blob);

  return new Promise((resolve, reject) => {
    const img = new Image();
    const timer = setTimeout(() => {
      URL.revokeObjectURL(url);
      if (origCanvases.length > 0) {
        try {
          const d = origCanvases[0].toDataURL("image/jpeg", 0.85);
          resolve(d.replace(/^data:image\/[a-z]+;base64,/, ""));
          return;
        } catch {}
      }
      reject(new Error("Capture render timed out"));
    }, 4000);

    img.onload = () => {
      clearTimeout(timer);
      try {
        const canvas = document.createElement("canvas");
        canvas.width = Math.min(w, 1280);
        canvas.height = Math.min(h, 1280);
        const ctx = canvas.getContext("2d");
        ctx.fillStyle = "#ffffff";
        ctx.fillRect(0, 0, canvas.width, canvas.height);
        ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
        URL.revokeObjectURL(url);
        const dataUrl = canvas.toDataURL("image/jpeg", 0.85);
        resolve(dataUrl.replace(/^data:image\/[a-z]+;base64,/, ""));
      } catch (e) {
        URL.revokeObjectURL(url);
        if (origCanvases.length > 0) {
          try {
            const d = origCanvases[0].toDataURL("image/jpeg", 0.85);
            resolve(d.replace(/^data:image\/[a-z]+;base64,/, ""));
            return;
          } catch {}
        }
        reject(e);
      }
    };

    img.onerror = () => {
      clearTimeout(timer);
      URL.revokeObjectURL(url);
      if (origCanvases.length > 0) {
        try {
          const d = origCanvases[0].toDataURL("image/jpeg", 0.85);
          resolve(d.replace(/^data:image\/[a-z]+;base64,/, ""));
          return;
        } catch {}
      }
      reject(new Error("Failed to rasterize preview DOM to image"));
    };

    img.src = url;
  });
}

function bindChatEvents() {
  const chatView = $("chat-view");
  if (!chatView) return;
  window.addEventListener("pagehide", stopSpeechPlayback);
  window.addEventListener("beforeunload", stopSpeechPlayback);
  window.addEventListener("popstate", async () => {
    stopSpeechPlayback();
    await handleRouting();
  });
  window.addEventListener("resize", () => {
    // 1135px matches the CSS breakpoint where the options become an overlay.
    if (window.innerWidth > 1135) {
      const cv = $("chat-view");
      cv?.classList.remove("chat-options-open");
      syncChatPanels(cv);
    }
  });
  $("chat-btn")?.addEventListener("click", showChatView);
  addFastTapListener($("chat-back-btn"), () => {
    showModelsView();
    resetChatState();
    refreshModels().catch(() => {});
  });
  addFastTapListener($("chat-options-toggle"), () => {
    const cv = $("chat-view");
    if (!cv) return;
    if (cv.classList.contains("chat-options-open")) {
      swapToArtifact(cv);
    } else {
      swapToOptions(cv);
      void refreshModelArtifactCount();
    }
  });
  addFastTapListener($("chat-options-close"), () => {
    const cv = $("chat-view");
    if (!cv) return;
    swapToArtifact(cv);
  });
  addFastTapListener($("chat-artifact-back"), () => {
    const cv = $("chat-view");
    if (!cv) return;
    swapToArtifact(cv);
  });
  chatView.addEventListener("click", (e) => {
    if (!$("chat-view")?.classList.contains("chat-options-open")) return;
    if (e.target.closest(".chat-side")) return;
    if (e.target.closest("#chat-options-toggle")) return;
    if (e.target.closest("#chat-artifact-options")) return;
    if (e.target.closest("#chat-artifact-back")) return;
    swapToArtifact($("chat-view"));
  });
  $("chat-reset-btn")?.addEventListener("click", async () => {
    if (chatMessages.length === 0 && !activeArtifactTimestamp && chatAttachments.length === 0 && !$("chat-input")?.value.trim()) {
      resetChatState();
      return;
    }
    const res = await askConfirm({
      title: t("chat.reset_confirm_title"),
      text: t("chat.reset_confirm_text"),
      okText: t("chat.reset_confirm_ok"),
      okClass: "primary",
    });
    if (res && res.ok) {
      resetChatState();
      toast(t("chat.reset_success"), "success");
    }
  });
  $("chat-model")?.addEventListener("change", () => {
    updateChatCapabilityUI();
    updateChatContextMeter();
    void applyChatDefaultsForModel($("chat-model").value, true);

    const name = $("chat-model").value;
    const model = modelByName(name);
    if (model && model.digest) {
      const urlDigest = model.digest.replace(":", "-");
      const newPath = "/chat/" + urlDigest;
      if (window.location.pathname !== newPath) {
        history.replaceState(null, "", newPath);
      }
    }
  });
  $("chat-model-copy-btn")?.addEventListener("click", async () => {
    const val = $("chat-model-name-value")?.textContent || "";
    if (!val) return;
    const ok = await copyTextToClipboard(val);
    toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
  });
  addFastTapListener($("chat-send-btn"), () => sendChatMessage(false));
  ($("chat-scroll-shell") || $("chat-messages"))?.addEventListener("click", async (e) => {
    const artBtn = e.target.closest(".chat-artifact-open-btn");
    if (artBtn) {
      e.preventDefault();
      const url = artBtn.getAttribute("data-artifact-url") || "";
      const name = artBtn.getAttribute("data-artifact-name") || "Artifact";
      if (url) showArtifactPanel(url, name);
      return;
    }
    const quoteB = e.target.closest(".chat-quote-btn");
    if (quoteB) {
      e.preventDefault();
      const id = quoteB.getAttribute("data-msg-id");
      if (!id) return;
      const msg = chatMessages.find((x) => x.id === id);
      if (!msg) return;
      let quoteText = "";
      const sel = window.getSelection();
      const selStr = sel ? sel.toString().trim() : "";
      if (selStr) {
        const article = quoteB.closest(".chat-msg");
        if (article && sel.anchorNode && article.contains(sel.anchorNode)) {
          quoteText = selStr;
        }
      }
      if (!quoteText) {
        quoteText = String(msg.content || "").trim();
      }
      if (quoteText) {
        const quotedFormatted = quoteText.split("\n").map((l) => `> ${l}`).join("\n") + "\n\n";
        const ta = $("chat-input");
        if (ta) {
          const cur = ta.value;
          if (!cur.trim()) {
            ta.value = quotedFormatted;
          } else {
            ta.value = cur.trimEnd() + "\n\n" + quotedFormatted;
          }
          ta.focus();
          ta.setSelectionRange(ta.value.length, ta.value.length);
        }
      }
      return;
    }
    const regenB = e.target.closest(".chat-regenerate-btn");
    if (regenB) {
      e.preventDefault();
      const id = regenB.getAttribute("data-msg-id");
      if (id) await regenerateLastAssistantMessage(id);
      return;
    }
    const oomB = e.target.closest(".chat-oom-retry-btn");
    if (oomB) {
      e.preventDefault();
      const id = oomB.getAttribute("data-msg-id");
      const pct = normalizeNumCtxPct(Number(oomB.getAttribute("data-suggested-pct")));
      if (id && pct > 0) {
        await reduceContextAndRetry(id, pct);
      }
      return;
    }
    const ttsB = e.target.closest(".chat-tts-btn");
    if (ttsB) {
      e.preventDefault();
      const id = ttsB.getAttribute("data-msg-id");
      if (!id) return;
      const msg = chatMessages.find((x) => x.id === id);
      if (!msg) return;
      speakMessage(msg);
      return;
    }
    const editB = e.target.closest(".chat-edit-btn");
    if (editB) {
      e.preventDefault();
      const id = editB.getAttribute("data-msg-id");
      if (!id) return;
      const msg = chatMessages.find((x) => x.id === id);
      if (!msg) return;
      chatEditingMessageId = msg.id;
      chatEditingDraft = String(msg.content || "");
      renderChatMessages();
      const ta = document.querySelector(`.chat-edit-textarea[data-msg-id="${CSS.escape(id)}"]`);
      if (ta) {
        ta.focus();
        ta.setSelectionRange(ta.value.length, ta.value.length);
      }
      return;
    }
    const saveB = e.target.closest(".chat-edit-save");
    if (saveB) {
      e.preventDefault();
      const id = saveB.getAttribute("data-msg-id");
      if (id) await editAndResendUserMessage(id, chatEditingDraft);
      return;
    }
    const cancelB = e.target.closest(".chat-edit-cancel");
    if (cancelB) {
      e.preventDefault();
      chatEditingMessageId = "";
      chatEditingDraft = "";
      renderChatMessages();
      return;
    }
    const btn = e.target.closest(".chat-copy-btn");
    if (!btn) {
      const codeBtn = e.target.closest(".chat-code-copy-btn");
      if (codeBtn) {
        e.preventDefault();
        const code = codeBtn.getAttribute("data-code") || "";
        const ok = await copyTextToClipboard(code);
        toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
        return;
      }
      const quoteBtn = e.target.closest(".chat-quote-copy-btn");
      if (quoteBtn) {
        e.preventDefault();
        const quote = quoteBtn.getAttribute("data-quote") || "";
        const ok = await copyTextToClipboard(quote);
        toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
        return;
      }
      return;
    }
    e.preventDefault();
    const id = btn.getAttribute("data-msg-id");
    if (!id) return;
    const msg = chatMessages.find((x) => x.id === id);
    if (!msg) return;
    const text = String(msg.content || "");
    const ok = await copyTextToClipboard(text);
    toast(ok ? t("chat.copied") : t("chat.copy_failed"), ok ? "success" : "error");
  });
  const chatScrollHost = $("chat-scroll-shell") || $("chat-messages");
  if (chatScrollHost) {
    let isUserTouching = false;
    chatScrollHost.addEventListener("wheel", (e) => {
      if (e.deltaY < -2) {
        // User actively scrolled UP with wheel
        chatUserScrolledUp = true;
      } else if (e.deltaY > 2) {
        // User actively scrolled DOWN with wheel
        const distFromBottom = chatScrollHost.scrollHeight - chatScrollHost.clientHeight - chatScrollHost.scrollTop;
        if (distFromBottom < 100) {
          chatUserScrolledUp = false;
        }
      }
    }, { passive: true });

    chatScrollHost.addEventListener("touchstart", () => { isUserTouching = true; }, { passive: true });
    chatScrollHost.addEventListener("touchend", () => { isUserTouching = false; }, { passive: true });

    chatScrollHost.addEventListener("scroll", () => {
      const distFromBottom = chatScrollHost.scrollHeight - chatScrollHost.clientHeight - chatScrollHost.scrollTop;
      if (distFromBottom < 60) {
        chatUserScrolledUp = false;
      } else if (isUserTouching && distFromBottom > 120) {
        chatUserScrolledUp = true;
      }
    }, { passive: true });
  }
  ($("chat-scroll-shell") || $("chat-messages"))?.addEventListener("input", (e) => {
    const ta = e.target.closest(".chat-edit-textarea");
    if (ta) {
      chatEditingDraft = ta.value;
    }
  });
  ($("chat-scroll-shell") || $("chat-messages"))?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && e.target.closest(".chat-edit-textarea")) {
      e.preventDefault();
      const id = e.target.getAttribute("data-msg-id");
      if (id) void editAndResendUserMessage(id, e.target.value);
    }
    if (e.key === "Escape" && e.target.closest(".chat-edit-textarea")) {
      e.preventDefault();
      chatEditingMessageId = "";
      chatEditingDraft = "";
      renderChatMessages();
    }
  });
  const stopBtn = $("chat-stop-btn");
  if (stopBtn) {
    addFastTapListener(stopBtn, () => stopChatGeneration());
  }
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && $("chat-view")?.classList.contains("chat-options-open")) {
      $("chat-view")?.classList.remove("chat-options-open");
      syncChatPanels($("chat-view"));
      return;
    }
    if (e.code !== "Backspace" || !e.ctrlKey || !e.shiftKey) return;
    if (currentView !== "chat" || !chatStreamLock) return;
    e.preventDefault();
    stopChatGeneration();
  });
  $("chat-input")?.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if ($("chat-send-btn")?.disabled) return;
      sendChatMessage(true);
      return;
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      if ($("chat-send-btn")?.disabled) return;
      sendChatMessage(false);
    }
  });
  $("chat-input")?.addEventListener("paste", async (e) => {
    if (currentView !== "chat") return;
    const cd = e.clipboardData;
    if (!cd?.items?.length) return;
    const imageFiles = [];
    for (const item of cd.items) {
      if (item.kind === "file" && String(item.type || "").startsWith("image/")) {
        const f = item.getAsFile();
        if (f) imageFiles.push(f);
      }
    }
    if (!imageFiles.length) return;
    e.preventDefault();
    const extraText = cd.getData("text/plain") || "";
    await addFiles(imageFiles);
    if (extraText) {
      const ta = $("chat-input");
      const start = ta.selectionStart ?? ta.value.length;
      const end = ta.selectionEnd ?? ta.value.length;
      const v = ta.value;
      ta.value = v.slice(0, start) + extraText + v.slice(end);
      const pos = start + extraText.length;
      ta.setSelectionRange(pos, pos);
    }
  });
  $("chat-image-btn")?.addEventListener("click", () => $("chat-image-input")?.click());
  $("chat-audio-btn")?.addEventListener("click", () => $("chat-audio-input")?.click());
  $("chat-text-btn")?.addEventListener("click", () => $("chat-text-input")?.click());
  $("chat-record-btn")?.addEventListener("click", async () => {
    if (chatIsRecording) {
      stopAudioRecording();
    } else {
      await startAudioRecording();
    }
  });
  $("chat-image-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-image-input").files || []));
    $("chat-image-input").value = "";
  });
  $("chat-audio-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-audio-input").files || []));
    $("chat-audio-input").value = "";
  });
  $("chat-text-input")?.addEventListener("change", async () => {
    await addFiles(Array.from($("chat-text-input").files || []));
    $("chat-text-input").value = "";
  });

  const dropHost = chatView;
  dropHost.addEventListener("dragenter", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth += 1;
    $("chat-dropzone").hidden = false;
  });
  dropHost.addEventListener("dragover", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
  });
  dropHost.addEventListener("dragleave", (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth = Math.max(0, chatDndDepth - 1);
    if (chatDndDepth === 0) $("chat-dropzone").hidden = true;
  });
  dropHost.addEventListener("drop", async (e) => {
    if (currentView !== "chat") return;
    e.preventDefault();
    chatDndDepth = 0;
    $("chat-dropzone").hidden = true;
    const files = Array.from(e.dataTransfer?.files || []);
    if (!files.length) return;
    await addFiles(files);
  });

  document.addEventListener("click", (e) => {
    const open = e.target.closest(".image-preview-open");
    if (!open) return;
    const im = open.querySelector("img");
    if (!im || !im.getAttribute("src")) return;
    e.preventDefault();
    openImagePreview(im.src, open.getAttribute("data-name") || "");
  });

  const imgPrevBack = $("image-preview-backdrop");
  if (imgPrevBack) {
    imgPrevBack.addEventListener("click", closeImagePreview);
  }
  const imgPrevClose = $("image-preview-close");
  if (imgPrevClose) {
    imgPrevClose.addEventListener("click", closeImagePreview);
  }
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") return;
    const modal = $("image-preview-modal");
    if (modal && !modal.hidden) {
      e.preventDefault();
      closeImagePreview();
    }
  });

  const optionIds = [
    "chat-system",
    "chat-temperature",
    "chat-top-k",
    "chat-top-p",
    "chat-num-ctx",
    "chat-think-level",
    "chat-web-tools",
    "chat-artifacts",
    "chat-image-width",
    "chat-image-height",
    "chat-image-steps",
    "chat-image-seed"
  ];
  for (const id of optionIds) {
    const el = $(id);
    if (el) {
      const eventName = el.type === "checkbox" ? "change" : "input";
      el.addEventListener(eventName, saveChatOptionsForCurrentModel);
    }
  }
  $("chat-options-reset-btn")?.addEventListener("click", resetModelChatOptionsToDefaults);

  // Model artifacts list / load modal
  $("chat-model-artifacts-btn")?.addEventListener("click", openLoadArtifactModal);
  $("load-artifact-x")?.addEventListener("click", () => { $("load-artifact-modal").hidden = true; });
  $("load-artifact-close")?.addEventListener("click", () => { $("load-artifact-modal").hidden = true; });
  $("load-artifact-modal")?.addEventListener("click", (e) => {
    if (e.target === $("load-artifact-modal")) $("load-artifact-modal").hidden = true;
  });

  // Artifact resource button in chat compose bar
  const chatArtBtn = $("chat-artifact-btn");
  if (chatArtBtn) {
    chatArtBtn.addEventListener("click", (e) => {
      e.preventDefault();
      if (!activeArtifactTimestamp) return;
      const panel = $("chat-artifact-panel");
      const isShowing = panel && !panel.hidden;
      if (!isShowing) {
        const url = activeArtifactUrl || ("/api/artifacts/" + activeArtifactTimestamp + "/");
        const name = activeArtifactName || "Artifact";
        showArtifactPanel(url, name, false);
      }
    });
    chatArtBtn.addEventListener("contextmenu", (e) => {
      e.preventDefault();
      unloadSessionArtifact();
    });
  }

  // Artifact panel controls
  $("chat-artifact-close")?.addEventListener("click", hideArtifactPanel);
  $("chat-artifact-options")?.addEventListener("click", () => {
    const cv = $("chat-view");
    if (!cv) return;
    swapToOptions(cv);
  });
  $("chat-artifact-open")?.addEventListener("click", () => {
    const frame = $("chat-artifact-frame");
    if (frame && frame.src) window.open(frame.src, "_blank", "noopener,noreferrer");
  });
  $("chat-artifact-refresh")?.addEventListener("click", () => {
    const frame = $("chat-artifact-frame");
    if (frame && frame.src) {
      const currentSrc = frame.src;
      frame.src = "about:blank";
      requestAnimationFrame(() => { frame.src = currentSrc; });
    }
  });

  // Splitter drag-to-resize (desktop only)
  const splitter = $("chat-splitter");
  if (splitter && chatView) {
    let dragging = false;
    let overlay = null;
    splitter.addEventListener("mousedown", (e) => {
      if (window.innerWidth <= 1135) return;
      dragging = true;
      splitter.classList.add("dragging");
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      // Create overlay to prevent iframe from capturing mouse events
      overlay = document.createElement("div");
      overlay.style.cssText = "position:fixed;inset:0;z-index:9999;cursor:col-resize;background:transparent;";
      document.body.appendChild(overlay);
      e.preventDefault();
    });
    document.addEventListener("mousemove", (e) => {
      if (!dragging) return;
      const rect = chatView.getBoundingClientRect();
      const rightWidth = rect.right - e.clientX;
      applyArtifactWidth(chatView, rightWidth);
    });
    document.addEventListener("mouseup", () => {
      if (!dragging) return;
      dragging = false;
      splitter.classList.remove("dragging");
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
      if (overlay) {
        overlay.remove();
        overlay = null;
      }
      const w = chatView.style.getPropertyValue("--chat-right-width");
      if (w && w.endsWith("px")) {
        localStorage.setItem("ollama_manager_artifact_width", w);
      }
    });

    window.addEventListener("resize", () => {
      if (window.innerWidth > 1135) {
        const panel = $("chat-artifact-panel");
        if (panel && !panel.hidden && chatView) {
          const currentW = chatView.style.getPropertyValue("--chat-right-width");
          if (currentW) applyArtifactWidth(chatView, currentW);
        }
      }
    });
  }

  window.addEventListener("message", async (e) => {
    if (e.data && e.data.type === "artifact-console" && activeArtifactTimestamp) {
      try {
        await api("/api/artifacts/console", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            timestamp: activeArtifactTimestamp,
            log: `[${e.data.logType.toUpperCase()}] ${e.data.message}`
          })
        });
      } catch (err) {
        console.warn("Failed to send console log to server:", err);
      }
    }
  });
}

