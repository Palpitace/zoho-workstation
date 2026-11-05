<!-- input: Go 同步进度、自动运行状态、独立 AI 数量及模型调用统计。 -->
<!-- output: 当前部门、同步批次/并发、任务失败及累计调用状态概览。 -->
<!-- pos: 运行状态页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { dateTime, type SystemStatus } from "./api";
  let { status, loading, error }: { status: SystemStatus | null; loading: boolean; error: string } = $props();
  const kinds = ["jev", "bub"] as const;
  const counts = [["pending", "待处理"], ["running", "处理中"], ["failed", "失败"], ["success", "成功"], ["stale", "过期"]] as const;
</script>

<section class="max-w-4xl space-y-8" aria-labelledby="page-title" aria-busy={loading}>
  <header><h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">运行状态</h1><p class="mt-2 text-sm text-muted-foreground">当前部门的采样进度与已保存的同步结果。</p></header>
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  {#if status}
    <section class="rounded-lg border p-5 sm:p-6" aria-labelledby="sync-title">
      <div class="mb-6 flex flex-wrap items-center justify-between gap-3"><h2 id="sync-title" class="font-semibold">只读同步</h2><Badge variant={status.sync.last_error ? "destructive" : "secondary"}>{status.runtime?.sync_running ? "同步进行中" : status.sync.last_error ? "最近同步未完成" : status.sync.batch_upper ? "分批处理中" : status.sync.watermark ? "已完成一轮同步" : "等待首次同步"}</Badge></div>
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
      {#if status.runtime?.last_error?.sync && status.runtime.last_error.sync !== status.sync.last_error}<p role="alert" class="mt-4 text-sm break-words text-destructive">{status.runtime.last_error.sync}</p>{/if}
    </section>
    {#if status.runtime}
      <p class="text-sm text-muted-foreground">{status.runtime.automatic ? "自动任务已启用" : "自动任务未启用"}；{status.runtime.ai_scope === "all_synced" ? `自动分析全部已完整同步的有效工单${status.runtime.ai_scope_count === undefined ? "" : `（${status.runtime.ai_scope_count} 张）`}` : "分析范围以当前服务配置为准"}。每张完成后继续处理下一张；已有当前结果会复用，失败最多自动重试一次。</p>
    {/if}
    {#if status.runtime?.sync_batch_size && status.runtime?.sync_concurrency}
      <p class="text-sm text-muted-foreground">同步每批最多 {status.runtime.sync_batch_size} 张工单，最多 {status.runtime.sync_concurrency} 张同时读取。</p>
    {/if}
    {#if status.budget}
      <section class="space-y-4 rounded-lg border p-5 sm:p-6" aria-labelledby="budget-title">
        <h2 id="budget-title" class="font-semibold">模型调用统计</h2>
        <dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div><dt class="text-muted-foreground">累计请求</dt><dd class="mt-1 tabular-nums">{status.budget.total} 次</dd></div>
          <div><dt class="text-muted-foreground">当前轮次请求</dt><dd class="mt-1 tabular-nums">{status.budget.round} 次</dd></div>
        </dl>
        <p class="text-xs text-muted-foreground">请求额度限制已关闭。失败请求与重试仍计入统计，刷新页面或重启服务不会清零。</p>
      </section>
    {/if}
    <section aria-labelledby="capabilities-title">
      <h2 id="capabilities-title" class="mb-3 font-semibold">分析能力</h2>
      <div class="divide-y rounded-lg border text-sm">
        {#each kinds as kind}
          <section class="space-y-4 p-4" aria-label={kind === "jev" ? "Jev 结构化评估" : "Bub 沟通总结"}>
            <div class="flex flex-wrap items-center justify-between gap-3"><h3>{kind === "jev" ? "Jev 结构化评估" : "Bub 沟通总结"}</h3><Badge variant="outline">{!status[`${kind}_enabled`] ? "已关闭" : status.runtime?.[`${kind}_running`] ? "任务进行中" : "已启用"}</Badge></div>
            {#if status.ai?.[kind]}<dl class="grid grid-cols-3 gap-3 sm:grid-cols-5">{#each counts as [key, label]}<div><dt class="text-xs text-muted-foreground">{label}</dt><dd class="mt-1 tabular-nums">{status.ai[kind][key]}</dd></div>{/each}</dl>{/if}
            {#if status.runtime?.last_error?.[kind]}<p role="alert" class="break-words text-destructive">{status.runtime.last_error[kind]}</p>{/if}
          </section>
        {/each}
      </div>
    </section>
  {:else if loading}<p role="status" class="text-sm text-muted-foreground">正在读取运行状态…</p>{/if}
</section>
