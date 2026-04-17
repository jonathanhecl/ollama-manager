"use strict";

// Wrapped in an IIFE so internal helpers (t, setLang, applyTranslations, I18N)
// don't pollute the global scope. Only window.I18n is exposed.
(function () {

// Translation dictionaries. English is the source of truth; Spanish mirrors it.
const I18N = {
  en: {
    "status.connecting": "connecting…",
    "status.online": "ollama on",
    "status.offline": "ollama off",
    "status.unreachable": "offline",

    "downloads.title": "Downloads",
    "downloads.button": "Downloads",
    "downloads.add_placeholder": "install a model (e.g. llama3:8b)",
    "downloads.add_btn": "Add to queue",
    "downloads.section_active": "Active",
    "downloads.section_queued": "Queued",
    "downloads.section_finished": "Finished",
    "downloads.section_empty": "— empty —",
    "downloads.empty": "No downloads yet. Queue a model above.",
    "downloads.cancel": "Cancel",
    "downloads.remove": "Remove",
    "downloads.retry": "Retry",
    "downloads.clear_finished": "Clear finished",
    "downloads.enqueued": "{name} added to the queue",
    "downloads.jobs_count": "{n} total",
    "downloads.status.running": "running",
    "downloads.status.queued": "queued",
    "downloads.status.done": "done",
    "downloads.status.error": "error",
    "downloads.status.cancelled": "cancelled",

    "action.refresh": "Refresh",
    "action.settings": "Settings",
    "action.logout": "Sign out",
    "action.close": "Close",
    "action.cancel": "Cancel",
    "action.delete": "Delete",

    "col.name": "Name",
    "col.family": "Family",
    "col.params": "Params",
    "col.quant": "Quant.",
    "col.context": "Context",
    "col.size": "Size",
    "col.modified": "Modified",

    "state.loading": "Loading…",
    "state.empty_models": "No models installed. Click Downloads to pull one.",
    "state.error_prefix": "Error: ",

    "detail.empty": "Select a model.",
    "detail.family": "Family",
    "detail.architecture": "Architecture",
    "detail.params": "Parameters",
    "detail.quant": "Quantization",
    "detail.format": "Format",
    "detail.context": "Context",
    "detail.size": "Size",
    "detail.state": "State",
    "detail.modified": "Modified",
    "detail.digest": "Digest",
    "detail.loaded_vram": "loaded · VRAM {size}",
    "detail.not_loaded": "not loaded",
    "detail.capabilities": "Capabilities",
    "detail.parameters_section": "Parameters",
    "detail.template": "Template",
    "detail.dot_loaded": "loaded in memory",
    "detail.dot_not_loaded": "not loaded",
    "detail.delete_title": "Delete model",

    "confirm.title": "Confirm",
    "confirm.delete_text": "{name} will be uninstalled from the system. This action cannot be undone.",

    "settings.title": "Settings",
    "settings.general": "General",
    "settings.network": "Network",
    "settings.language": "Language",
    "settings.port": "HTTP port",
    "settings.expose": "Expose to local network",
    "settings.expose_hint": "Allow other devices on your LAN to connect.",
    "settings.expose_warn": "With no password, anyone on your LAN can manage Ollama.",
    "settings.restart_hint": "Port and network changes take effect after restarting the server.",
    "settings.password_section": "Password",
    "settings.new_password": "New password",
    "settings.save_password": "Save",
    "settings.clear_password": "Remove password",
    "settings.save": "Save changes",
    "settings.pwd_set": "Enabled",
    "settings.pwd_unset": "Disabled",
    "settings.bind_local": "local only",
    "settings.bind_lan": "open to LAN",
    "settings.saved": "Settings saved",
    "settings.saved_restart": "Settings saved — restart the server to apply",
    "settings.pwd_saved": "Password updated",
    "settings.pwd_cleared": "Password removed",
    "settings.pwd_too_short": "Password is too short",

    "toast.deleted": "Deleted {name}",
    "toast.delete_error": "Error deleting: {msg}",
    "toast.error": "Error: {msg}",
  },

  es: {
    "status.connecting": "conectando…",
    "status.online": "ollama on",
    "status.offline": "ollama off",
    "status.unreachable": "sin conexión",

    "downloads.title": "Descargas",
    "downloads.button": "Descargas",
    "downloads.add_placeholder": "instalar modelo (ej. llama3:8b)",
    "downloads.add_btn": "Agregar a la cola",
    "downloads.section_active": "Activa",
    "downloads.section_queued": "En cola",
    "downloads.section_finished": "Finalizadas",
    "downloads.section_empty": "— vacío —",
    "downloads.empty": "No hay descargas todavía. Agregá un modelo arriba.",
    "downloads.cancel": "Cancelar",
    "downloads.remove": "Quitar",
    "downloads.retry": "Reintentar",
    "downloads.clear_finished": "Limpiar finalizadas",
    "downloads.enqueued": "{name} agregado a la cola",
    "downloads.jobs_count": "{n} en total",
    "downloads.status.running": "descargando",
    "downloads.status.queued": "en cola",
    "downloads.status.done": "listo",
    "downloads.status.error": "error",
    "downloads.status.cancelled": "cancelado",

    "action.refresh": "Refrescar",
    "action.settings": "Ajustes",
    "action.logout": "Salir",
    "action.close": "Cerrar",
    "action.cancel": "Cancelar",
    "action.delete": "Eliminar",

    "col.name": "Nombre",
    "col.family": "Familia",
    "col.params": "Parámetros",
    "col.quant": "Cuant.",
    "col.context": "Contexto",
    "col.size": "Tamaño",
    "col.modified": "Modificado",

    "state.loading": "Cargando…",
    "state.empty_models": "No hay modelos instalados. Tocá Descargas para instalar uno.",
    "state.error_prefix": "Error: ",

    "detail.empty": "Selecciona un modelo.",
    "detail.family": "Familia",
    "detail.architecture": "Arquitectura",
    "detail.params": "Parámetros",
    "detail.quant": "Cuantización",
    "detail.format": "Formato",
    "detail.context": "Contexto",
    "detail.size": "Tamaño",
    "detail.state": "Estado",
    "detail.modified": "Modificado",
    "detail.digest": "Digest",
    "detail.loaded_vram": "cargado · VRAM {size}",
    "detail.not_loaded": "no cargado",
    "detail.capabilities": "Capacidades",
    "detail.parameters_section": "Parámetros",
    "detail.template": "Template",
    "detail.dot_loaded": "cargado en memoria",
    "detail.dot_not_loaded": "no cargado",
    "detail.delete_title": "Eliminar modelo",

    "confirm.title": "Confirmar",
    "confirm.delete_text": "Se desinstalará {name} del sistema. Esta acción no se puede deshacer.",

    "settings.title": "Ajustes",
    "settings.general": "General",
    "settings.network": "Red",
    "settings.language": "Idioma",
    "settings.port": "Puerto HTTP",
    "settings.expose": "Exponer a la red local",
    "settings.expose_hint": "Permite conexiones desde otros equipos de tu LAN.",
    "settings.expose_warn": "Sin contraseña, cualquiera en tu LAN puede gestionar Ollama.",
    "settings.restart_hint": "Los cambios de puerto y red surten efecto al reiniciar el servidor.",
    "settings.password_section": "Contraseña",
    "settings.new_password": "Nueva contraseña",
    "settings.save_password": "Guardar",
    "settings.clear_password": "Quitar contraseña",
    "settings.save": "Guardar cambios",
    "settings.pwd_set": "Activada",
    "settings.pwd_unset": "Desactivada",
    "settings.bind_local": "solo local",
    "settings.bind_lan": "abierto a LAN",
    "settings.saved": "Ajustes guardados",
    "settings.saved_restart": "Ajustes guardados — reiniciá el servidor para aplicar",
    "settings.pwd_saved": "Contraseña actualizada",
    "settings.pwd_cleared": "Contraseña eliminada",
    "settings.pwd_too_short": "La contraseña es demasiado corta",

    "toast.deleted": "Eliminado {name}",
    "toast.delete_error": "Error eliminando: {msg}",
    "toast.error": "Error: {msg}",
  },
};

let _lang = "en";

function setLang(lang) {
  if (!I18N[lang]) lang = "en";
  _lang = lang;
  document.documentElement.lang = lang;
  applyTranslations();
}

function getLang() { return _lang; }

// t(key, vars?) returns a translated string with {var} substitutions.
function t(key, vars) {
  const dict = I18N[_lang] || I18N.en;
  let s = dict[key] ?? I18N.en[key] ?? key;
  if (vars) {
    for (const k of Object.keys(vars)) {
      s = s.split("{" + k + "}").join(String(vars[k]));
    }
  }
  return s;
}

// applyTranslations walks the DOM and updates every [data-i18n] node.
// - data-i18n="key" sets textContent (default).
// - data-i18n-attr="title placeholder" sets the listed attributes instead.
function applyTranslations(root = document) {
  root.querySelectorAll("[data-i18n]").forEach((el) => {
    const key = el.getAttribute("data-i18n");
    const value = t(key);
    const attrSpec = el.getAttribute("data-i18n-attr");
    if (attrSpec) {
      attrSpec.split(/\s+/).forEach((a) => { if (a) el.setAttribute(a, value); });
      // If element also wants its text replaced, opt-in via data-i18n-text.
      if (el.hasAttribute("data-i18n-text")) el.textContent = value;
    } else {
      el.textContent = value;
    }
  });
}

// Expose to global scope for app.js.
window.I18n = { setLang, getLang, t, applyTranslations };

})();
