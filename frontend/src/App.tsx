import { useState, type FormEvent } from "react";
import { login, logout } from "./api";
import { JsonPage } from "./JsonPage";
import { LimensPage } from "./Limens";
import { MiddlewaresPage } from "./Middlewares";
import { Overview, pageTitle } from "./Overview";
import { RegistriesPage } from "./Registries";
import { RoutesPage } from "./Routes";
import { ServicesPage } from "./Services";
import { SettingsPage } from "./Settings";
import { useConfig } from "./useConfig";

type Page = "overview" | "routes" | "services" | "middlewares" | "registries" | "limens" | "settings" | "json";

const NAV: { id: Page; label: string }[] = [
  { id: "overview", label: "概览" },
  { id: "routes", label: "路由" },
  { id: "services", label: "服务" },
  { id: "middlewares", label: "中间件" },
  { id: "registries", label: "注册中心" },
  { id: "limens", label: "入口" },
  { id: "settings", label: "全局设置" },
  { id: "json", label: "JSON" },
];

export function App() {
  const store = useConfig();
  const [page, setPage] = useState<Page>("overview");

  if (store.status === "loading" || store.status === "error") {
    return (
      <div className="login-shell">
        <div className="login-card">
          <div className="brand-mark">J</div>
          <h2>正在连接 Janus…</h2>
          <p>{store.status === "error" ? store.message || "加载失败" : "正在读取配置快照"}</p>
          {store.status === "error" && (
            <button type="button" className="btn primary" onClick={() => store.load(true)}>
              重试
            </button>
          )}
        </div>
      </div>
    );
  }

  if (store.status === "unauthorized") {
    return <Login onDone={() => store.load(true)} />;
  }

  async function refresh() {
    if (store.dirty && !window.confirm("本地有未发布草稿，刷新会丢弃草稿。继续吗？")) return;
    await store.load(true);
  }

  return (
    <div className="app-shell">
      <aside className="side">
        <div className="brand">
          <div className="brand-mark">J</div>
          <div>
            <strong>Janus</strong>
            <small>GATEWAY CONSOLE</small>
          </div>
        </div>
        <nav>
          {NAV.map((item) => (
            <button key={item.id} type="button" className={page === item.id ? "active" : ""} onClick={() => setPage(item.id)}>
              <span className="nav-dot" />
              {item.label}
            </button>
          ))}
        </nav>
        <div className="side-foot">
          <span className="status-dot" />
          运行中
          <br />
          <small>Revision {store.revision}</small>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <div>
            <div className="eyebrow">CONTROL PLANE</div>
            <div className="title-line">
              <h1>{pageTitle(page)}</h1>
              <span className={`draft-state ${store.dirty ? "dirty" : ""}`}>{store.dirty ? "未发布变更" : "已同步"}</span>
            </div>
          </div>
          <div className="header-actions">
            <button type="button" className="btn ghost" disabled={store.busy} onClick={refresh}>
              刷新
            </button>
            <button type="button" className="btn ghost" disabled={store.busy} onClick={() => store.validate()}>
              校验
            </button>
            <button
              type="button"
              className="btn ghost"
              disabled={store.busy}
              onClick={async () => {
                await logout().catch(() => undefined);
                store.setStatus("unauthorized");
              }}
            >
              退出
            </button>
            <button type="button" className="btn primary" disabled={store.busy || !store.dirty} onClick={() => store.publish()}>
              发布变更
            </button>
          </div>
        </header>
        {store.remoteChanged && (
          <div className="notice">
            远端配置发生变化。
            <button type="button" onClick={() => store.discard()}>
              丢弃草稿并刷新
            </button>
          </div>
        )}
        {store.message && (
          <div className="toast">
            {store.message}
            <button type="button" onClick={() => store.setMessage("")} aria-label="关闭提示">
              ×
            </button>
          </div>
        )}
        {page === "overview" && <Overview store={store} />}
        {page === "routes" && <RoutesPage store={store} />}
        {page === "services" && <ServicesPage store={store} />}
        {page === "middlewares" && <MiddlewaresPage store={store} />}
        {page === "registries" && <RegistriesPage store={store} />}
        {page === "limens" && <LimensPage store={store} />}
        {page === "settings" && <SettingsPage store={store} />}
        {page === "json" && <JsonPage store={store} />}
      </main>
    </div>
  );
}

function Login({ onDone }: { onDone: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(username, password);
      onDone();
    } catch {
      setError("账号或密码错误。");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="login-shell">
      <form className="login-card" onSubmit={submit}>
        <div className="brand-mark">J</div>
        <div className="eyebrow">JANUS CONSOLE</div>
        <h2>欢迎回来</h2>
        <p>登录后管理网关配置。</p>
        <input autoFocus placeholder="管理员账号" value={username} onChange={(e) => setUsername(e.target.value)} />
        <input type="password" placeholder="密码" value={password} onChange={(e) => setPassword(e.target.value)} />
        <button type="submit" className="btn primary" disabled={busy}>
          {busy ? "登录中…" : "登录"}
        </button>
        {error && <small className="error-text">{error}</small>}
      </form>
    </div>
  );
}
