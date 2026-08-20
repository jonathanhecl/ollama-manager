"use strict";

// ---------- API ----------
async function api(path, opts = {}) {
  const reqOpts = { credentials: "same-origin", ...opts };
  if (reqOpts.body && typeof reqOpts.body === "object" && !(reqOpts.body instanceof FormData) && !(reqOpts.body instanceof Blob) && !(reqOpts.body instanceof ArrayBuffer)) {
    reqOpts.headers = { "Content-Type": "application/json", ...reqOpts.headers };
    reqOpts.body = JSON.stringify(reqOpts.body);
  }
  const res = await fetch(path, reqOpts);
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

