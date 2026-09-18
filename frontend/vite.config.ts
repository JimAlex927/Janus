import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const admin = { target: "http://127.0.0.1:9090", changeOrigin: false };

// Development only: the production console is served by Janus itself. Keeping
// these paths same-origin in Vite avoids a separate CORS/authentication mode.
export default defineConfig({
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
      "/api": admin,
      "/livez": admin,
      "/readyz": admin,
      "/metrics": admin,
    },
  },
});
