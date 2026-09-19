import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, getDiscovery, getMetrics, listConfigs, type ConfigRecord } from "./api";
import type { ConfigStore } from "./useConfig";
import type { MetricsSummary } from "./types";

type Sample = { at: number; requests: number; failed: number; qps: number; errorRate: number };

const emptyMetrics: MetricsSummary = { requests: 0, errors: 0, failed_requests: 0, client_errors: 0, server_errors: 0, not_found: 0, in_flight: 0 };

export function Overview({ store }: { store: ConfigStore }) {
  const [metrics, setMetrics] = useState<MetricsSummary>(emptyMetrics);
  const [discovery, setDiscovery] = useState<Record<string, unknown>>({});
  const [configs, setConfigs] = useState<ConfigRecord[]>([]);
  const [samples, setSamples] = useState<Sample[]>([]);
  const lastSample = useRef<{ at: number; requests: number } | null>(null);
  const draft = store.draft;

  useEffect(() => {
    let alive = true;
    const sample = async () => {
      try {
        const [m, d, c] = await Promise.all([getMetrics(), getDiscovery().catch(() => null), listConfigs({ status: "active", limit: 1 }).catch(() => null)]);
        if (!alive) return;
        const failed = m.failed_requests ?? m.errors;
        const now = Date.now();
        const previous = lastSample.current;
        const elapsed = previous ? Math.max((now - previous.at) / 1000, 1) : 0;
        const qps = previous ? Math.max(0, m.requests - previous.requests) / elapsed : 0;
        const errorRate = m.requests > 0 ? (failed / m.requests) * 100 : 0;
        lastSample.current = { at: now, requests: m.requests };
        setMetrics(m);
        setSamples((current) => [...current, { at: now, requests: m.requests, failed, qps, errorRate }].slice(-36));
        if (d) setDiscovery(d.services || {});
        if (c) setConfigs(c.configs || []);
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) store.setStatus("unauthorized");
      }
    };
    sample().catch(() => undefined);
    const timer = window.setInterval(() => sample().catch(() => undefined), 10000);
    return () => { alive = false; window.clearInterval(timer); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const active = configs.find((c) => c.status === "active");
  const routes = draft?.routes || [];
  const unbound = routes.filter((r) => {
    const service = r.action?.forward?.service || r.service || "";
    if (r.action?.redirect || r.action?.respond || r.action?.static) return false;
    return !service || !draft?.services?.[service];
  });
  const serviceCount = Object.keys(draft?.services || {}).length;
  const discoveryCount = Object.keys(discovery).length;
  const failedRequests = metrics.failed_requests ?? metrics.errors;
  const clientErrors = metrics.client_errors ?? 0;
  const serverErrors = metrics.server_errors ?? 0;
  const notFound = metrics.not_found ?? 0;
  const errorRate = metrics.requests > 0 ? (failedRequests / metrics.requests) * 100 : 0;
  const latest = samples[samples.length - 1];
  const qps = latest?.qps ?? 0;
  const healthy = serverErrors === 0 && unbound.length === 0;
  const services = useMemo(() => Object.entries(discovery), [discovery]);

  return (
    <section className="page overview-page">
      <div className="overview-dashboard-head">
        <div><div className="overview-kicker"><span /> OPERATIONS OVERVIEW</div><h2>网关运行概况</h2><p>实时采样当前进程指标，帮助你快速判断流量、错误与服务发现是否健康。</p></div>
        <div className="overview-head-meta"><span className={`overview-live-dot ${healthy ? "" : "warn"}`} /><div><strong>{healthy ? "运行正常" : "需要关注"}</strong><small>每 10 秒刷新 · 会话趋势</small></div><span className="overview-revision">REV {store.revision}</span></div>
      </div>

      <div className="overview-health-grid">
        <section className="overview-health-card"><div className="overview-card-head"><div><span className="overview-section-label">RUNTIME HEALTH</span><h3>运行健康度</h3></div><span className="overview-info">i</span></div><div className="health-summary"><div className="health-ring" style={{ "--health": `${healthy ? 100 : Math.max(18, 100 - errorRate * 4)}%` } as React.CSSProperties}><div><strong>{healthy ? "100" : Math.max(0, 100 - Math.round(errorRate * 4))}<small>%</small></strong><span>{healthy ? "稳定" : "关注"}</span></div></div><div className="health-copy"><strong>{active ? active.name : "库外配置"}</strong><span>当前生效配置</span><div className="health-line"><i className={healthy ? "ok" : "warn"} />{healthy ? "没有检测到服务级异常" : "存在未绑定路由或服务错误"}</div></div></div><div className="health-foot"><span>服务发现 <b>{discoveryCount}</b></span><span>路由 <b>{routes.length}</b></span><span>运行中 <b>{metrics.in_flight}</b></span></div></section>
        <KpiCard label="请求总量" value={metrics.requests.toLocaleString()} hint="累计完成请求" accent="blue" trend={qps > 0 ? `${qps.toFixed(2)} QPS` : "等待采样"} />
        <KpiCard label="实时吞吐" value={qps.toFixed(2)} unit="QPS" hint="最近采样窗口" accent="teal" trend={samples.length > 1 ? "SESSION TREND" : "正在建立趋势"} />
        <KpiCard label="错误率" value={`${errorRate.toFixed(2)}%`} hint={`${failedRequests.toLocaleString()} 个失败请求`} accent={errorRate > 0 ? "red" : "green"} trend={serverErrors > 0 ? `${serverErrors} 个 5xx` : "服务端 0 错误"} />
      </div>

      {unbound.length > 0 && <div className="overview-alert" role="alert"><span className="overview-alert-mark">!</span><div><strong>{unbound.length} 条路由未绑定有效 Service</strong><p>{unbound.map((r) => r.name).join("、")}。请在 Config 画布修复后重新发布。</p></div></div>}

      <div className="overview-chart-grid">
        <section className="overview-panel overview-traffic-panel"><PanelHead label="TRAFFIC TREND" title="请求吞吐趋势" detail="QPS · 当前会话" /><div className="chart-summary"><strong>{qps.toFixed(2)}</strong><span>QPS</span><small>{samples.length < 2 ? "首个采样完成后显示曲线" : `最近 ${samples.length} 个采样点`}</small></div><Sparkline values={samples.map((sample) => sample.qps)} color="#3d80ed" fill="#3d80ed1a" empty="等待更多采样数据" /><div className="chart-axis"><span>较早</span><span>现在</span></div></section>
        <section className="overview-panel"><PanelHead label="ERROR TREND" title="错误趋势" detail="失败请求" tone="red" /><div className="chart-summary"><strong className={errorRate > 0 ? "red-value" : ""}>{errorRate.toFixed(2)}%</strong><span>ERROR RATE</span><small>{clientErrors} 个 4xx · {serverErrors} 个 5xx</small></div><Sparkline values={samples.map((sample) => sample.errorRate)} color="#ef6b68" fill="#ef6b681a" empty="当前会话暂无错误" /><div className="chart-axis"><span>较早</span><span>现在</span></div></section>
      </div>

      <div className="overview-lower-grid">
        <section className="overview-panel distribution-panel"><PanelHead label="ERROR BREAKDOWN" title="错误分类" detail="累计计数" tone="red" /><Breakdown label="客户端错误" value={clientErrors} total={failedRequests} color="yellow" /><Breakdown label="服务端错误" value={serverErrors} total={failedRequests} color="red" /><Breakdown label="路由未找到" value={notFound} total={failedRequests} color="purple" />{failedRequests === 0 && <div className="panel-empty"><span>✓</span><strong>当前没有错误</strong><small>请求质量保持稳定</small></div>}</section>
        <section className="overview-panel"><PanelHead label="DISCOVERY STATUS" title="服务发现" detail={`${discoveryCount} 个服务`} />{services.length === 0 ? <div className="panel-empty"><span>—</span><strong>暂无服务发现数据</strong><small>配置 Nacos 服务后会在这里显示</small></div> : <div className="service-list">{services.map(([name, value]) => <ServiceStatus key={name} name={name} value={value} />)}</div>}</section>
        <section className="overview-panel duration-panel"><PanelHead label="LATENCY" title="请求时长分布" detail="数据采集即将接入" tone="purple" /><div className="latency-placeholder"><div className="placeholder-bars"><i /><i /><i /><i /><i /></div><strong>等待时长样本</strong><span>当前 API 仅提供累计请求计数</span></div></section>
      </div>

      <div className="overview-detail-grid"><section className="overview-detail"><div className="overview-section-head"><div><span className="overview-section-label">CONFIGURATION</span><h3>配置库</h3></div><span className="overview-count">{configs.length}</span></div><dl className="overview-definition-list"><div><dt>当前生效</dt><dd>{active ? active.name : "库外配置"}</dd></div><div><dt>配置总数</dt><dd>{configs.length} 套</dd></div><div><dt>路由 / 服务</dt><dd>{routes.length} / {serviceCount}</dd></div><div><dt>配置变更</dt><dd>通过校验后发布</dd></div></dl></section><section className="overview-detail"><div className="overview-section-head"><div><span className="overview-section-label">WORKFLOW</span><h3>配置操作</h3></div><span className="overview-count">03</span></div><ol className="overview-workflow-list"><li><span>01</span><p><strong>编辑路由</strong>维护入口、匹配规则与服务目标。</p></li><li><span>02</span><p><strong>校验发布</strong>确保同一时刻仅一套配置生效。</p></li><li><span>03</span><p><strong>应用设置</strong>进程级修改保存后重启生效。</p></li></ol></section></div>
    </section>
  );
}

function KpiCard({ label, value, unit, hint, accent, trend }: { label: string; value: string; unit?: string; hint: string; accent: string; trend: string }) { return <section className={`overview-kpi overview-kpi-${accent}`}><div className="overview-kpi-label"><span>{label}</span><i>↗</i></div><div className="overview-kpi-value">{value}<small>{unit}</small></div><div className="overview-kpi-foot"><span>{hint}</span><b>{trend}</b></div></section>; }

function PanelHead({ label, title, detail, tone = "blue" }: { label: string; title: string; detail: string; tone?: string }) { return <div className="overview-panel-head"><div><span className={`panel-icon panel-icon-${tone}`} /><span className="overview-section-label">{label}</span><h3>{title}</h3></div><span className="panel-detail">{detail}</span></div>; }

function Sparkline({ values, color, fill, empty }: { values: number[]; color: string; fill: string; empty: string }) {
  const width = 620; const height = 170; const plotted = values.length > 1 ? values : [];
  if (plotted.length === 0) return <div className="sparkline-empty">{empty}</div>;
  const max = Math.max(...plotted, 1);
  const points = plotted.map((value, index) => `${(index / (plotted.length - 1)) * width},${height - (value / max) * (height - 20) - 8}`).join(" ");
  const area = `0,${height} ${points} ${width},${height}`;
  const id = `chart-fill-${color.replace(/[^a-z0-9]/gi, "")}`;
  return <svg className="sparkline" viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" role="img" aria-label="趋势图"><defs><linearGradient id={id} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor={fill} /><stop offset="1" stopColor={fill} stopOpacity="0" /></linearGradient></defs><line x1="0" y1={height - 8} x2={width} y2={height - 8} stroke="#dfe8e4" strokeDasharray="3 5" /><polygon points={area} fill={`url(#${id})`} /><polyline points={points} fill="none" stroke={color} strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}

function Breakdown({ label, value, total, color }: { label: string; value: number; total: number; color: string }) { const percentage = total > 0 ? Math.min(100, (value / total) * 100) : 0; return <div className="breakdown-row"><div><span className={`breakdown-dot breakdown-${color}`} />{label}<strong>{value.toLocaleString()}</strong></div><div className="breakdown-track"><i className={`breakdown-fill breakdown-${color}`} style={{ width: `${percentage}%` }} /></div></div>; }

function ServiceStatus({ name, value }: { name: string; value: unknown }) { const status = value && typeof value === "object" ? value as { instances?: number; failed?: boolean; expired?: boolean } : {}; const failed = Boolean(status.failed || status.expired); return <div className="service-status"><div className={`service-status-dot ${failed ? "failed" : ""}`} /><div><strong>{name}</strong><span>{failed ? "连接异常" : `${status.instances ?? 0} 个实例在线`}</span></div><b className={failed ? "failed" : ""}>{failed ? "异常" : "正常"}</b></div>; }
