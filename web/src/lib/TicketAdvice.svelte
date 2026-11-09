<!-- input: 当前工单 ID、会话消息和内存中的会话防护令牌。 -->
<!-- output: 共享任务状态、内部建议及来源/消息证据展示。 -->
<!-- pos: 工单事实总结之外的内部处理建议。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { taskLabel } from "./api";
  import { onMount } from "svelte";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { dateTime, postAction, type Message } from "./api";
  import { knowledgeJSON, knowledgeLink, type TicketKnowledge } from "./knowledge-api";

  let { ticketID, token, messages }: { ticketID: string; token: string; messages: Message[] } = $props();
  let knowledgeState = $state<TicketKnowledge | null>(null);
  let loading = $state(true);
  let submitting = $state(false);
  let error = $state("");
  let actionError = $state("");
  let notice = $state("");
  let request: AbortController | undefined;
  let action: AbortController | undefined;
  let polling = false;
  let disposed = false;
  let returnTarget = $state<HTMLElement | null>(null);
  const view = $derived(knowledgeState?.advice);
  const result = $derived(view?.success?.result);
  const failed = $derived(view?.current?.status === "failed" || view?.current?.status === "invalid");
  const blocked = $derived(!token ? "等待有效运行状态" : !knowledgeState?.enabled ? "Bub 已关闭" : knowledgeState.automatic_eligible === false ? "工单尚未完整同步或已停止" : submitting ? "正在提交" : view?.current?.status === "running" ? "本任务正在执行" : knowledgeState?.busy ? "其他分析任务正在执行，请稍后重试" : view?.waiting_reason || "");

  async function load() {
    if (disposed || polling) return;
    polling = true;
    request = new AbortController();
    const signal = request.signal;
    try {
      const next = await knowledgeJSON<TicketKnowledge>(`/api/tickets/${encodeURIComponent(ticketID)}/knowledge`, signal);
      if (!signal.aborted) { knowledgeState = next; error = ""; }
    } catch (failure) {
      if (!signal.aborted) error = failure instanceof Error ? failure.message : "暂时无法读取建议。";
    } finally {
      if (!signal.aborted) loading = false;
      polling = false;
    }
  }
  async function rerun() {
    if (blocked || loading) return;
    submitting = true; notice = actionError = "";
    action = new AbortController();
    try {
      await postAction(`/api/tickets/${encodeURIComponent(ticketID)}/analysis/advice`, token, action.signal);
      if (!action.signal.aborted) { notice = "请求已受理，完成后自动更新。"; await load(); }
    } catch (failure) {
      if (!action.signal.aborted) actionError = failure instanceof Error ? failure.message : "建议请求未受理。";
    } finally { submitting = false; }
  }
  function showMessage(id: string, origin: HTMLElement) {
    const matches = messages.filter((message) => message.id === id);
    if (matches.length !== 1) return;
    const target = document.getElementById(`message-${matches[0].source}-${id}`);
    if (!target) return;
    returnTarget = origin;
    for (let parent = target.parentElement; parent; parent = parent.parentElement) if (parent instanceof HTMLDetailsElement) parent.open = true;
    target.focus({ preventScroll: true }); target.scrollIntoView({ block: "start" });
  }
  function returnToAdvice() {
    const target = returnTarget?.isConnected ? returnTarget : document.getElementById("advice-title");
    target?.focus({ preventScroll: true }); target?.scrollIntoView({ block: "center" }); returnTarget = null;
  }
  onMount(() => {
    const poll = () => { if (document.visibilityState === "visible") void load(); };
    void load();
    const timer = window.setInterval(poll, 5000);
    document.addEventListener("visibilitychange", poll);
    return () => { disposed = true; clearInterval(timer); document.removeEventListener("visibilitychange", poll); request?.abort(); action?.abort(); };
  });
</script>

