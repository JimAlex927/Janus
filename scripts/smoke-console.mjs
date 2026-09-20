// Exercise the actual release binary, not a substituted Handler or asset URL.
// Usage: node scripts/smoke-console.mjs bin/janus /janus
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { createServer } from "node:net";
import { once } from "node:events";
import { setTimeout as delay } from "node:timers/promises";

const binary = resolve(process.argv[2] || "bin/janus");
const mount = (process.argv[3] || "/").replace(/\/+$/, "") + "/";
const stage = mkdtempSync(join(tmpdir(), "janus-console-smoke-"));
const password = "janus-local-smoke-only";
const hash = spawnSync("go", ["run", "./cmd/janus-hash", "-password-stdin", "-cost", "4"], { input: password, encoding: "utf8" });
assert.equal(hash.status, 0, hash.stderr);
async function freePort() {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const port = server.address().port;
  await new Promise((resolve, reject) => server.close(err => err ? reject(err) : resolve()));
  return port;
}
const adminPort = await freePort();
const origin = `http://127.0.0.1:${adminPort}`;
const config = {
  listen: `127.0.0.1:${await freePort()}`,
  settings: {
    admin: { address: `127.0.0.1:${adminPort}`, username: "admin", password_hash: hash.stdout.trim() },
    shutdown: { load_balancer_removal_delay: "0s" },
  },
  services: { smoke: { upstreams: ["http://127.0.0.1:1"] } },
  routes: [{ name: "smoke", path_prefix: "/", service: "smoke" }],
};
const configPath = join(stage, "config.json");
writeFileSync(configPath, JSON.stringify(config));
let logs = "";
const child = spawn(binary, ["-config", configPath], { stdio: ["ignore", "pipe", "pipe"] });
child.stdout.on("data", data => { logs += data; });
child.stderr.on("data", data => { logs += data; });
const exited = once(child, "exit");
const get = (path, init = {}) => fetch(new URL(path, origin), { ...init, signal: init.signal || AbortSignal.timeout(5000) });
try {
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if (child.exitCode !== null) throw new Error(`Janus exited: ${logs}`);
    try { ready = (await get("/readyz")).ok; } catch { /* startup */ }
    if (ready) break;
    await delay(100);
  }
  assert.ok(ready, logs);
  if (mount !== "/") {
    const redirect = await get(mount.slice(0, -1), { redirect: "manual" });
    assert.equal(redirect.status, 307);
    assert.equal(redirect.headers.get("location"), mount);
  }
  for (const path of [mount, `${mount}index.html`]) {
    const response = await get(path);
    assert.equal(response.status, 200);
    const html = await response.text();
    const base = html.match(/<base href="([^"]+)">/)?.[1];
    assert.equal(base, mount);
    const documentBase = new URL(base, response.url);
    const assets = [...html.matchAll(/(?:src|href)="([^"]*assets\/[^\"]+)"/g)];
    assert.ok(assets.length >= 2);
    for (const [, ref] of assets) {
      const asset = await fetch(new URL(ref, documentBase), { signal: AbortSignal.timeout(5000) });
      assert.equal(asset.status, 200, ref);
      assert.ok((await asset.arrayBuffer()).byteLength > 0);
    }
  }
  const api = `${mount}api/v1/`;
  assert.equal((await get(api + "config")).status, 401);
  const login = await get(api + "auth/login", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: "admin", password }),
  });
  assert.equal(login.status, 200);
  const cookie = login.headers.getSetCookie()[0];
  assert.ok(cookie.includes(`Path=${mount};`), cookie);
  const headers = { Cookie: cookie.split(";")[0] };
  assert.equal((await get(api + "config", { headers })).status, 200);
  const heartbeatCount = Number(process.env.JANUS_SMOKE_HEARTBEATS || "0");
  assert.ok(Number.isInteger(heartbeatCount) && heartbeatCount >= 0 && heartbeatCount <= 4);
  const stream = await get(api + "events", { headers, signal: AbortSignal.timeout(heartbeatCount * 15000 + 5000) });
  assert.equal(stream.headers.get("content-type"), "text/event-stream");
  const reader = stream.body.getReader();
  assert.match(new TextDecoder().decode((await reader.read()).value), /event: ready/);
  let heartbeatData = "";
  while ((heartbeatData.match(/: keep-alive/g) || []).length < heartbeatCount) {
    const chunk = await reader.read();
    assert.equal(chunk.done, false, "SSE ended before requested heartbeats");
    heartbeatData += new TextDecoder().decode(chunk.value);
  }
  await reader.cancel();
  const logout = await get(api + "auth/logout", { method: "POST", headers });
  assert.ok(logout.ok);
  assert.ok(logout.headers.getSetCookie()[0].includes(`Path=${mount};`));
  assert.equal((await get(api + "config", { headers })).status, 401);
  console.log(`PASS ${binary}: ${origin}${mount} assets/login/cookie/API/SSE/logout`);
  if (process.env.JANUS_SMOKE_HOLD === "1") {
    console.log(`Browser check: ${origin}${mount} (admin / ${password}); Ctrl-C to stop`);
    await new Promise(resolve => { process.once("SIGINT", resolve); process.once("SIGTERM", resolve); });
  }
} finally {
  child.kill("SIGTERM");
  const force = setTimeout(() => child.kill("SIGKILL"), 5000);
  await exited;
  clearTimeout(force);
  rmSync(stage, { recursive: true, force: true });
}
