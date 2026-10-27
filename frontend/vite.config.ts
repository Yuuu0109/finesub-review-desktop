import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
// 后端默认 8765；该端口可能落在 Windows 动态端口保留段内（见 README），
// 此时用 REVIEW_API 指向实际端口，例如 REVIEW_API=http://127.0.0.1:9100。
const api = process.env.REVIEW_API ?? "http://127.0.0.1:8765";
export default defineConfig({
  plugins: [vue()],
  server: { port: 5173, strictPort: true, proxy: { "/api": api } },
});
