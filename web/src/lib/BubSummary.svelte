<!-- input: Bub 结果、当前消息、操作反馈和重新总结回调。 -->
<!-- output: 区分建议/执行/结果的引用总结及受控重新总结入口。 -->
<!-- pos: 工单详情中的 Bub 总结展示与证据定位。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { dateTime, type BubView, type Message, type ActionFeedback } from "./api";

  let { view, messages, loading, error, control, onRerun }: { view: BubView | null; messages: Message[]; loading: boolean; error: string; control: ActionFeedback; onRerun: () => void } = $props();
  const summary = $derived(view?.success?.result?.summary);
  const failed = $derived(view?.current?.status === "failed" || view?.current?.status === "invalid");
  const executions: Record<string, string> = { suggested: "客服提出建议，未确认执行", customer_confirmed_executed: "客户确认已执行", unknown: "是否执行未知" };
  function showMessage(message: Message) {
    const element = document.getElementById(`message-${message.source}-${message.id}`);
    element?.focus({ preventScroll: true });
    element?.scrollIntoView({ block: "start" });
  }
</script>

{#snippet references(ids: string[])}
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground" aria-label="消息引用">
    {#each ids as id}
      {@const matches = messages.filter((message) => message.id === id)}
      {#if matches.length === 1}
        <Button variant="link" class="min-h-10 max-w-full p-0 text-xs whitespace-normal break-all" aria-label={`查看引用消息 ${id}`} onclick={() => showMessage(matches[0])}>消息 {id}</Button>
      {:else}
        <span class="break-all">消息 {id}：{matches.length === 0 ? "当前会话中未找到" : "存在多条匹配，需核对"}</span>
      {/if}
    {:else}<span>无可引用消息</span>{/each}
  </div>
{/snippet}

<section aria-labelledby="bub-title" class="space-y-5 border-t pt-6" aria-busy={loading}>
  <header class="flex flex-wrap items-center gap-3">
    <h2 id="bub-title" class="text-lg font-semibold">Bub 沟通总结</h2>
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
    <div class="space-y-6 rounded-lg border p-5 sm:p-6">
      <section aria-labelledby="bub-current-title"><h3 id="bub-current-title" class="mb-2 text-sm font-semibold">当前情况</h3>
        <p class="text-sm leading-7 whitespace-pre-wrap break-words">{summary.current_situation.text}</p>
        {@render references(summary.current_situation.message_ids)}
      </section>
      <section aria-labelledby="bub-progress-title"><h3 id="bub-progress-title" class="mb-3 text-sm font-semibold">关键沟通过程</h3>
        <ol class="space-y-3">
          {#each summary.key_progress as fact}<li><p class="text-sm leading-7 whitespace-pre-wrap break-words">{fact.text}</p>{@render references(fact.message_ids)}</li>
          {:else}<li class="text-sm text-muted-foreground">暂无有依据的关键进展。</li>{/each}
        </ol>
      </section>
      <section aria-labelledby="bub-measures-title"><h3 id="bub-measures-title" class="mb-3 text-sm font-semibold">措施、执行确认与结果</h3>
        <ul class="space-y-5">
          {#each summary.attempted_measures as measure}
            <li class="space-y-2">
              <Badge variant="outline">{executions[measure.execution_status] || "是否执行未知"}</Badge>
              <p class="text-sm leading-7 whitespace-pre-wrap break-words">{measure.action}</p>
              <p class="text-xs text-muted-foreground">{measure.result_status === "customer_reported" ? "客户报告执行结果" : "执行结果未知 / 未确认"}</p>
              <p class="text-sm leading-7 whitespace-pre-wrap break-words">{measure.result}</p>
              {@render references(measure.message_ids)}
            </li>
          {:else}<li class="text-sm text-muted-foreground">暂无有依据的措施记录。</li>{/each}
        </ul>
      </section>
      <section aria-labelledby="bub-blockers-title"><h3 id="bub-blockers-title" class="mb-3 text-sm font-semibold">当前卡点与缺失信息</h3>
        <ul class="space-y-3">
          {#each summary.blockers as fact}<li><p class="text-sm leading-7 whitespace-pre-wrap break-words">{fact.text}</p>{@render references(fact.message_ids)}</li>
          {:else}<li class="text-sm text-muted-foreground">暂无已记录的卡点。</li>{/each}
        </ul>
      </section>
      <section aria-labelledby="bub-next-title"><h3 id="bub-next-title" class="mb-3 text-sm font-semibold">建议的下一步</h3>
        <ol class="space-y-4">
          {#each summary.next_steps as step}<li class="space-y-2"><Badge variant="secondary">{step.basis === "recommendation" ? "模型建议" : "待验证假设"}</Badge><p class="text-sm leading-7 whitespace-pre-wrap break-words">{step.text}</p>{@render references(step.message_ids)}</li>
          {:else}<li class="text-sm text-muted-foreground">暂无有依据的下一步建议。</li>{/each}
        </ol>
      </section>
    </div>
    <details class="text-xs text-muted-foreground"><summary class="cursor-pointer">总结来源</summary><dl class="mt-3 space-y-2 break-all">
      <div><dt class="inline">完成时间：</dt><dd class="inline">{dateTime(view.success.completed_at)}</dd></div>
      <div><dt class="inline">请求模型：</dt><dd class="inline">{view.success.requested_model}</dd></div>
      <div><dt class="inline">返回模型：</dt><dd class="inline">{view.success.returned_model || "接口未提供"}</dd></div>
      <div><dt class="inline">Schema：</dt><dd class="inline">{view.success.schema_version}</dd></div>
      <div><dt class="inline">输入版本：</dt><dd class="inline">{view.success.input_hash}</dd></div>
      {#if view.success.result?.request_id}<div><dt class="inline">请求标识：</dt><dd class="inline">{view.success.result.request_id}</dd></div>{/if}
    </dl></details>
  {:else if !loading && !error}<p class="text-sm text-muted-foreground">尚无成功总结。</p>{/if}
</section>
