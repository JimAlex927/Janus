import { useEffect, useState } from "react";
import { ApiError, getDiscovery, getMetrics, listConfigs, type ConfigRecord } from "./api";
import { StatCard } from "./ui";
import type { ConfigStore } from "./useConfig";
import type { MetricsSummary } from "./types";

export function Overview({ store }: { store: ConfigStore }) {
  const [metrics, setMetrics] = useState<MetricsSummary>({ requests: 0, errors: 0, in_flight: 0 });
  const [discovery, setDiscovery] = useState<Record<string, unknown>>({});
  const [configs, setConfigs] = useState<ConfigRecord[]>([]);
  const draft = store.draft;

  useEffect(() => {
    let alive = true;
    const sample = async () => {
      try {
        const [m, d, c] = await Promise.all([
          getMetrics(),
          getDiscovery().catch(() => null),
          listConfigs().catch(() => null),
        ]);
        if (!alive) return;
        setMetrics(m);
        if (d) setDiscovery(d.services || {});
        if (c) setConfigs(c.configs || []);
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      }
    };
    sample().catch(() => undefined);
    const timer = window.setInterval(() => sample().catch(() => undefined), 10000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const active = configs.find((c) => c.status === "active");
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
            生效配置：{active ? active.name : "（库外配置）"} · Revision {store.revision} · {routes.length} 条路由 ·
            {Object.keys(draft?.services || {}).length} 个服务
          </p>
        </div>
      </div>
      <div className="stat-grid">
        <StatCard label="已完成请求" value={metrics.requests} />
        <StatCard label="错误数" value={metrics.errors} />
        <StatCard label="在途请求" value={metrics.in_flight} />
        <StatCard label="配置库" value={configs.length} sub={active ? `生效中：${active.name}` : undefined} />
        <StatCard label="Nacos 服务实例视图" value={Object.keys(discovery).length} />
      </div>
      {unbound.length > 0 && (
        <div className="warn-card">
          <strong>有 {unbound.length} 条路由未绑定到有效 Service</strong>
          <p>{unbound.map((r) => r.name).join("、")}。请到 Config 画布中修复后重新发布。</p>
        </div>
      )}
      <div className="card">
        <h3>使用顺序</h3>
        <ol className="todo-list">
          <li>在「Config」创建或打开一套配置，在画布上拖入节点、连线表达路由规则，保存草稿后发布。</li>
          <li>同一时间只有一套配置生效；发布前可用校验拦截明显错误。</li>
          <li>「全局设置」改的是进程级参数，保存后必须重启 Janus 才生效。</li>
        </ol>
      </div>
    </section>
  );
}