<section class="space-y-4 border-t pt-6" aria-labelledby="advice-title" aria-busy={loading}>
  <header class="flex flex-wrap items-center gap-3">
    <h2 id="advice-title" tabindex="-1" class="text-lg font-semibold">参考排查建议</h2>
    {#if view?.task_state}<Badge variant="secondary">{taskLabel(view.task_state)}</Badge>
    {:else if failed}<Badge variant="destructive">最近生成失败</Badge>
    {:else if view?.current?.status === "running"}<Badge variant="secondary">生成中</Badge>
    {:else if view?.stale}<Badge variant="outline">待更新</Badge>
    {:else if result}<Badge variant="secondary">已生成</Badge>
    {:else if view?.waiting_reason}<Badge variant="outline">等待资料</Badge>{/if}
    <Button variant="outline" class="h-10 sm:ml-auto" disabled={loading || !!blocked} title={blocked} onclick={rerun}>重新生成建议</Button>
  </header>
  <p class="text-xs text-muted-foreground">供内部处理参考，不会自动回复客户。</p>
  {#if loading}<p role="status" class="text-sm text-muted-foreground">正在读取建议…</p>{/if}
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  {#if actionError}<p role="alert" class="text-sm text-destructive">{actionError}</p>{/if}
  {#if notice}<p role="status" class="text-sm text-muted-foreground">{notice}</p>{/if}
  {#if knowledgeState && !knowledgeState.enabled}<p class="text-sm text-muted-foreground">Bub 已关闭，暂停知识提取和建议生成。</p>{/if}
  {#if view?.waiting_reason}<p class="text-sm text-muted-foreground">{view.waiting_reason}</p>{/if}
  {#if view?.current?.error}<p role="alert" class="text-sm break-words text-destructive">{view.current.error}</p>{/if}
  {#if view?.next_retry_at}<p class="text-xs text-muted-foreground">自动重试：{dateTime(view.next_retry_at)}</p>{/if}
  {#if view?.stale}
    <p class="text-sm text-muted-foreground">工单或参考资料已变化，旧建议不再作为当前处理依据。</p>
  {:else if result && !error}
    {#if result.advice.conflicts.length}
      <div class="space-y-2 rounded-md border bg-muted/40 p-4 text-sm"><h3 class="font-medium">需要先核对</h3><ul class="list-inside list-disc space-y-1">{#each result.advice.conflicts as conflict}<li class="whitespace-pre-wrap break-words">{conflict}</li>{/each}</ul></div>
    {/if}
    <ol class="space-y-5">
      {#each result.advice.steps as step, index}
        <li class="space-y-2 text-sm">
          <h3 class="font-medium leading-7 whitespace-pre-wrap break-words">{index + 1}. {step.action}</h3>
          <p class="leading-7 whitespace-pre-wrap break-words text-muted-foreground">{step.reason}</p>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
            {#each step.source_ids as id}
              {@const source = result.sources.find((item) => item.id === id)}
              {#if source}<a class="inline-flex min-h-10 items-center underline underline-offset-4" href={knowledgeLink(source.entry_id, source.block_id)}>{source.kind === "sop" ? "SOP" : "案例"}：{source.title}</a>{/if}
            {/each}
            {#each step.current_message_ids as id}
              {#if messages.filter((message) => message.id === id).length === 1}<Button variant="link" class="min-h-10 p-0 text-xs" onclick={(event) => showMessage(id, event.currentTarget)}>当前工单消息 {id}</Button>{/if}
            {/each}
          </div>
        </li>
      {:else}<li class="text-sm text-muted-foreground">当前资料不足以给出具体排查步骤。</li>{/each}
    </ol>
    {#if result.advice.missing_information.length}
      <section class="space-y-2"><h3 class="text-sm font-medium">待补充信息</h3><ul class="list-inside list-disc space-y-1 text-sm text-muted-foreground">{#each result.advice.missing_information as item}<li class="whitespace-pre-wrap break-words">{item}</li>{/each}</ul></section>
    {/if}
  {:else if !loading && !error && !view?.waiting_reason}<p class="text-sm text-muted-foreground">尚无建议。总结完成且有适用来源后，将自动生成。</p>{/if}
  {#if knowledgeState?.case.current?.status === "failed" || knowledgeState?.case.current?.status === "invalid"}<p class="text-xs text-muted-foreground">本工单案例暂未整理成功，事实总结和现有参考建议仍可使用。{knowledgeState.case.next_retry_at ? `重试：${dateTime(knowledgeState.case.next_retry_at)}` : ""}</p>{/if}
</section>
{#if returnTarget}<Button class="fixed right-5 bottom-5 z-40 shadow-lg" onclick={returnToAdvice}>返回建议</Button>{/if}
