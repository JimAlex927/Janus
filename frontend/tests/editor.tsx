// Real editor, deterministic network delays. Development-only browser fixture.
import React from "react";
import { createRoot } from "react-dom/client";
import { ConfigEditorPage } from "../src/ConfigEditor";
import type { ConfigStore } from "../src/useConfig";
import "../src/app.css";

const content = { version: 1, limens: { private: { address: "127.0.0.1:8080", protocols: ["http1"] } }, routes: [{ name: "initial", limen: "private", match: "PathPrefix(`/`)", action: { respond: { status: 200, body: "initial" } } }], settings: {} };
let save: ((response: Response) => void) | undefined;
let publish: ((response: Response) => void) | undefined;
let version = 1;
const response = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } });
window.fetch = async (url, init) => {
  const path = String(url);
  document.getElementById("requests")!.textContent += `${init?.method || "GET"} ${path} ${JSON.stringify(init?.headers)} ${init?.body || ""}\n`;
  if (path.endsWith("/capabilities/middlewares")) return response({ middlewares: [] });
  if (path.endsWith("/publish")) return new Promise<Response>(resolve => { publish = resolve; });
  if (init?.method === "PUT") return new Promise<Response>(resolve => { save = resolve; });
  if (path.endsWith("/configs/1")) return response({ id: 1, name: "Concurrent editor", status: "draft", content, updated_at: "v1" });
  throw new Error(`unexpected request ${path}`);
};
document.getElementById("save-ok")!.onclick = () => { save?.(response({ ok: true, id: 1, status: "draft", updated_at: `v${++version}` })); save = undefined; };
document.getElementById("save-conflict")!.onclick = () => { save?.(response({ error: "conflict" }, 409)); save = undefined; };
document.getElementById("publish-ok")!.onclick = () => { publish?.(response({ ok: true, revision: 2, updated_at: `v${++version}` })); publish = undefined; };
const store = { revision: 1, load: async () => true, setMessage: (message: string) => { document.getElementById("message")!.textContent = message; }, setStatus: () => {} } as unknown as ConfigStore;
createRoot(document.getElementById("root")!).render(<ConfigEditorPage store={store} id={1} onBack={() => {}} onStatusChange={() => {}} />);
