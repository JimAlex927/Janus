import react from "@vitejs/plugin-react";
import { defineConfig, loadEnv } from "vite";

const admin = { target: "http://127.0.0.1:9090", changeOrigin: false };

function consoleBase(value?: string): string {
  if (!value) return "/";
  const trimmed = value.trim();
  if (!/^\/[A-Za-z0-9._/-]*$/.test(trimmed) || trimmed.includes("//")) {
    throw new Error("JANUS_UI_BASE_URL must be empty or an absolute URL path, such as /janus");
  }
  return trimmed === "/" ? "/" : `${trimmed.replace(/\/+$/, "")}/`;
}

// Development only: the production console is served by Janus itself. Keeping
// these paths same-origin in Vite avoids a separate CORS/authentication mode.
export default defineConfig(({ mode }) => {
  const base = consoleBase(loadEnv(mode, process.cwd(), "").JANUS_UI_BASE_URL);
  const proxyBase = base === "/" ? "" : base.slice(0, -1);
  const apiPrefix = `${proxyBase}/api`;
  return {
    base,
    plugins: [react()],
    build: {
      rollupOptions: {
        output: {
          // React changes much less often than the console, so keep it cacheable
          // independently of routine UI edits.
          manualChunks: { react: ["react", "react-dom"] },
        },
      },
    },
    server: {
      port: 5173,
      proxy: {
        [apiPrefix]: { ...admin, rewrite: (path) => proxyBase ? path.slice(proxyBase.length) : path },
        "/livez": admin,
        "/readyz": admin,
        "/metrics": admin,
      },
    },
  };
});
