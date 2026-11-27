// The only way the page reaches Go. Each function sends one call of the
// bridge contract and returns a promise settled by Go's reply; names that
// are not listed here do not exist. Go also sends events, which reach the
// listeners added with onEvent.

const pending = new Map();
const listeners = new Map();
let nextId = 1;

window.chrome.webview.addEventListener("message", (e) => {
  const m = e.data;
  if (m === null || typeof m !== "object") {
    return;
  }
  if (typeof m.id === "number") {
    const p = pending.get(m.id);
    if (p === undefined) {
      return;
    }
    pending.delete(m.id);
    if (m.error) {
      p.reject(new BridgeError(m.error));
    } else {
      p.resolve(m.result);
    }
    return;
  }
  if (typeof m.event === "string") {
    for (const fn of listeners.get(m.event) || []) {
      fn(m.data);
    }
  }
});

export class BridgeError extends Error {
  constructor(e) {
    super(e.message);
    this.code = e.code;
    this.fields = e.fields || {};
  }
}

function call(name, args) {
  const id = nextId++;
  const promise = new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
  });
  window.chrome.webview.postMessage(JSON.stringify({ id, call: name, args }));
  return promise;
}

export function onEvent(name, fn) {
  if (!listeners.has(name)) {
    listeners.set(name, []);
  }
  listeners.get(name).push(fn);
}

export const api = Object.freeze({
  appInfo: () => call("app.info"),
  pickFolder: (start) => call("dialog.pickFolder", { start: start || "" }),
  status: (withSnapshots) => call("status.get", { withSnapshots }),
  backupNow: (job) => call("job.backupNow", { job }),
  pause: () => call("task.pause"),
  resume: () => call("task.resume"),
  snapshots: (job) => call("snapshots.list", { job }),
  listFolder: (snapshot, folder) => call("snapshots.ls", { snapshot, folder: folder || "" }),
});
