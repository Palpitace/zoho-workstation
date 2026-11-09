<!-- input: 一个固定回复版本、独立模型评估、来源快照与人工评分。 -->
<!-- output: 可复制的离线回复、证据查看和不覆盖历史的评分表单。 -->
<!-- pos: 批次中的逐版本审核组件。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Button } from "$lib/components/ui/button/index.js";
  import { dateTime } from "./api";
  import type { LabCall, LabEvaluation, LabFeedback, LabReplyData } from "./lab-api";
  let { call, evaluation, feedback, disabled, onRate }: { call: LabCall; evaluation?: LabCall; feedback: LabFeedback[]; disabled: boolean; onRate: (value: { call_id: number; score: number; tags: string[]; note: string; edited_reply: string }) => Promise<boolean> } = $props();
  const reply = $derived(call.result as LabReplyData | undefined);
  const grade = $derived(evaluation?.result as LabEvaluation | undefined);
  const ratings = $derived(feedback.filter((f) => f.call_id === call.id));
  let score = $state(3), note = $state(""), rewrite = $state(""), tags = $state<string[]>([]), notice = $state("");
  async function rate(event: SubmitEvent) {
    event.preventDefault();
    if (await onRate({ call_id: call.id, score, tags, note, edited_reply: rewrite })) { note = rewrite = ""; tags = []; notice = "评分已保存；开发样本的反馈在下一轮使用，留出样本仅供审核。"; }
  }
  async function copy() { try { await navigator.clipboard.writeText(reply?.text || ""); notice = "已复制回复。"; } catch { notice = "无法访问剪贴板，请选择回复正文复制。"; } }
</script>

<section class="space-y-4 border-t py-5">
  <div class="flex flex-wrap items-center justify-between gap-2">
    <h4 class="text-sm font-semibold">{call.variant === "baseline" ? "原 FAQ" : "候选 FAQ"} · 回复 v{call.revision + 1}</h4>
    {#if reply}<Button variant="outline" size="sm" onclick={copy}>复制回复</Button>{/if}
  </div>
  {#if call.error}<p class="text-sm text-destructive">{call.error}</p>{/if}
  {#if reply}
    <p class="whitespace-pre-wrap break-words text-sm leading-7" lang={reply.language}>{reply.text}</p>
    <p class="text-sm text-muted-foreground">内部依据：{reply.rationale}</p>
    {#if reply.missing_information.length}<p class="text-sm">待补充：{reply.missing_information.join("；")}</p>{/if}
    <details class="text-sm">
      <summary class="cursor-pointer text-muted-foreground">查看此版本引用及调用信息</summary>
      <div class="mt-3 space-y-3">
        {#each reply.message_ids as id}<p class="whitespace-pre-wrap break-words"><span class="font-medium">消息 {id}：</span>{call.input.ticket.messages.find((m) => m.id === id)?.body || "引用不可用"}</p>{/each}
        {#each reply.source_ids as id}{@const source = call.input.sources.find((s) => s.id === id)}<p class="whitespace-pre-wrap break-words"><span class="font-medium">{source?.title || id}：</span>{source?.text || "引用不可用"}</p>{/each}
        <p class="break-all text-xs text-muted-foreground">调用 #{call.id} · {dateTime(call.completed_at)} · {call.returned_model || "模型版本未返回"}<br />输入 {call.input_hash}</p>
      </div>
    </details>
    <div class="space-y-2 bg-muted/40 p-3 text-sm">
      <p class="font-medium">模型评估：{grade ? `${({ pass: "通过", revise: "建议改写", unsafe: "存在实质问题" } as Record<string,string>)[grade.verdict] || grade.verdict} · ${grade.score}/5` : evaluation?.error || "等待评估"}</p>
      {#each grade?.findings || [] as finding}<p>{finding.severity === "blocker" ? "需处理：" : ""}{finding.reason}</p>{/each}
      {#each grade?.suggestions || [] as suggestion}<p>{suggestion}</p>{/each}
    </div>
    <details>
      <summary class="cursor-pointer text-sm font-medium">人工评分与修改{ratings.length ? ` · ${ratings.length} 条记录` : ""}</summary>
      {#each ratings as f}<div class="mt-3 border-b pb-3 text-sm"><p>{f.score}/5 · {dateTime(f.created_at)} · {f.tags.join("、")}</p>{#if f.note}<p class="mt-1 whitespace-pre-wrap">{f.note}</p>{/if}{#if f.edited_reply}<p class="mt-1 whitespace-pre-wrap">人工改写：{f.edited_reply}</p>{/if}</div>{/each}
      <form onsubmit={rate} class="mt-4 space-y-3">
        <label class="flex items-center gap-3 text-sm">评分<select class="rounded-md border bg-background px-3 py-2" bind:value={score}>{#each [1,2,3,4,5] as value}<option value={value}>{value} 分</option>{/each}</select></label>
        <fieldset class="flex flex-wrap gap-3 text-sm"><legend class="mb-2 text-muted-foreground">问题标签（可选）</legend>{#each ["事实错误","机型不符","遗漏诉求","重复失败操作","越权承诺","表达问题"] as tag}<label class="flex items-center gap-1"><input type="checkbox" value={tag} bind:group={tags} />{tag}</label>{/each}</fieldset>
        <label class="block text-sm">修改建议（可选）<textarea class="mt-1 w-full rounded-md border bg-background p-2" rows="2" maxlength="4000" bind:value={note}></textarea></label>
        <label class="block text-sm">直接改写回复（可选）<textarea class="mt-1 w-full rounded-md border bg-background p-2" rows="4" maxlength="6000" bind:value={rewrite}></textarea></label>
        <Button type="submit" disabled={disabled}>保存评分</Button>
      </form>
    </details>
  {/if}
  {#if notice}<p role="status" class="text-sm text-muted-foreground">{notice}</p>{/if}
</section>
