// input: HTML 容器、应用组件及基础样式。
// output: 挂载后的 Svelte 工单工作台。
// pos: 前端启动入口。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import { mount } from "svelte";
import App from "./App.svelte";
import "./app.css";

mount(App, { target: document.getElementById("app")! });
