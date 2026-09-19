import { ApiError, type ConfigSnapshot, type JanusConfig, type MetricsSummary, type MiddlewareCapability, type NacosRegistry } from "./types";

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
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

export function listConfigs(): Promise<{ configs: ConfigRecord[] }> {
  return request("/api/v1/configs");
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
): Promise<{ ok: boolean; id: number; status?: string; forked_from?: number }> {
  return request(`/api/v1/configs/${id}`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export function deleteStoredConfig(id: number): Promise<{ ok: boolean }> {
  return request(`/api/v1/configs/${id}`, { method: "DELETE" });
}

export function publishStoredConfig(id: number, revision: number): Promise<{ ok: boolean; revision: number; warning?: string }> {
  return request(`/api/v1/configs/${id}/publish`, {
    method: "POST",
    headers: { "X-Janus-Revision": String(revision) },
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
  const source = new EventSource("/api/v1/events");
  source.addEventListener("generation_changed", () => onGenerationChanged());
  source.onerror = () => undefined;
  return () => source.close();
}

export { ApiError };
