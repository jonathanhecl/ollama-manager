let pendingAuthPromise = null;

function promptInPlaceAuth() {
  if (pendingAuthPromise) return pendingAuthPromise;
  if (typeof saveActiveChatSession === "function") {
    saveActiveChatSession();
  }
  const modal = $("auth-modal");
  const input = $("auth-modal-password");
  const errEl = $("auth-modal-error");
  if (!modal || !input) {
    window.location.href = "/login";
    return Promise.reject(new Error("unauthorized"));
  }

  modal.hidden = false;
  if (errEl) { errEl.hidden = true; errEl.textContent = ""; }
  input.value = "";
  setTimeout(() => input.focus(), 50);

  pendingAuthPromise = new Promise((resolve) => {
    const onKeyDown = (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        submitAuth();
      }
    };
    const submitAuth = async () => {
      const password = input.value;
      if (!password) return;
      const submitBtn = $("auth-modal-submit");
      if (submitBtn) submitBtn.disabled = true;
      try {
        const res = await fetch("/api/login", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          credentials: "same-origin",
          body: JSON.stringify({ password }),
        });
        if (!res.ok) {
          const j = await res.json().catch(() => ({}));
          throw new Error(j.error || (typeof t === "function" ? t("auth.error") : "Incorrect password"));
        }
        modal.hidden = true;
        input.removeEventListener("keydown", onKeyDown);
        pendingAuthPromise = null;
        if (typeof toast === "function" && typeof t === "function") {
          toast(t("auth.unlocked"), "success");
        }
        resolve(true);
      } catch (err) {
        if (errEl) {
          errEl.textContent = err.message || "Incorrect password";
          errEl.hidden = false;
        }
        input.focus();
        input.select();
      } finally {
        if (submitBtn) submitBtn.disabled = false;
      }
    };

    input.addEventListener("keydown", onKeyDown);
    const submitBtn = $("auth-modal-submit");
    if (submitBtn) {
      submitBtn.onclick = (e) => {
        e.preventDefault();
        submitAuth();
      };
    }
  });

  return pendingAuthPromise;
}

// ---------- API ----------
async function api(path, opts = {}) {
  const reqOpts = { credentials: "same-origin", ...opts };
  if (reqOpts.body && typeof reqOpts.body === "object" && !(reqOpts.body instanceof FormData) && !(reqOpts.body instanceof Blob) && !(reqOpts.body instanceof ArrayBuffer)) {
    reqOpts.headers = { "Content-Type": "application/json", ...reqOpts.headers };
    reqOpts.body = JSON.stringify(reqOpts.body);
  }
  let res = await fetch(path, reqOpts);
  if (res.status === 401) {
    if (path === "/api/login") {
      throw new Error("unauthorized");
    }
    // Wait for in-place re-authentication
    await promptInPlaceAuth();
    // Retry request with fresh session cookie
    res = await fetch(path, reqOpts);
  }
  if (res.status === 401) {
    window.location.href = "/login";
    throw new Error("unauthorized");
  }
  if (!res.ok) {
    let err = res.statusText;
    try { const j = await res.json(); if (j.error) err = j.error; } catch { }
    throw new Error(err);
  }
  return res.json();
}

