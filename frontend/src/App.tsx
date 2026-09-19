import { useState, type FormEvent } from "react";
import { login, logout } from "./api";
import { ConfigEditorPage } from "./ConfigEditor";
import { ConfigsPage } from "./Configs";
import { Overview } from "./Overview";
import { PelicanRide } from "./PelicanRide";
import { SettingsPage } from "./Settings";
import { Toast } from "./ui";
import { useConfig } from "./useConfig";

type Page = "overview" | "configs" | "settings";

const NAV: { id: Page; label: string }[] = [
  { id: "overview", label: "概览" },
  { id: "configs", label: "Config" },
  { id: "settings", label: "全局设置" },
];

const TITLES: Record<Page, string> = { overview: "系统概览", configs: "配置", settings: "全局设置" };

function JanusMark({ className = "" }: { className?: string }) {
  return (
    <svg className={`brand-mark janus-mark ${className}`.trim()} viewBox="0 0 40 40" role="img" aria-label="Janus">
      <path d="M12 11v12a8 8 0 0 0 16 0V11" fill="none" stroke="currentColor" strokeWidth="3.1" strokeLinecap="round" />
      <path d="M28 29V17a8 8 0 0 0-16 0" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" opacity=".42" />
      <path d="M12 11h5M23 11h5" stroke="currentColor" strokeWidth="3.1" strokeLinecap="round" />
      <circle cx="20" cy="31" r="1.7" fill="currentColor" />
    </svg>
  );
}

export function App() {
  const store = useConfig();
  const [page, setPage] = useState<Page>("overview");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [configsTick, setConfigsTick] = useState(0);

  function navigate(next: Page) {
    setPage(next);
    if (next !== "configs") setEditingId(null);
  }

  if (store.status === "loading" || store.status === "error") {
    return (
      <div className="login-shell">
        <div className="login-card">
          <JanusMark />
          <h2>正在连接 Janus…</h2>
          <p>{store.status === "error" ? store.message || "加载失败" : "正在读取运行状态"}</p>
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

  return (
    <div className="app-shell">
      <aside className="side">
        <div className="brand">
          <JanusMark />
          <div>
            <strong>Janus</strong>
            <small>GATEWAY CONSOLE</small>
          </div>
        </div>
        <nav><Navigation page={page} onNavigate={navigate} /></nav>
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
              <h1>{editingId != null ? "配置画布" : TITLES[page]}</h1>
            </div>
          </div>
          <div className="header-actions">
            <button
              type="button"
              className="btn ghost"
              onClick={async () => {
                await logout().catch(() => undefined);
                store.setStatus("unauthorized");
              }}
            >
              退出
            </button>
          </div>
        </header>
        <nav className="mobile-nav" aria-label="主要导航">
          <Navigation page={page} onNavigate={navigate} />
        </nav>
        {store.message && (
          <Toast message={store.message} onClose={() => store.setMessage("")} />
        )}
        {page === "overview" && <Overview store={store} />}
        {page === "configs" &&
          (editingId != null ? (
            <ConfigEditorPage
              store={store}
              id={editingId}
              onBack={() => {
                setEditingId(null);
                setConfigsTick((t) => t + 1);
              }}
              onStatusChange={() => setConfigsTick((t) => t + 1)}
            />
          ) : (
            <ConfigsPage key={configsTick} store={store} onEdit={(id) => setEditingId(id)} />
          ))}
        {page === "settings" && <SettingsPage store={store} />}
      </main>
    </div>
  );
}

function Navigation({ page, onNavigate }: { page: Page; onNavigate: (page: Page) => void }) {
  return (
    <>
      {NAV.map((item) => (
        <button key={item.id} type="button" className={page === item.id ? "active" : ""} onClick={() => onNavigate(item.id)}>
          <span className="nav-dot" />
          {item.label}
        </button>
      ))}
    </>
  );
}

function Login({ onDone }: { onDone: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
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
    <div className="login-page">
      <section className="login-brand-panel">
        <div className="login-brand-head">
          <JanusMark className="login-mark" />
          <div>
            <strong>Janus</strong>
            <small>GATEWAY CONTROL PLANE</small>
          </div>
        </div>
        <div className="login-intro">
          <span className="login-kicker">OPERATIONS / 01</span>
          <h1>让流量<br /><em>有序抵达。</em></h1>
          <p>在一个清晰的控制平面里，管理入口、服务发现与访问策略。</p>
        </div>
        <PelicanRide />
        <div className="login-brand-foot"><span />JANUS ADMIN · PRIVATE CONTROL PLANE</div>
      </section>
      <section className="login-form-panel">
        <div className="login-form-wrap">
          <div className="login-mobile-brand"><JanusMark className="login-mark" /><strong>Janus</strong></div>
          <form className="login-form" onSubmit={submit}>
            <div className="login-form-heading">
              <span className="login-kicker">WELCOME BACK</span>
              <h2>登录控制台</h2>
              <p>使用管理员账号继续操作。</p>
            </div>
            <div className="login-fields">
              <label className="login-field">
                <span>管理员账号</span>
                <input required autoComplete="username" placeholder="输入账号" value={username} onChange={(e) => setUsername(e.target.value)} />
              </label>
              <label className="login-field">
                <span>密码</span>
                <div className="password-field">
                  <input required type={showPassword ? "text" : "password"} autoComplete="current-password" placeholder="输入密码" value={password} onChange={(e) => setPassword(e.target.value)} />
                  <button type="button" className="password-toggle" onClick={() => setShowPassword((value) => !value)} aria-label={showPassword ? "隐藏密码" : "显示密码"}>
                    {showPassword ? "隐藏" : "显示"}
                  </button>
                </div>
              </label>
            </div>
            <button type="submit" className="login-submit" disabled={busy}>
              <span>{busy ? "正在验证" : "进入控制台"}</span><b aria-hidden="true">→</b>
            </button>
            {error && <div className="login-error" role="alert">{error}</div>}
            <div className="login-form-foot"><span className="login-lock" aria-hidden="true">●</span> 会话使用 HttpOnly Cookie 保护</div>
          </form>
        </div>
      </section>
    </div>
  );
}
