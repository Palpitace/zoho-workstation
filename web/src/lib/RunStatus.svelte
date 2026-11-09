<!-- input: Go 同步/AI 状态、精确恢复候选、会话令牌及刷新回调。 -->
<!-- output: Issue #10 范围恢复/缓存待确认；当前同步范围、Bub活动任务、按任务分类积压及持久失败/重试详情。 -->
<!-- pos: 运行状态页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { taskLabel, modelFailureHint, dateTime, postAction, type AIKind, type RetryCandidate, type SystemStatus } from "./api";
  let { status, loading, error, onRefresh }: { status: SystemStatus | null; loading: boolean; error: string; onRefresh: () => Promise<void> } = $props();
  let confirmation = $state<{ kind: AIKind; candidates: RetryCandidate[] } | null>(null);
  let submitting = $state(false);
  let recoveryError = $state("");
  let recoveryNotice = $state("");
  let request: AbortController | undefined;
  onDestroy(() => request?.abort());
  async function recover() {
    if (!confirmation || !status || submitting) return;
    submitting = true;
    recoveryError = recoveryNotice = "";
    request = new AbortController();
    const batch = confirmation;
    try {
      await postAction(`/api/analysis/${batch.kind}/retry`, status.csrf_token, request.signal, { candidates: batch.candidates });
      confirmation = null;
      recoveryNotice = `已受理 ${batch.candidates.length} 张 ${batch.kind === "bub" ? "Bub" : "Jev"} 重试，每张只执行一次。`;
    } catch (error) {
      if (!request.signal.aborted) recoveryError = error instanceof Error ? error.message : "恢复请求失败，请刷新后重试。";
    } finally {
      if (!request.signal.aborted) await onRefresh();
      submitting = false;
    }
  }
  const taskKinds: Record<string, string> = { bub: "工单总结", case: "案例提取", advice: "处理建议" };
  const taskCounts = ["pending", "running", "retry_waiting", "manual", "waiting"] as const;
  const kinds = ["jev", "bub"] as const;
  const counts = [["pending", "待处理"], ["running", "处理中"], ["failed", "失败"], ["success", "成功"], ["stale", "过期"]] as const;
</script>

