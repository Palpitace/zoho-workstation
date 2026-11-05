<!-- input: Bub 结果、当前消息、操作反馈和重新总结回调。 -->
<!-- output: 整单三段综合总结、未知作者原文标记、引用定位及返回总结入口。 -->
<!-- pos: 工单详情中的 Bub 总结展示与证据定位。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { type BubView, type Message, type ActionFeedback } from "./api";

  let { view, messages, loading, error, control, onRerun }: { view: BubView | null; messages: Message[]; loading: boolean; error: string; control: ActionFeedback; onRerun: () => void } = $props();
  const savedSummary = $derived(view?.success?.result?.summary);
  const summary = $derived(savedSummary && "device_info" in savedSummary ? savedSummary : null);
  const sections = [
    ["device_info", "客户设备信息"],
    ["customer_feedback", "客户反馈总结"],
    ["support_responses", "客服回复或给予的排查"],
  ] as const;
  const failed = $derived(view?.current?.status === "failed" || view?.current?.status === "invalid");
  let returnTarget = $state<HTMLElement | null>(null);
  function showMessage(message: Message, origin: HTMLElement) {
    const element = document.getElementById(`message-${message.source}-${message.id}`);
    if (!element) return;
    returnTarget = origin;
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      if (parent instanceof HTMLDetailsElement) parent.open = true;
    }
    element.focus({ preventScroll: true });
    element.scrollIntoView({ block: "start" });
  }
  function returnToSummary() {
    const target = returnTarget?.isConnected ? returnTarget : document.getElementById("bub-title");
    target?.focus({ preventScroll: true });
    target?.scrollIntoView({ block: "center" });
    returnTarget = null;
  }
</script>

{#snippet references(ids: string[])}
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground" aria-label="消息引用">
    {#each ids as id}
      {@const matches = messages.filter((message) => message.id === id)}
      {#if matches.length === 1}
        <Button variant="link" class="min-h-10 max-w-full p-0 text-xs whitespace-normal break-all" aria-label={`查看引用消息 ${id}`} onclick={(event) => showMessage(matches[0], event.currentTarget)}>消息 {id}</Button>
      {:else}
        <span class="break-all">消息 {id}：{matches.length === 0 ? "当前会话中未找到" : "存在多条匹配，需核对"}</span>
      {/if}
    {/each}
  </div>
{/snippet}

<section aria-labelledby="bub-title" class="space-y-5 border-t pt-6" aria-busy={loading}>
  <header class="flex flex-wrap items-center gap-3">
    <h2 id="bub-title" tabindex="-1" class="text-lg font-semibold">Bub 工单汇总</h2>
    {#if failed}<Badge variant="destructive">{view?.current?.status === "invalid" ? "无法总结" : "最近总结失败"}</Badge>
    {:else if view?.current?.status === "running"}<Badge variant="secondary">总结中</Badge>
    {:else if view?.success}<Badge variant="secondary">已有总结</Badge>
    {:else if !loading && !error}<Badge variant="outline">待总结</Badge>{/if}
    {#if view?.stale}<Badge variant="outline">结果已过期</Badge>{/if}
    <Button variant="outline" class="h-10 sm:ml-auto" onclick={onRerun} disabled={!!control.blocked}>重新总结</Button>
  </header>
  {#if control.blocked}<p class="text-xs text-muted-foreground">重新总结暂不可用：{control.blocked}</p>{/if}
  {#if control.error}<p role="alert" class="text-sm text-destructive">重新总结：{control.error}</p>{/if}
  {#if control.notice}<p role="status" class="text-sm text-muted-foreground">{control.notice}</p>{/if}
  {#if loading}<p role="status" class="text-sm text-muted-foreground">正在读取总结…</p>{/if}
  {#if error}<p role="alert" class="text-sm text-destructive">总结暂不可读：{error}</p>{/if}
  {#if view?.current?.error}<p role="alert" class="text-sm break-words text-destructive">{view.current.error}</p>{/if}
  {#if view?.stale}<p class="text-sm text-muted-foreground">以下是旧输入或旧配置对应的成功总结，不代表当前会话。</p>
  {:else if view?.success && failed}<p class="text-sm text-muted-foreground">保留上次成功总结，最近失败未覆盖已有结果。</p>{/if}
  {#if view?.success && summary}
    <div class="space-y-5">
      {#each sections as [key, title]}
        {#if summary[key].length}
          <section aria-labelledby={`bub-${key}-title`} class="space-y-2">
            <h3 id={`bub-${key}-title`} class="text-sm font-semibold">{title}</h3>
            <ul class="space-y-3 text-sm leading-7">
              {#each summary[key] as fact}
                <li>
                  {#if key === "customer_feedback" && fact.message_ids.length === 1 && messages.some((message) => message.id === fact.message_ids[0] && message.role === "unknown" && !message.mixed_authors)}
                    <p class="text-xs text-muted-foreground">来信原文 · 作者未确认</p>
                  {/if}
                  <p class="whitespace-pre-wrap break-words">{fact.text}</p>
                  {#if fact.message_ids.length}{@render references(fact.message_ids)}
                  {:else if key === "device_info"}<span class="text-xs text-muted-foreground">工单字段</span>{/if}
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {/each}
    </div>
    {#if sections.every(([key]) => summary[key].length === 0)}<p class="text-sm text-muted-foreground">当前会话没有可归纳的相关内容。</p>{/if}
  {:else if view?.success && savedSummary}
    <p class="text-sm text-muted-foreground">已有旧版总结已保留。请重新总结，生成设备信息、客户反馈和客服回复的新结构。</p>
  {:else if !loading && !error}<p class="text-sm text-muted-foreground">尚无成功总结。</p>{/if}
</section>

{#if returnTarget}
  <Button class="fixed right-5 bottom-5 z-40 shadow-lg" onclick={returnToSummary}>返回总结</Button>
{/if}
