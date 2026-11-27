import { api } from "./bridge.js";
import { text } from "./strings.js";

const $ = (id) => document.getElementById(id);

function row(list, term, value) {
  const dt = document.createElement("dt");
  dt.textContent = term;
  const dd = document.createElement("dd");
  dd.textContent = value;
  list.append(dt, dd);
}

async function showInfo() {
  const info = await api.appInfo();
  const list = $("info");
  list.replaceChildren();
  row(list, text.version, info.version);
  row(list, text.installedVersion, info.installedVersion || text.notInstalled);
  if (info.setupNeeded) {
    row(list, text.status, text.setupNeeded);
  }
  return info;
}

async function showStatus() {
  const out = $("status");
  out.textContent = text.loading;
  try {
    const s = await api.status(false);
    const lines = [
      s.running ? text.running : text.idle,
      !s.task || !s.task.installed ? text.noTask : s.task.paused ? text.paused : text.scheduled,
      `${text.jobs}: ${(s.jobs || []).map((j) => j.name).join(", ")}`,
    ];
    out.textContent = lines.join(" ");
  } catch (e) {
    out.textContent = `${text.error}: ${e.message}`;
  }
}

async function start() {
  document.title = text.title;
  $("title").textContent = text.title;
  $("status-title").textContent = text.status;
  $("refresh").textContent = text.refresh;
  $("refresh").addEventListener("click", showStatus);
  try {
    await showInfo();
  } catch (e) {
    $("info").textContent = `${text.error}: ${e.message}`;
  }
  await showStatus();
}

start();
