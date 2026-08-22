"use strict";

(function () {
  let activeTab = "builder"; // "builder" | "raw"
  let isCreating = false;
  let createdModelName = "";

  const SYSTEM_PRESETS = {
    coder: `You are an expert software engineer and technical architect.
- Write clean, efficient, well-structured, and robust code.
- Prefer modern idioms, best practices, and error handling.
- Keep explanations clear and concise.`,
    concise: `You are a direct, concise, and highly effective AI assistant.
- Provide direct answers without filler, preambles, or excessive pleasantries.
- Use structured bullet points and tables when helpful.`,
    translator: `You are a professional translator and multilingual specialist.
- Translate text accurately while preserving tone, idiom, and cultural context.
- Maintain formatting and technical terms precisely.`,
    tutor: `You are an encouraging and insightful tutor using the Socratic method.
- Guide the learner with step-by-step questions and constructive hints.
- Explain core concepts intuitively before diving into details.`,
    clear: "",
  };

  function $(id) {
    return document.getElementById(id);
  }

  function initModelfileStudio() {
    // Open button from topbar
    const topbarBtn = $("modelfile-btn");
    if (topbarBtn) {
      topbarBtn.addEventListener("click", () => showModelfileView());
    }

    // Back button from modelfile-view
    const backBtn = $("modelfile-back-btn");
    if (backBtn) {
      backBtn.addEventListener("click", () => {
        if (typeof showModelsView === "function") {
          showModelsView();
        }
      });
    }

    // Tab switching
    const tabBuilder = $("mf-tab-builder");
    const tabRaw = $("mf-tab-raw");
    if (tabBuilder && tabRaw) {
      tabBuilder.addEventListener("click", () => switchTab("builder"));
      tabRaw.addEventListener("click", () => switchTab("raw"));
    }

    // Toggle custom base model input
    const customToggle = $("mf-base-toggle-custom");
    if (customToggle) {
      customToggle.addEventListener("click", () => {
        const sel = $("mf-base-model-select");
        const inp = $("mf-base-model-custom");
        const isCustom = !inp.hidden;
        inp.hidden = isCustom;
        sel.hidden = !isCustom;
        if (!isCustom) {
          inp.focus();
        }
        updatePreview();
      });
    }

    // Inputs change listeners for live preview
    [
      "mf-model-name",
      "mf-base-model-select",
      "mf-base-model-custom",
      "mf-system-prompt",
      "mf-num-ctx",
      "mf-top-k",
      "mf-repeat-penalty",
      "mf-stop-tokens",
      "mf-template",
    ].forEach((id) => {
      const el = $(id);
      if (el) {
        el.addEventListener("input", updatePreview);
        el.addEventListener("change", updatePreview);
      }
    });

    // Sliders with value display
    const tempEl = $("mf-temperature");
    const tempVal = $("mf-temperature-val");
    if (tempEl && tempVal) {
      tempEl.addEventListener("input", () => {
        tempVal.textContent = parseFloat(tempEl.value).toFixed(2);
        updatePreview();
      });
    }

    const topPEl = $("mf-top-p");
    const topPVal = $("mf-top-p-val");
    if (topPEl && topPVal) {
      topPEl.addEventListener("input", () => {
        topPVal.textContent = parseFloat(topPEl.value).toFixed(2);
        updatePreview();
      });
    }

    // Presets buttons
    document.querySelectorAll(".mf-preset-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        const presetKey = btn.dataset.preset;
        if (presetKey in SYSTEM_PRESETS) {
          const sysEl = $("mf-system-prompt");
          if (sysEl) {
            sysEl.value = SYSTEM_PRESETS[presetKey];
            updatePreview();
          }
        }
      });
    });

    // Num_ctx quick chips
    document.querySelectorAll(".mf-chip-btn").forEach((btn) => {
      btn.addEventListener("click", () => {
        const ctxEl = $("mf-num-ctx");
        if (ctxEl) {
          ctxEl.value = btn.dataset.val;
          updatePreview();
        }
      });
    });

    // Raw editor input
    const rawEl = $("mf-raw-modelfile");
    if (rawEl) {
      rawEl.addEventListener("input", () => {
        if (activeTab === "raw") {
          const codeEl = $("mf-preview-code");
          if (codeEl) codeEl.textContent = rawEl.value;
        }
      });
    }

    // Copy Modelfile button
    const copyBtn = $("mf-copy-btn");
    if (copyBtn) {
      copyBtn.addEventListener("click", () => {
        const text = getGeneratedModelfile();
        if (!text) return;
        navigator.clipboard.writeText(text).then(() => {
          showToast(t("modelfile.copied") || "Copied!");
        });
      });
    }

    // Create Model button
    const createBtn = $("mf-create-btn");
    if (createBtn) {
      createBtn.addEventListener("click", handleCreateModelSubmit);
    }

    // Test in chat button
    const testChatBtn = $("mf-test-chat-btn");
    if (testChatBtn) {
      testChatBtn.addEventListener("click", () => {
        if (createdModelName && typeof showChatViewWithModel === "function") {
          showChatViewWithModel(createdModelName);
        } else if (createdModelName && typeof selectChatModel === "function" && typeof showChatView === "function") {
          selectChatModel(createdModelName);
          showChatView();
        }
      });
    }
  }

  function showModelfileView(baseModelName) {
    if (typeof hideAllMainViews === "function") {
      hideAllMainViews();
    }
    if (typeof stopSpeechPlayback === "function") {
      stopSpeechPlayback();
    }

    currentView = "modelfile";
    const view = $("modelfile-view");
    if (view) view.hidden = false;

    if (!window.location.pathname.startsWith("/modelfile")) {
      history.pushState(null, "", "/modelfile");
    }

    createdModelName = "";
    isCreating = false;

    // Reset status & buttons
    const statusWrap = $("mf-status-wrap");
    if (statusWrap) statusWrap.hidden = true;
    const testChatBtn = $("mf-test-chat-btn");
    if (testChatBtn) testChatBtn.hidden = true;
    const createBtn = $("mf-create-btn");
    if (createBtn) {
      createBtn.disabled = false;
      createBtn.textContent = t("modelfile.btn_create");
    }

    // Populate base model select
    const sel = $("mf-base-model-select");
    const customInp = $("mf-base-model-custom");
    if (sel && customInp) {
      sel.innerHTML = "";
      customInp.hidden = true;
      customInp.value = "";
      sel.hidden = false;

      const installed = Array.isArray(models) ? models.filter((m) => !m.archived) : [];
      if (installed.length === 0) {
        const opt = document.createElement("option");
        opt.value = "";
        opt.textContent = "— No models installed —";
        sel.appendChild(opt);
      } else {
        installed.forEach((m) => {
          const opt = document.createElement("option");
          opt.value = m.name;
          opt.textContent = m.name + (m.parameter_size ? ` (${m.parameter_size})` : "");
          sel.appendChild(opt);
        });
      }

      if (baseModelName) {
        const exists = installed.some((m) => m.name === baseModelName);
        if (exists) {
          sel.value = baseModelName;
        } else {
          customInp.hidden = false;
          customInp.value = baseModelName;
          sel.hidden = true;
        }
      }
    }

    // Set default target name if empty or derived
    const nameInp = $("mf-model-name");
    if (nameInp) {
      if (baseModelName) {
        const baseClean = baseModelName.split(":")[0];
        nameInp.value = `${baseClean}-custom:latest`;
      } else if (!nameInp.value) {
        nameInp.value = "my-custom-model:latest";
      }
    }

    switchTab("builder");
    updatePreview();

    if (nameInp) nameInp.focus();
  }

  function switchTab(tab) {
    activeTab = tab;
    const tabBuilder = $("mf-tab-builder");
    const tabRaw = $("mf-tab-raw");
    const paneBuilder = $("mf-builder-pane");
    const paneRaw = $("mf-raw-pane");
    const rawEl = $("mf-raw-modelfile");

    if (tab === "raw") {
      if (tabBuilder) tabBuilder.classList.remove("active");
      if (tabRaw) tabRaw.classList.add("active");
      if (paneBuilder) paneBuilder.hidden = true;
      if (paneRaw) paneRaw.hidden = false;

      // Sync form into raw editor
      if (rawEl) {
        rawEl.value = getGeneratedModelfile();
      }
    } else {
      if (tabBuilder) tabBuilder.classList.add("active");
      if (tabRaw) tabRaw.classList.remove("active");
      if (paneBuilder) paneBuilder.hidden = false;
      if (paneRaw) paneRaw.hidden = true;

      // Try parsing raw editor back to form if modified
      if (rawEl && rawEl.value) {
        parseRawModelfileToForm(rawEl.value);
      }
    }
    updatePreview();
  }

  function getBaseModelValue() {
    const customInp = $("mf-base-model-custom");
    if (customInp && !customInp.hidden && customInp.value.trim()) {
      return customInp.value.trim();
    }
    const sel = $("mf-base-model-select");
    return sel ? sel.value.trim() : "";
  }

  function getGeneratedModelfile() {
    if (activeTab === "raw") {
      const rawEl = $("mf-raw-modelfile");
      return rawEl ? rawEl.value.trim() : "";
    }

    const lines = [];
    const base = getBaseModelValue();
    if (base) {
      lines.push(`FROM ${base}`);
    }

    const systemPrompt = $("mf-system-prompt")?.value?.trim();
    if (systemPrompt) {
      lines.push(`\nSYSTEM """${systemPrompt}"""`);
    }

    const tempEl = $("mf-temperature");
    if (tempEl && tempEl.value !== "") {
      const v = parseFloat(tempEl.value);
      if (!isNaN(v) && v !== 0.7) {
        lines.push(`PARAMETER temperature ${v.toFixed(2)}`);
      }
    }

    const topPEl = $("mf-top-p");
    if (topPEl && topPEl.value !== "") {
      const v = parseFloat(topPEl.value);
      if (!isNaN(v) && v !== 0.9) {
        lines.push(`PARAMETER top_p ${v.toFixed(2)}`);
      }
    }

    const topKEl = $("mf-top-k");
    if (topKEl && topKEl.value !== "") {
      const v = parseInt(topKEl.value, 10);
      if (!isNaN(v) && v !== 40) {
        lines.push(`PARAMETER top_k ${v}`);
      }
    }

    const ctxEl = $("mf-num-ctx");
    if (ctxEl && ctxEl.value.trim() !== "") {
      const v = parseInt(ctxEl.value, 10);
      if (!isNaN(v) && v > 0) {
        lines.push(`PARAMETER num_ctx ${v}`);
      }
    }

    const repEl = $("mf-repeat-penalty");
    if (repEl && repEl.value !== "") {
      const v = parseFloat(repEl.value);
      if (!isNaN(v) && v !== 1.1) {
        lines.push(`PARAMETER repeat_penalty ${v.toFixed(2)}`);
      }
    }

    const stopEl = $("mf-stop-tokens")?.value?.trim();
    if (stopEl) {
      const tokens = stopEl.split(",").map((s) => s.trim()).filter(Boolean);
      tokens.forEach((t) => {
        lines.push(`PARAMETER stop "${t}"`);
      });
    }

    const tmplEl = $("mf-template")?.value?.trim();
    if (tmplEl) {
      lines.push(`\nTEMPLATE """${tmplEl}"""`);
    }

    return lines.join("\n").trim();
  }

  function parseRawModelfileToForm(text) {
    if (!text) return;
    const lines = text.split("\n");
    let inSystem = false;
    let systemBuffer = [];
    let inTemplate = false;
    let templateBuffer = [];
    const stopTokens = [];

    for (let i = 0; i < lines.length; i++) {
      let line = lines[i];

      if (inSystem) {
        if (line.includes('"""')) {
          systemBuffer.push(line.substring(0, line.indexOf('"""')));
          inSystem = false;
          const sysEl = $("mf-system-prompt");
          if (sysEl) sysEl.value = systemBuffer.join("\n").trim();
        } else {
          systemBuffer.push(line);
        }
        continue;
      }

      if (inTemplate) {
        if (line.includes('"""')) {
          templateBuffer.push(line.substring(0, line.indexOf('"""')));
          inTemplate = false;
          const tmplEl = $("mf-template");
          if (tmplEl) tmplEl.value = templateBuffer.join("\n").trim();
        } else {
          templateBuffer.push(line);
        }
        continue;
      }

      const trimmed = line.trim();
      if (trimmed.startsWith("FROM ")) {
        const fromVal = trimmed.substring(5).trim();
        const sel = $("mf-base-model-select");
        const customInp = $("mf-base-model-custom");
        const exists = Array.from(sel.options).some((o) => o.value === fromVal);
        if (exists) {
          sel.value = fromVal;
          sel.hidden = false;
          customInp.hidden = true;
        } else {
          customInp.value = fromVal;
          customInp.hidden = false;
          sel.hidden = true;
        }
      } else if (trimmed.startsWith('SYSTEM """')) {
        const rest = trimmed.substring(10);
        if (rest.includes('"""')) {
          const sysEl = $("mf-system-prompt");
          if (sysEl) sysEl.value = rest.substring(0, rest.indexOf('"""')).trim();
        } else {
          inSystem = true;
          systemBuffer = [rest];
        }
      } else if (trimmed.startsWith("SYSTEM ")) {
        const sysEl = $("mf-system-prompt");
        if (sysEl) sysEl.value = trimmed.substring(7).trim();
      } else if (trimmed.startsWith('TEMPLATE """')) {
        const rest = trimmed.substring(12);
        if (rest.includes('"""')) {
          const tmplEl = $("mf-template");
          if (tmplEl) tmplEl.value = rest.substring(0, rest.indexOf('"""')).trim();
        } else {
          inTemplate = true;
          templateBuffer = [rest];
        }
      } else if (trimmed.startsWith("PARAMETER ")) {
        const parts = trimmed.substring(10).trim().split(/\s+/);
        const param = parts[0];
        const val = parts.slice(1).join(" ").replace(/^"|"$/g, "");
        if (param === "temperature") {
          const el = $("mf-temperature");
          const valEl = $("mf-temperature-val");
          if (el) el.value = val;
          if (valEl) valEl.textContent = parseFloat(val).toFixed(2);
        } else if (param === "top_p") {
          const el = $("mf-top-p");
          const valEl = $("mf-top-p-val");
          if (el) el.value = val;
          if (valEl) valEl.textContent = parseFloat(val).toFixed(2);
        } else if (param === "top_k") {
          const el = $("mf-top-k");
          if (el) el.value = val;
        } else if (param === "num_ctx") {
          const el = $("mf-num-ctx");
          if (el) el.value = val;
        } else if (param === "repeat_penalty") {
          const el = $("mf-repeat-penalty");
          if (el) el.value = val;
        } else if (param === "stop") {
          stopTokens.push(val);
        }
      }
    }

    if (stopTokens.length > 0) {
      const stopEl = $("mf-stop-tokens");
      if (stopEl) stopEl.value = stopTokens.join(", ");
    }
  }

  function updatePreview() {
    const codeEl = $("mf-preview-code");
    if (codeEl) {
      const text = getGeneratedModelfile();
      codeEl.textContent = text || "# Complete the fields to preview Modelfile";
    }
  }

  async function handleCreateModelSubmit() {
    if (isCreating) return;

    const nameInp = $("mf-model-name");
    const targetName = nameInp ? nameInp.value.trim() : "";
    if (!targetName) {
      showToast(t("modelfile.missing_name") || "Please enter a model name.");
      if (nameInp) nameInp.focus();
      return;
    }

    const baseModel = getBaseModelValue();
    if (!baseModel && activeTab === "builder") {
      showToast(t("modelfile.missing_base") || "Please select or enter a base model (FROM).");
      return;
    }

    const modelfileContent = getGeneratedModelfile();
    if (!modelfileContent) {
      showToast("Modelfile is empty.");
      return;
    }

    const createBtn = $("mf-create-btn");
    const statusWrap = $("mf-status-wrap");
    const statusText = $("mf-status-text");
    const progressFill = $("mf-progress-fill");
    const testChatBtn = $("mf-test-chat-btn");

    isCreating = true;
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = t("modelfile.building") || "Building…";
    }
    if (testChatBtn) testChatBtn.hidden = true;
    if (statusWrap) statusWrap.hidden = false;
    if (progressFill) progressFill.style.width = "0%";
    if (statusText) statusText.textContent = "Connecting to Ollama…";

    try {
      const res = await fetch("/api/models/create", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "text/event-stream",
        },
        body: JSON.stringify({
          name: targetName,
          from: baseModel,
          modelfile: modelfileContent,
        }),
      });

      if (!res.ok) {
        const txt = await res.text();
        let errMsg = txt;
        try {
          const errObj = JSON.parse(txt);
          if (errObj.error) errMsg = errObj.error;
        } catch (_) {}
        throw new Error(errMsg || `HTTP ${res.status}`);
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop();

        let currentEvent = "message";
        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) {
            currentEvent = "message";
            continue;
          }
          if (trimmed.startsWith("event:")) {
            currentEvent = trimmed.slice(6).trim();
            continue;
          }
          if (trimmed.startsWith("data:")) {
            const rawData = trimmed.slice(5).trim();
            try {
              const data = JSON.parse(rawData);
              handleCreateSSEEvent(currentEvent, data);
            } catch (_) {}
          }
        }
      }

      createdModelName = targetName;
      if (progressFill) progressFill.style.width = "100%";
      if (statusText) statusText.textContent = t("modelfile.success") || "Model created successfully!";
      if (testChatBtn) testChatBtn.hidden = false;
      showToast(t("modelfile.success") || "Model created successfully!");

      // Refresh global model list
      if (typeof fetchModels === "function") {
        fetchModels();
      }
    } catch (e) {
      if (statusText) statusText.textContent = `Error: ${e.message}`;
      showToast(`${t("modelfile.error") || "Error"}: ${e.message}`);
    } finally {
      isCreating = false;
      if (createBtn) {
        createBtn.disabled = false;
        createBtn.textContent = t("modelfile.btn_create");
      }
    }
  }

  function handleCreateSSEEvent(event, data) {
    const statusText = $("mf-status-text");
    const progressFill = $("mf-progress-fill");

    if (event === "error" || data.error) {
      if (statusText) statusText.textContent = `Error: ${data.error}`;
      return;
    }

    if (data.status) {
      let label = data.status;
      if (data.total && data.completed) {
        const pct = Math.min(100, Math.round((data.completed / data.total) * 100));
        label += ` (${pct}%)`;
        if (progressFill) progressFill.style.width = `${pct}%`;
      } else {
        if (progressFill && parseFloat(progressFill.style.width || "0") < 80) {
          const current = parseFloat(progressFill.style.width || "0");
          progressFill.style.width = `${Math.min(85, current + 15)}%`;
        }
      }
      if (statusText) statusText.textContent = label;
    }
  }

  // Hook into global scope
  window.showModelfileView = showModelfileView;
  window.openModelfileStudio = showModelfileView; // backwards compatibility alias

  // Initialize when DOM is ready
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initModelfileStudio);
  } else {
    initModelfileStudio();
  }
})();
