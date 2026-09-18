import { useState, type FormEvent } from "react";
import { login, logout } from "./api";
import { ConfigEditorPage } from "./ConfigEditor";
import { ConfigsPage } from "./Configs";
import { Overview } from "./Overview";
import { PelicanRide } from "./PelicanRide";
import { SettingsPage } from "./Settings";
import { useConfig } from "./useConfig";

type Page = "overview" | "configs" | "settings";

const NAV: { id: Page; label: string }[] = [
  { id: "overview", label: "概览" },
  { id: "configs", label: "Config" },
  { id: "settings", label: "全局设置" },
];

const TITLES: Record<Page, string> = { overview: "系统概览", configs: "配置", settings: "全局设置" };

export function App() {
  const store = useConfig();
  const [page, setPage] = useState<Page>("overview");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [configsTick, setConfigsTick] = useState(0);

  if (store.status === "loading" || store.status === "error") {
    return (
      <div className="login-shell">
        <div className="login-card">
          <div className="brand-mark">J</div>
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
          <div className="brand-mark">J</div>
          <div>
            <strong>Janus</strong>
            <small>GATEWAY CONSOLE</small>
          </div>
        </div>
        <nav>
          {NAV.map((item) => (
            <button
              key={item.id}
              type="button"
              className={page === item.id ? "active" : ""}
              onClick={() => {
                setPage(item.id);
                if (item.id !== "configs") setEditingId(null);
              }}
            >
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
        {store.message && (
          <div className="toast">
            {store.message}
            <button type="button" onClick={() => store.setMessage("")} aria-label="关闭提示">
              ×
            </button>
          </div>
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
    <div className="login-split">
      <div className="login-scene-pane">
        <div className="login-brand">
          <div className="brand-mark">J</div>
          <div>
            <strong>Janus</strong>
            <small>GATEWAY CONSOLE</small>
          </div>
        </div>
        <PelicanRide />
      </div>
      <div className="login-form-pane">
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
    </div>
  );
}
