// Development-only browser regression harness. This is not part of index.html
// or the production bundle. Exercise the real hook with controllable responses.
import React from "react";
import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { useConfig, type ConfigStore } from "../src/useConfig";

type Pending = { url: string; init?: RequestInit; answer: (data: unknown, status?: number) => void };
let pending: Pending[] = [];
let current: ConfigStore;
const log: string[] = [];
const tick = () => new Promise(resolve => setTimeout(resolve, 0));
function assert(value: unknown, message: string) { if (!value) throw new Error(message); }
const config = (name: string) => ({ version: 1, limens: { private: { address: "127.0.0.1:8080", protocols: ["http1"] } }, routes: [{ name, limen: "private", match: "PathPrefix(`/`)", action: { respond: { status: 200, body: name } } }], settings: {} });
let notify: () => void = () => {};
class Events {
  onerror: unknown;
  addEventListener(_name: string, callback: () => void) { notify = callback; }
  close() {}
}
window.EventSource = Events as unknown as typeof EventSource;
window.fetch = async (url, init) => new Promise<Response>(resolve => {
  pending.push({ url: String(url), init, answer: (data, status = 200) => resolve(new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } })) });
});
function Hook() { current = useConfig(); return <span>{current.status}/{current.revision}/{current.draft?.routes?.[0]?.name}/{String(current.dirty)}</span>; }
async function take(suffix: string) {
  for (let i = 0; i < 100; i++) { const index = pending.findIndex(r => r.url.endsWith(suffix)); if (index >= 0) return pending.splice(index, 1)[0]; await tick(); }
  throw new Error(`missing request ${suffix}`);
}
async function settle() { await tick(); await new Promise(requestAnimationFrame); await new Promise(requestAnimationFrame); }
document.getElementById("run")!.onclick = async () => {
  const result = document.getElementById("results")!;
  (document.getElementById("run") as HTMLButtonElement).disabled = true;
  const root = createRoot(document.getElementById("hook")!);
  try {
    root.render(<Hook />);
    (await take("/config")).answer({ revision: 1, config: config("initial") }); await settle();
    assert(current.status === "ready", `initial load failed: ${current.status} ${current.message}`);

    // A clean request starts, then the user edits while it is in flight.
    const load = current.load(); const delayed = await take("/config");
    flushSync(() => current.replace(config("local-during-load")));
    delayed.answer({ revision: 2, config: config("remote") }); await load; await settle();
    assert(current.draft?.routes?.[0].name === "local-during-load" && current.dirty && current.revision === 1 && current.remoteChanged, "late load overwrote edits or advanced draft baseline");
    log.push("PASS late load preserves new edits and revision baseline");

    const before = pending.length; notify(); await settle();
    assert(pending.length === before && current.dirty, "remote event fetched over dirty draft");
    log.push("PASS remote event does not overwrite dirty draft");

    const first = current.load(true); const one = await take("/config");
    const second = current.load(true); const two = await take("/config");
    two.answer({ revision: 3, config: config("newest") }); await second; await settle();
    assert(current.busy, "older pending operation cleared busy");
    one.answer({ revision: 2, config: config("stale") }); await first; await settle();
    assert(current.draft?.routes?.[0].name === "newest" && !current.busy, "out-of-order load won");
    log.push("PASS out-of-order responses and overlapping busy state");

    flushSync(() => current.replace(config("submitted")));
    const publication = current.publish(); const publishRequest = await take("/config/publish");
    assert(JSON.parse(String(publishRequest.init?.body)).routes[0].name === "submitted", "publication not a stable snapshot");
    flushSync(() => current.replace(config("edited-during-publish")));
    notify(); await settle();
    publishRequest.answer({ revision: 4 }); assert(await publication, "publish failed"); await settle();
    assert(current.draft?.routes?.[0].name === "edited-during-publish" && current.dirty && current.revision === 4 && pending.length === 0, "publish erased subsequent edits");
    log.push("PASS publication keeps later edits and does not force reload");

    const conflict = current.publish(); (await take("/config/publish")).answer({ error: "conflict" }, 409);
    assert(!(await conflict), "conflict accepted"); await settle();
    assert(current.draft?.routes?.[0].name === "edited-during-publish" && current.dirty && current.remoteChanged && current.revision === 4, "409 erased local draft");
    log.push("PASS conflict preserves local draft");

    const discard = current.discard(); const discarding = await take("/config");
    flushSync(() => current.replace(config("edited-after-discard")));
    discarding.answer({ revision: 5, config: config("remote-five") }); await discard; await settle();
    assert(current.draft?.routes?.[0].name === "edited-after-discard" && current.dirty && !current.message.includes("已丢弃"), "discard erased edits created after click");
    log.push("PASS explicit discard cannot erase edits made after it began");
    result.textContent = log.join("\n") + "\nALL PASS";
    document.title = "PASS Janus B4 concurrency";
  } catch (error) { result.textContent = log.join("\n") + "\nFAIL " + String(error); document.title = "FAIL Janus B4 concurrency"; }
  finally { root.unmount(); }
};
