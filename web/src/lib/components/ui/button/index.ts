// input: shadcn-svelte 组件属性、子内容和样式工具。
// output: 官方 Vega 基础组件的展示能力。
// pos: CLI 生成组件，仅本地文档头纳入约定。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import Root, {
	type ButtonProps,
	type ButtonSize,
	type ButtonVariant,
	buttonVariants,
} from "./button.svelte";

export {
	Root,
	type ButtonProps as Props,
	//
	Root as Button,
	buttonVariants,
	type ButtonProps,
	type ButtonSize,
	type ButtonVariant,
};
