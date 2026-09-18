import { useEffect, useState } from "react";
import { ApiError, getDiscovery, getMetrics } from "./api";
import type { ConfigStore } from "./useConfig";
import type { MetricsSummary } from "./types";

const TITLES: Record<string, string> = {
  overview: "系统概览",
  routes: "路由",
  services: "服务",
  middlewares: "中间件",
  registries: "注册中心",
  limens: "入口",
  settings: "全局设置",
  json: "JSON",
};

export function pageTitle(page: string): string {
  return TITLES[page] || page;
}

export function Overview({ store }: { store: ConfigStore }) {
  const [metrics, setMetrics] = useState<MetricsSummary>({ requests: 0, errors: 0, in_flight: 0 });
  const [discovery, setDiscovery] = useState<Record<string, unknown>>({});
  const draft = store.draft;

  useEffect(() => {
    let alive = true;
    const sample = async () => {
      try {
        const [m, d] = await Promise.all([getMetrics(), getDiscovery().catch(() => null)]);
        if (!alive) return;
        setMetrics(m);
        if (d) setDiscovery(d.services || {});
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      }
    };
    sample().catch(() => undefined);
    const timer = window.setInterval(() => sample().catch(() => undefined), 5000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const services = Object.keys(draft?.services || {});
  const routes = draft?.routes || [];
  const unbound = routes.filter((r) => {
    const service = r.action?.forward?.service || r.service || "";
    if (r.action?.redirect || r.action?.respond) return false;
    return !service || !draft?.services?.[service];
  });

  return (
    <section className="page">
      <div className="hero">
        <div>
          <div className="eyebrow">JANUS RUNTIME</div>
          <h2>网关运行状态与配置健康</h2>
          <p>
            Revision {store.revision} · {routes.length} 条路由 · {services.length} 个服务
            {store.dirty ? " · 本地有未发布草稿" : " · 草稿已同步"}
          </p>
        </div>
      </div>
      <div className="stat-grid">
        <div className="stat-card">
          <span>已完成请求</span>
          <strong>{metrics.requests}</strong>
        </div>
        <div className="stat-card">
          <span>错误数</span>
          <strong>{metrics.errors}</strong>
        </div>
        <div className="stat-card">
          <span>在途请求</span>
          <strong>{metrics.in_flight}</strong>
        </div>
        <div className="stat-card">
          <span>Nacos 服务实例视图</span>
          <strong>{Object.keys(discovery).length}</strong>
        </div>
      </div>
      {unbound.length > 0 && (
        <div className="warn-card">
          <strong>有 {unbound.length} 条路由未绑定到有效 Service</strong>
          <p>{unbound.map((r) => r.name).join("、")}。发布前请先修复，否则请求会失败。</p>
        </div>
      )}
      <div className="card">
        <h3>待办（按顺序处理）</h3>
        <ol className="todo-list">
          <li>在「入口」确认监听地址与协议（修改后需重启生效）。</li>
          <li>在「服务」维护上游池或 Nacos 引用。</li>
          <li>在「中间件」创建可复用策略（buffer / body_limit / in_flight）。</li>
          <li>在「路由」把入口、匹配规则、服务、中间件串起来，发布前先校验。</li>
        </ol>
      </div>
    </section>
  );
}
