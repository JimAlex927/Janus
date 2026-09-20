import { ApiError, type ConfigSnapshot, type JanusConfig, type MetricsSummary, type MiddlewareCapability, type NacosRegistry } from "./types";

// Janus supplies <base href=".../"> at runtime. The same bundle can therefore
// serve any mount without compiling API/EventSource URLs into the JavaScript.
const consoleBase = new URL(".", document.baseURI).pathname.replace(/\/$/, "");

export function apiPath(path: string): string {
  return `${consoleBase}${path}`;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(apiPath(path), {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  });
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const text = await response.text();
      if (text) {
        try {
          const body = JSON.parse(text) as { error?: string };
          if (body && typeof body.error === "string" && body.error) message = body.error;
          else message = text.slice(0, 500);
        } catch {
          message = text.slice(0, 500);
        }
      }
    } catch {
      /* 保持默认 message */
    }
    throw new ApiError(message, response.status);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export function login(username: string, password: string): Promise<unknown> {
  return request("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export function logout(): Promise<unknown> {
  return request("/api/v1/auth/logout", { method: "POST" });
}

export function getConfig(): Promise<ConfigSnapshot> {
  return request<ConfigSnapshot>("/api/v1/config");
}

export function validateConfig(config: JanusConfig): Promise<{ ok: boolean }> {
  return request("/api/v1/config/validate", {
    method: "POST",
    body: JSON.stringify(config),
  });
}

export function publishConfig(
  config: JanusConfig,
  revision: number,
): Promise<{ ok: boolean; revision: number }> {
  return request("/api/v1/config/publish", {
    method: "POST",
    headers: { "X-Janus-Revision": String(revision) },
    body: JSON.stringify(config),
  });
}

export function getMetrics(): Promise<MetricsSummary> {
  return request<MetricsSummary>("/api/v1/metrics");
}

export function getDiscovery(): Promise<{ revision: number; services: Record<string, unknown> }> {
  return request("/api/v1/discovery");
}

export function getMiddlewareCapabilities(): Promise<{ middlewares: MiddlewareCapability[] }> {
  return request("/api/v1/capabilities/middlewares");
}

export function testRegistry(
  name: string,
  registry: NacosRegistry,
): Promise<{ healthy: boolean; latency_ms?: number; error?: string }> {
  return request("/api/v1/discovery/registries/health", {
    method: "POST",
    body: JSON.stringify({ name, registry }),
  });
}

export interface ConfigRecord {
  id: number;
  name: string;
  status: "draft" | "active" | "archived" | string;
  created_at: string;
  updated_at: string;
}

export interface StoredConfig {
  id: number;
  name: string;
  status: string;
  content: JanusConfig;
  layout: { nodes?: Record<string, { x: number; y: number }> } | null;
  created_at: string;
  updated_at: string;
}

export interface ConfigPage {
  configs: ConfigRecord[];
  total: number;
  status?: "draft" | "active" | "archived";
  pageNum?: number;
  pageSize?: number;
  sortBy?: "createdAt" | "updatedAt";
  sortOrder?: "asc" | "desc";
}

export function listConfigs(options?: {
  status?: "draft" | "active" | "archived";
  pageNum?: number;
  pageSize?: number;
  sortBy?: "createdAt" | "updatedAt";
  sortOrder?: "asc" | "desc";
  createdAtFrom?: string;
  createdAtTo?: string;
  updatedAtFrom?: string;
  updatedAtTo?: string;
}): Promise<ConfigPage> {
  const query = new URLSearchParams();
  if (options?.status) query.set("status", options.status);
  if (options?.pageNum != null) query.set("pageNum", String(options.pageNum));
  if (options?.pageSize != null) query.set("pageSize", String(options.pageSize));
  if (options?.sortBy) query.set("sortBy", options.sortBy);
  if (options?.sortOrder) query.set("sortOrder", options.sortOrder);
  for (const key of ["createdAtFrom", "createdAtTo", "updatedAtFrom", "updatedAtTo"] as const) {
    if (options?.[key]) query.set(key, options[key]!);
  }
  const suffix = query.size > 0 ? `?${query}` : "";
  return request(`/api/v1/configs${suffix}`);
}

export function createConfig(name: string, from?: string, content?: JanusConfig): Promise<{ id: number }> {
  return request("/api/v1/configs", {
    method: "POST",
    body: JSON.stringify({ name, from: from || "blank", content }),
  });
}

export function getStoredConfig(id: number): Promise<StoredConfig> {
  return request(`/api/v1/configs/${id}`);
}

export function saveStoredConfig(
  id: number,
  payload: { name?: string; content?: JanusConfig; layout?: Record<string, unknown> },
  recordRevision?: string,
): Promise<{ ok: boolean; id: number; status?: string; forked_from?: number; updated_at: string }> {
  return request(`/api/v1/configs/${id}`, {
    method: "PUT",
    headers: recordRevision ? { "X-Janus-Record-Revision": recordRevision } : undefined,
    body: JSON.stringify(payload),
  });
}

export function deleteStoredConfig(id: number): Promise<{ ok: boolean }> {
  return request(`/api/v1/configs/${id}`, { method: "DELETE" });
}

export function publishStoredConfig(id: number, revision: number, recordRevision?: string): Promise<{ ok: boolean; revision: number; warning?: string; updated_at?: string }> {
  return request(`/api/v1/configs/${id}/publish`, {
    method: "POST",
    headers: { "X-Janus-Revision": String(revision), ...(recordRevision ? { "X-Janus-Record-Revision": recordRevision } : {}) },
  });
}

export function stageLimens(id: number): Promise<{ ok: boolean; restart_required: boolean; warning?: string }> {
  return request(`/api/v1/configs/${id}/stage-limens`, { method: "POST" });
}

export function saveSettings(settings: unknown): Promise<{ ok: boolean; restart_required: boolean }> {
  return request("/api/v1/config/settings", {
    method: "POST",
    body: JSON.stringify(settings),
  });
}

/** 订阅远端 generation 变更；返回取消函数。401 时交由调用方处理。 */
export function subscribeEvents(onGenerationChanged: () => void): () => void {
  const source = new EventSource(apiPath("/api/v1/events"));
  source.addEventListener("generation_changed", () => onGenerationChanged());
  source.onerror = () => undefined;
  return () => source.close();
}

export { ApiError };
