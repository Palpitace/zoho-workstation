<!-- input: shadcn-svelte 组件属性、子内容和样式工具。 -->
<!-- output: 官方 Vega 基础组件的展示与交互能力。 -->
<!-- pos: CLI 生成组件，保留当前使用的导出。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
	import { Command as CommandPrimitive } from "bits-ui";
	import { cn } from "$lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: CommandPrimitive.ListProps = $props();
</script>

<CommandPrimitive.List
	bind:ref
	data-slot="command-list"
	class={cn("no-scrollbar max-h-72 scroll-py-1 outline-none overflow-x-hidden overflow-y-auto", className)}
	{...restProps}
/>
