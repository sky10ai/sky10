import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const rpcTarget = env.SKY10_WEB_RPC_TARGET || "http://localhost:9101";

  return {
    plugins: [react()],
    server: {
      port: 5173,
      proxy: {
        "/rpc": {
          target: rpcTarget,
          changeOrigin: true,
        },
        "/health": {
          target: rpcTarget,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: "dist",
      emptyOutDir: true,
    },
  };
});