<section class="max-w-4xl space-y-8" aria-labelledby="page-title" aria-busy={loading}>
  <header><h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">运行状态</h1><p class="mt-2 text-sm text-muted-foreground">当前部门的持续发现进度与已保存的同步结果。</p></header>
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  {#if status}
    <section class="rounded-lg border p-5 sm:p-6" aria-labelledby="sync-title">
      <div class="mb-6 flex flex-wrap items-center justify-between gap-3"><h2 id="sync-title" class="font-semibold">只读同步</h2><Badge variant={status.sync.last_error ? "destructive" : "secondary"}>{status.runtime?.sync_running ? "同步进行中" : status.sync.last_error ? "最近同步未完成" : status.sync.batch_upper ? "分批处理中" : status.sync.watermark ? "已完成一轮同步" : "等待首次同步"}</Badge></div>
      <dl class="grid gap-x-8 gap-y-5 text-sm sm:grid-cols-2">
        <div><dt class="text-muted-foreground">本次服务启动</dt><dd class="mt-1 tabular-nums">{dateTime(status.runtime?.started_at)}</dd></div>
        <div><dt class="text-muted-foreground">同步范围</dt><dd class="mt-1">{status.runtime?.sync_recent_limit ? `按创建时间滚动最新 ${status.runtime.sync_recent_limit} 张` : dateTime(status.runtime?.sync_start_time)}</dd></div>
        <div><dt class="text-muted-foreground">下次定时调度</dt><dd class="mt-1">{status.runtime?.sync_running ? "当前同步仍在进行，完成后继续调度" : dateTime(status.runtime?.next_sync_at)}</dd></div>
        <div><dt class="text-muted-foreground">{status.runtime?.sync_recent_limit ? (status.sync.recent_confirmed === false ? "本地缓存（范围待确认）" : "当前已选工单") : "本轮发现进度"}</dt><dd class="mt-1">{#if status.runtime?.sync_recent_limit}{status.sync.selected} / {status.runtime.sync_recent_limit} 张{:else}已扫描 {status.sync.discovery_offset ?? 0} 条列表记录 · {status.sync.discovery_done ? "扫描完成" : "分页发现中"}{/if}</dd></div>
        <div><dt class="text-muted-foreground">当前待同步工作</dt><dd class="mt-1">{status.runtime?.last_sync_batch?.remaining ?? 0} 项{#if !status.runtime?.sync_recent_limit}（包含尚未完成的发现分页）{/if}</dd></div>
        <div><dt class="text-muted-foreground">最近批次</dt><dd class="mt-1">保存 {status.runtime?.last_sync_batch?.fetched ?? 0} · 失败 {status.runtime?.last_sync_batch?.failed ?? 0} · 停止 {status.runtime?.last_sync_batch?.stopped ?? 0}</dd></div>
        <div><dt class="text-muted-foreground">已停止同步</dt><dd class="mt-1">{status.runtime?.stopped_count ?? 0} 张，历史保留</dd></div>
        <div><dt class="text-muted-foreground">当前部门 ID</dt><dd class="mt-1 break-all">{status.sync.department_id}</dd></div>
        <div><dt class="text-muted-foreground">已发现工单</dt><dd class="mt-1 tabular-nums">{status.sync.selected} 张</dd></div>
        <div><dt class="text-muted-foreground">已有快照</dt><dd class="mt-1 tabular-nums">{status.sync.ready} 张</dd></div>
        <div><dt class="text-muted-foreground">尚待首次保存</dt><dd class="mt-1 tabular-nums">{Math.max(0, status.sync.selected - status.sync.ready)} 张</dd></div>
        <div><dt class="text-muted-foreground">同步失败或不完整</dt><dd class="mt-1 tabular-nums">{status.sync.failed} 张</dd></div>
        <div><dt class="text-muted-foreground">最近尝试</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.last_attempt)}</dd></div>
        <div><dt class="text-muted-foreground">已完成同步至</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.watermark)}</dd></div>
        {#if status.sync.batch_upper}<div><dt class="text-muted-foreground">本轮截止时间</dt><dd class="mt-1 tabular-nums">{dateTime(status.sync.batch_upper)}</dd></div>{/if}
      </dl>
      {#if status.sync.last_error}<p role="alert" class="mt-5 border-t pt-4 text-sm break-words text-destructive">{status.runtime?.last_sync_batch?.stage === "selection" ? "工单范围选取失败，等待重试：" : status.runtime?.last_sync_batch?.stage === "details" ? "工单详情读取失败：" : ""}{status.sync.last_error}</p>{/if}
      {#if status.runtime?.last_error?.sync && status.runtime.last_error.sync !== status.sync.last_error}<p role="alert" class="mt-4 text-sm break-words text-destructive">{status.runtime.last_error.sync}</p>{/if}
    </section>
    {#if status.runtime}
      <p class="text-sm text-muted-foreground">{status.runtime.automatic ? "自动任务已启用" : "自动任务未启用"}；{status.runtime.ai_scope === "all_synced" ? `自动分析全部已完整同步的有效工单${status.runtime.ai_scope_count === undefined ? "" : `（${status.runtime.ai_scope_count} 张）`}` : "分析范围以当前服务配置为准"}。先由 Bub 总结，再由 Jev 基于当前总结评估；没有可用总结时 Jev 等待。每张完成后继续处理下一张，已有当前结果会复用，失败自动退避重试，不再因累计失败次数永久停止。</p>
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
    {#if status.bub_tasks}
      <section class="space-y-4 rounded-lg border p-5" aria-labelledby="bub-tasks-title">
        <h2 id="bub-tasks-title" class="font-semibold">Bub 任务</h2>
        <p class="text-xs text-muted-foreground">按任务计数；同一工单可包含总结、案例提取和建议。</p>
        <dl class="grid grid-cols-2 gap-4 sm:grid-cols-5">
          {#each taskCounts as state}<div><dt class="text-xs text-muted-foreground">{taskLabel(state)}</dt><dd>{status.bub_tasks.counts[state] ?? 0}</dd></div>{/each}
        </dl>
        {#each status.bub_tasks.active as task}
          <div class="rounded border p-3 text-sm" aria-label="正在执行的 Bub 任务">
            <a class="underline" href={`#/tickets/${encodeURIComponent(task.ticket_id)}`}>工单 #{task.ticket_number || task.ticket_id}</a> · {taskKinds[task.kind] || task.kind} · 执行中
            <p class="mt-1 text-muted-foreground">开始：{dateTime(task.started_at)} · 已运行 {task.elapsed_seconds ?? 0} 秒</p>
          </div>
        {:else}<p class="text-sm text-muted-foreground">当前没有正在执行的 Bub 任务</p>{/each}
        <details><summary class="cursor-pointer text-sm">查看待处理与重试任务（{status.bub_tasks.tasks.length}）</summary>
          <ul class="mt-3 max-h-96 divide-y overflow-auto text-sm">
            {#each status.bub_tasks.tasks as task}
              <li class="space-y-1 py-3">
                <a class="underline" href={`#/tickets/${encodeURIComponent(task.ticket_id)}`}>工单 #{task.ticket_number || task.ticket_id}</a> · {taskKinds[task.kind] || task.kind} · {taskLabel(task.state)}
                <p>已尝试 {task.attempts} 次{task.last_failed_at ? ` · 最近失败：${dateTime(task.last_failed_at)}` : ""}</p>
                {#if task.next_retry_at}<p>{task.state === "retry_waiting" ? "下次重试" : "重试到期时间"}：{dateTime(task.next_retry_at)}</p>{/if}
                {#if task.reason}<p class="text-muted-foreground">{task.reason}</p>{/if}
                {#if task.error}<p class="break-words text-destructive">{modelFailureHint(task.error)} {task.error}</p>{/if}
              </li>
            {/each}
          </ul>
        </details>
      </section>
    {/if}
    <section aria-labelledby="capabilities-title">
      <h2 id="capabilities-title" class="mb-3 font-semibold">分析能力</h2>
      <div class="divide-y rounded-lg border text-sm">
        {#each kinds as kind}
          <section class="space-y-4 p-4" aria-label={kind === "jev" ? "Jev 结构化评估" : "Bub 沟通总结"}>
            <div class="flex flex-wrap items-center justify-between gap-3"><h3>{kind === "jev" ? "Jev 结构化评估" : "Bub 沟通总结"}</h3><Badge variant="outline">{!status[`${kind}_enabled`] ? "已关闭" : status.runtime?.[`${kind}_running`] ? "任务进行中" : "已启用"}</Badge></div>
            {#if status.ai?.[kind]}<dl class="grid grid-cols-3 gap-3 sm:grid-cols-5">{#each counts as [key, label]}<div><dt class="text-xs text-muted-foreground">{label}</dt><dd class="mt-1 tabular-nums">{status.ai[kind][key]}</dd></div>{/each}</dl>{/if}
            {#if status.ai?.[kind]?.retry_waiting}
              <p class="text-muted-foreground">{status.ai[kind].retry_waiting} 张等待自动重试；按 1、2、4、8、16、30 分钟退避，之后每 30 分钟继续尝试。详情可查看失败与下次重试时间。</p>
            {/if}
            {#if status.runtime?.recovery_pending?.[kind]}<p role="status">已确认恢复：还有 {status.runtime.recovery_pending[kind]} 张待开始。</p>{/if}
            {#if status.retry_candidates?.[kind]?.length}
              <Button variant="outline" disabled={submitting || !!error || !!confirmation || !status.runtime?.automatic || !!status.runtime?.[`${kind}_running`] || !!status.runtime?.recovery_pending?.[kind]} onclick={() => { confirmation = { kind, candidates: [...status!.retry_candidates![kind]] }; recoveryError = recoveryNotice = ""; }}>立即重试失败的 {kind === "bub" ? "Bub" : "Jev"}（{status.retry_candidates[kind].length}）</Button>
            {/if}
            {#if status.runtime?.last_error?.[kind]}<p role="alert" class="break-words text-destructive">{status.runtime.last_error[kind]}</p>{/if}
          </section>
        {/each}
      </div>
    </section>
    {#if confirmation}
      <section aria-label="确认恢复分析" class="space-y-3 rounded-lg border p-5 text-sm">
        <p>确认已修复运行环境后，将重试当前选定的 {confirmation.candidates.length} 张 {confirmation.kind === "bub" ? "Bub" : "Jev"} 任务，每张最多一次，会产生模型请求费用。{confirmation.kind === "bub" ? "总结成功后，Jev 会按正常流程自动评估。" : ""}</p>
        <p class="text-muted-foreground">保留全部失败历史；再次失败将继续按退避间隔自动重试。服务重启后，未开始的恢复项需要重新确认。</p>
        <div class="flex gap-2"><Button disabled={submitting || !!error} onclick={recover}>{submitting ? "正在提交…" : "确认重试"}</Button><Button variant="outline" disabled={submitting} onclick={() => confirmation = null}>取消</Button></div>
      </section>
    {/if}
    {#if recoveryError}<p role="alert" class="text-sm text-destructive">{recoveryError}</p>{/if}
    {#if recoveryNotice}<p role="status" class="text-sm text-muted-foreground">{recoveryNotice}</p>{/if}
  {:else if loading}<p role="status" class="text-sm text-muted-foreground">正在读取运行状态…</p>{/if}
</section>
