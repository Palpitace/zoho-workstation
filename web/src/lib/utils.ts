// input: shadcn-svelte 生成组件的类名与类型。
// output: 官方 cn 导出及组件辅助类型。
// pos: CLI 生成的样式工具。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
export { cn } from "cn";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChild<T> = T extends { child?: any } ? Omit<T, "child"> : T;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type WithoutChildren<T> = T extends { children?: any } ? Omit<T, "children"> : T;
export type WithoutChildrenOrChild<T> = WithoutChildren<WithoutChild<T>>;
export type WithElementRef<T, U extends HTMLElement = HTMLElement> = T & { ref?: U | null };
