// Everything here must be refused by the window; the test reads what the
// host reports. The final app.info call tells the test that the page is
// done, and whether the inline script in index.html ran.

const post = (m) => window.chrome.webview.postMessage(typeof m === "string" ? m : JSON.stringify(m));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function attempt(fn) {
  try {
    await fn();
  } catch (e) {
    // expected
  }
}

async function main() {
  // Requests first: after the cancelled download below, the runtime fails
  // later fetches before they reach the host.
  await attempt(() => fetch("missing.txt"));
  await attempt(() => fetch("index.html", { method: "POST" }));
  await attempt(() => fetch("https://example.com/fetch"));

  await attempt(() => window.open("https://example.com/popup"));
  await attempt(() => window.open("index.html"));

  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob(["x"], { type: "application/octet-stream" }));
  a.download = "x.bin";
  document.body.append(a);
  a.click();

  await attempt(() => navigator.clipboard.readText());

  post({ id: 1, call: "no.such.call" });
  post({ id: 2, call: "app.info", args: { extra: 1 } });
  post({ id: 3, call: "status.get", args: { withSnapshots: "yes" } });
  post({ id: 4, call: "snapshots.ls", args: { snapshot: "--config=other.toml", folder: "" } });
  post({ id: 5, call: "job.backupNow", args: { job: "-x" } });
  post("not json");
  post({ id: 6, call: "status.get", args: { withSnapshots: false } });

  // Frames cannot load at all: every response says frame-ancestors 'none'.
  const f = document.createElement("iframe");
  f.src = "frame.html";
  document.body.append(f);
  await sleep(1500);

  location.href = "https://example.com/away";
  await sleep(1000);

  post({ id: 7, call: "app.info", args: {} });
  if (window.inlineRan) {
    post({ id: 8, call: "app.info" });
  }
}

main();
