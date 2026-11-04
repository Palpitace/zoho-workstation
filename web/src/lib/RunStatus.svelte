<!-- input: Go 同步进度与功能启用状态。 -->
<!-- output: 当前部门、同步水位、错误和功能状态的只读概览。 -->
<!-- pos: 运行状态页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { dateTime, type SystemStatus } from "./api";
  let { status, loading, error }: { status: SystemStatus | null; loading: boolean; error: string } = $props();
</script>

<section class="max-w-4xl space-y-8" aria-labelledby="page-title" aria-busy={loading}>
  <header><h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">运行状态</h1><p class="mt-2 text-sm text-muted-foreground">当前部门的采样进度与已保存的同步结果。</p></header>
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  {#if status}
    <section class="rounded-lg border p-5 sm:p-6" aria-labelledby="sync-title">
      <div class="mb-6 flex flex-wrap items-center justify-between gap-3"><h2 id="sync-title" class="font-semibold">只读同步</h2><Badge variant={status.sync.last_error ? "destructive" : "secondary"}>{status.sync.last_error ? "最近同步未完成" : status.sync.batch_upper ? "分批处理中" : status.sync.watermark ? "已完成一轮同步" : "等待首次同步"}</Badge></div>
      <dl class="grid gap-x-8 gap-y-5 text-sm sm:grid-cols-2">
        <div><dt class="text-muted-foreground">当前部门 ID</dt><dd class="mt-1 break-all">{status.sync.department_id}</dd></div>
        <div><dt class="text-muted-foreground">已选工单</dt><dd class="mt-1 tabular-nums">{status.sync.selected} 张</dd></div>
        <div><dt class="text-muted-foreground">已有快照</dt><dd class="mt-1 tabular-nums">{status.sync.ready} 张</dd></div>
        <div><dt class="text-muted-foreground">尚待首次保存</dt><dd class="mt-1 tabular-nums">{Math.max(0, status.sync.selected - status.sync.ready)} 张</dd></div>
        <div><dt class="text-muted-foreground">同步失败或不完整</dt><dd class="mt-1 tabular-nums">{status.sync.failed} 张</dd></div>
        <div><dt class="text-muted-foreground">最近尝试</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.last_attempt)}</dd></div>
        <div><dt class="text-muted-foreground">已完成同步至</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.watermark)}</dd></div>
        {#if status.sync.batch_upper}<div><dt class="text-muted-foreground">本轮截止时间</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.batch_upper)}</dd></div>{/if}
      </dl>
      {#if status.sync.last_error}<p role="alert" class="mt-5 border-t pt-4 text-sm break-words text-destructive">{status.sync.last_error}</p>{/if}
    </section>
    <section aria-labelledby="capabilities-title">
      <h2 id="capabilities-title" class="mb-3 font-semibold">分析能力</h2>
      <dl class="divide-y rounded-lg border text-sm">
        <div class="flex flex-wrap items-center justify-between gap-3 p-4"><dt>Jev 结构化评估</dt><dd><Badge variant="outline">{status.jev_enabled ? "已启用" : "已关闭"}</Badge></dd></div>
        <div class="flex flex-wrap items-center justify-between gap-3 p-4"><dt>Bub 沟通总结</dt><dd><Badge variant="outline">{status.bub_enabled ? "已启用" : "已关闭"}</Badge></dd></div>
      </dl>
    </section>
  {:else if loading}<p role="status" class="text-sm text-muted-foreground">正在读取运行状态…</p>{/if}
</section>
