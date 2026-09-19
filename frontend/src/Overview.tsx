import { useEffect, useState } from "react";
import { ApiError, getDiscovery, getMetrics, listConfigs, type ConfigRecord } from "./api";
import type { ConfigStore } from "./useConfig";
import type { MetricsSummary } from "./types";

export function Overview({ store }: { store: ConfigStore }) {
  const [metrics, setMetrics] = useState<MetricsSummary>({ requests: 0, errors: 0, failed_requests: 0, client_errors: 0, server_errors: 0, not_found: 0, in_flight: 0 });
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
  const serviceCount = Object.keys(draft?.services || {}).length;
  const discoveryCount = Object.keys(discovery).length;
  const failedRequests = metrics.failed_requests ?? metrics.errors;
  const clientErrors = metrics.client_errors ?? 0;
  const serverErrors = metrics.server_errors ?? 0;
  const notFound = metrics.not_found ?? 0;
  const hasClientErrors = clientErrors > 0;
  const hasServerErrors = serverErrors > 0;

  return (
    <section className="page overview-page">
      <div className="overview-runtime">
        <div className="overview-runtime-copy">
          <div className="overview-kicker"><span /> RUNTIME STATUS</div>
          <h2>网关正在处理流量</h2>
          <p>当前配置已加载。状态与关键计数每 10 秒刷新一次。</p>
        </div>
        <div className="overview-runtime-meta">
          <div>
            <span>ACTIVE CONFIG</span>
            <strong>{active ? active.name : "库外配置"}</strong>
          </div>
          <div>
            <span>REVISION</span>
            <strong className="overview-mono">{store.revision}</strong>
          </div>
        </div>
      </div>

      <div className="overview-metrics" aria-label="流量指标">
        <div className="overview-metric">
          <span>REQUESTS</span>
          <strong>{metrics.requests.toLocaleString()}</strong>
          <small>已完成请求</small>
        </div>
        <div className="overview-metric">
          <span>FAILED REQUESTS</span>
          <strong className={failedRequests > 0 ? "overview-metric-warning" : ""}>{failedRequests.toLocaleString()}</strong>
          <small>{failedRequests > 0 ? "4xx + 5xx" : "当前无失败"}</small>
        </div>
        <div className="overview-metric">
          <span>4XX CLIENT EXCL. 404</span>
          <strong className={hasClientErrors ? "overview-metric-client" : ""}>{clientErrors.toLocaleString()}</strong>
          <small>{hasClientErrors ? "除未找到外的客户端错误" : "当前无其他 4xx"}</small>
        </div>
        <div className="overview-metric">
          <span>5XX SERVER</span>
          <strong className={hasServerErrors ? "overview-metric-server" : ""}>{serverErrors.toLocaleString()}</strong>
          <small>{hasServerErrors ? "需要排查网关或上游" : "当前无 5xx"}</small>
        </div>
        <div className="overview-metric">
          <span>404 NOT FOUND</span>
          <strong className={notFound > 0 ? "overview-metric-client" : ""}>{notFound.toLocaleString()}</strong>
          <small>{notFound > 0 ? "未匹配路由或资源" : "当前无 404"}</small>
        </div>
        <div className="overview-metric">
          <span>IN FLIGHT</span>
          <strong>{metrics.in_flight.toLocaleString()}</strong>
          <small>正在处理</small>
        </div>
      </div>

      {unbound.length > 0 && (
        <div className="overview-alert" role="alert">
          <span className="overview-alert-mark">!</span>
          <div>
            <strong>{unbound.length} 条路由未绑定有效 Service</strong>
            <p>{unbound.map((r) => r.name).join("、")}。请在 Config 画布修复后重新发布。</p>
          </div>
        </div>
      )}

      <div className="overview-detail-grid">
        <section className="overview-detail">
          <div className="overview-section-head">
            <div>
              <span className="overview-section-label">CONFIGURATION</span>
              <h3>配置库</h3>
            </div>
            <span className="overview-count">{configs.length}</span>
          </div>
          <dl className="overview-definition-list">
            <div>
              <dt>当前生效</dt>
              <dd>{active ? active.name : "库外配置"}</dd>
            </div>
            <div>
              <dt>配置总数</dt>
              <dd>{configs.length} 套</dd>
            </div>
            <div>
              <dt>路由 / 服务</dt>
              <dd>{routes.length} / {serviceCount}</dd>
            </div>
            <div>
              <dt>配置变更</dt>
              <dd>通过校验后发布</dd>
            </div>
          </dl>
        </section>
        <section className="overview-detail">
          <div className="overview-section-head">
            <div>
              <span className="overview-section-label">DISCOVERY</span>
              <h3>服务发现</h3>
            </div>
            <span className="overview-count">{discoveryCount}</span>
          </div>
          <dl className="overview-definition-list">
            <div>
              <dt>Nacos 服务视图</dt>
              <dd>{discoveryCount} 个</dd>
            </div>
            <div>
              <dt>本地服务定义</dt>
              <dd>{serviceCount} 个</dd>
            </div>
            <div>
              <dt>健康状态</dt>
              <dd><span className="overview-health-dot" />运行中</dd>
            </div>
          </dl>
        </section>
      </div>

      <section className="overview-workflow" aria-label="配置工作流">
        <div className="overview-workflow-head">
          <span className="overview-section-label">WORKFLOW</span>
          <h3>配置操作</h3>
        </div>
        <ol>
          <li><span>01</span><p><strong>编辑路由</strong>在 Config 画布中维护入口、匹配规则与服务目标。</p></li>
          <li><span>02</span><p><strong>校验发布</strong>发布前检查配置，确保同一时刻仅一套配置生效。</p></li>
          <li><span>03</span><p><strong>应用全局设置</strong>进程级修改保存后，重启 Janus 使其生效。</p></li>
        </ol>
      </section>
    </section>
  );
}
