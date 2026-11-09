// input: Svelte 源码、Tailwind 样式和本机 Go API。
// output: 保留浏览器 Origin、匹配 Go Host 校验的开发代理及静态构建及离线编辑器资源。
// pos: 前端构建和开发配置。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { cpSync, existsSync, readFileSync, statSync } from "node:fs";
import { resolve, extname, sep } from "node:path";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [svelte(), tailwindcss(), {
    name: "local-vditor-assets",
    configureServer(server) {
      server.middlewares.use("/vendor/vditor/dist", (req, res, next) => {
        const root = resolve("node_modules/vditor/dist");
        const path = resolve(root, "." + (req.url || "").split("?")[0]);
        if (!path.startsWith(root + sep) || !existsSync(path) || !statSync(path).isFile()) return next();
        const types: Record<string, string> = { ".js": "application/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".woff2": "font/woff2" };
        res.setHeader("Content-Type", types[extname(path)] || "application/octet-stream");
        res.end(readFileSync(path));
      });
    },
    closeBundle() {
      for (const part of ["css", "images", "js/lute", "js/i18n", "js/icons"])
        cpSync(`node_modules/vditor/dist/${part}`, `dist/vendor/vditor/dist/${part}`, { recursive: true });
    },
  }],
  resolve: { alias: { $lib: new URL("./src/lib", import.meta.url).pathname } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { "/api": { target: "http://127.0.0.1:8080", changeOrigin: true } },
  },
  build: { outDir: "dist" },
});
