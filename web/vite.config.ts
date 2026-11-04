// input: Svelte 源码、Tailwind 样式和本机 Go API。
// output: 开发代理与 dist 静态构建产物。
// pos: 前端构建和开发配置。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  resolve: { alias: { $lib: new URL("./src/lib", import.meta.url).pathname } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
  build: { outDir: "dist" },
});
