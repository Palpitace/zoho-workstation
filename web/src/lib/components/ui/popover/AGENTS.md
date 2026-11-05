一旦我所属的文件夹有所变化请更新我。

负责 shadcn-svelte popover 基础组件。
由工单 Combobox 组合使用，与共享样式和 Bits UI 配合。
不包含工单查询或业务状态。

| 名称 | 地位 | 功能 |
| --- | --- | --- |
| AGENTS.md | 目录说明 | 来源与直接索引 |
| index.ts | 生成组件 | 导出当前使用的组件 |
| popover-content.svelte | 生成组件 | 官方基础交互与样式 |
| popover-portal.svelte | 生成组件 | 官方基础交互与样式 |
| popover-trigger.svelte | 生成组件 | 官方基础交互与样式 |
| popover.svelte | 生成组件 | 官方基础交互与样式 |

2026-11-05 使用固定 shadcn-svelte 1.8.0 CLI/Vega registry 生成；仅保留当前消费者需要的组件和导出。原有 button/input 未覆盖，无第二 UI 库。
