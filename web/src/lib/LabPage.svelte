<!-- input: 固定批次与候选知识 API、用户选单和逐版本审核。 -->
<!-- output: 手动批次、离线回复/评估、人工反馈及 FAQ 修订对比。 -->
<!-- pos: 第一阶段离线实验主页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import LabReply from "./LabReply.svelte";
  import { dateTime } from "./api";
  import type { KnowledgeEntry } from "./knowledge-api";
  import { labJSON, labStatus, phaseName, splitName, type LabBatch, type LabCandidate, type LabSelection, type LabFeedback, type LabEvaluation } from "./lab-api";
  let { batchID, token, refreshVersion }: { batchID: string; token: string; refreshVersion: number } = $props();
  let batches = $state<LabBatch[]>([]), batch = $state<LabBatch | null>(null), candidates = $state<LabCandidate[]>([]);
  let selection = $state<LabSelection[]>([]), knowledge = $state<KnowledgeEntry[]>([]), selected = $state<string[]>([]), knowledgeIDs = $state<number[]>([]);
  let groups = $state<Record<string,string>>({});
  let from = $state("2026-11-05"), until = $state(""), preparedFrom = $state(""), preparedUntil = $state("");
  let picking = $state(false), busy = $state(false), loading = $state(true), error = $state(""), notice = $state("");
  let validationComplete = $state(false);
  let sampleID = $state(""), nodeID = $state("");
  let mounted = false, read: AbortController | undefined, action: AbortController | undefined;
  const blocked = $derived(busy || !token);
  const sample = $derived(batch?.spec.samples.find((s) => s.ticket_id === sampleID));
  const node = $derived(sample?.nodes.find((n) => n.id === nodeID));
  const calls = $derived(batch?.state.calls || []);
  const replies = $derived(calls.filter((c) => c.node_id === nodeID && c.task === "reply"));
  const evaluations = $derived(calls.filter((c) => c.task === "evaluate" && c.status === "success"));
  const failed = $derived(calls.filter((c) => c.status === "failed" || c.status === "interrupted").length);
  const held = $derived(batch?.spec.samples.filter((s) => s.split === "holdout").flatMap((s) => s.nodes) || []);
  function grade(id: string, variant: string) { return evaluations.find((c) => c.node_id === id && c.variant === variant)?.result as LabEvaluation | undefined; }
  async function load() {
    read?.abort(); read = new AbortController(); const signal = read.signal;
    try {
      if (batchID) {
        const next = await labJSON<{ batch?: LabBatch; candidates: LabCandidate[]; unchanged?: boolean; validation_complete: boolean }>(`/api/lab/batches/${encodeURIComponent(batchID)}${batch ? `?revision=${batch.revision}` : ""}`, signal);
        if (!signal.aborted && next.batch) { batch = next.batch; validationComplete=next.validation_complete; candidates = next.candidates; if (!sampleID) chooseSample(next.batch.spec.samples.find((s) => s.split === "development")?.ticket_id || next.batch.spec.samples[0]?.ticket_id || ""); }
      } else { const next = await labJSON<{ data: LabBatch[] }>("/api/lab/batches", signal); if (!signal.aborted) batches = next.data; }
    } catch (e) { if (!signal.aborted) error = e instanceof Error ? e.message : "无法读取离线批次"; }
    finally { if (!signal.aborted) loading = false; }
  }
  function chooseSample(id: string) { sampleID = id; nodeID = batch?.spec.samples.find((s) => s.ticket_id === id)?.nodes[0]?.id || ""; }
  async function mutate<T>(path: string, body?: unknown): Promise<T | null> {
    if (blocked) return null; busy = true; error = notice = ""; action = new AbortController();
    try { return await labJSON<T>(path, action.signal, token, body); }
    catch (e) { if (!action.signal.aborted) error = e instanceof Error ? e.message : "操作未完成"; return null; }
    finally { busy = false; }
  }
  async function prepare(event: SubmitEvent) {
    event.preventDefault(); busy = true; error = ""; read?.abort(); read = new AbortController(); const signal = read.signal;
    const params = new URLSearchParams(); if (from) params.set("from", `${from}T00:00:00+08:00`);
    if (until) { const end = new Date(`${until}T00:00:00+08:00`); end.setUTCDate(end.getUTCDate() + 1); params.set("until", end.toISOString()); }
    try {
      const [tickets, docs] = await Promise.all([labJSON<{ data: LabSelection[]; from: string; until: string }>(`/api/lab/candidates?${params}`, signal), labJSON<{ data: KnowledgeEntry[] }>("/api/knowledge?status=published", signal)]);
      if (signal.aborted) return;
      selection = tickets.data; preparedFrom = tickets.from; preparedUntil = tickets.until;
      selected = selection.filter((s) => !s.reason).map((s) => s.id); knowledge = docs.data.filter((d) => d.kind === "sop"); knowledgeIDs = knowledge.filter((d) => !d.source_ticket_id && !d.source_tickets?.length).map((d) => d.id); picking = true;
    } catch (e) { if (!signal.aborted) error = e instanceof Error ? e.message : "无法读取候选工单"; }
    finally { busy = false; }
  }
  async function create(parentID?: number) {
    const saved = await mutate<LabBatch>("/api/lab/batches", parentID ? { parent_id: parentID } : { ticket_ids: selected, knowledge_ids: knowledgeIDs, groups:Object.fromEntries(Object.entries(groups).filter(([id])=>selected.includes(id))), from: preparedFrom, until: preparedUntil });
    if (saved) window.location.hash = `#/lab/${saved.id}`;
  }
  async function run(action: "start" | "pause") { if (!batch) return; if (await mutate(`/api/lab/batches/${batch.id}/${action}`)) { notice = action === "start" ? "批次已启动，完成后在此审核。" : "已暂停，正在进行的单次调用仍会保存结果。"; await load(); } }
  async function rate(value: Pick<LabFeedback,"call_id"|"score"|"tags"|"note"|"edited_reply">) { if (!batch) return false; const saved = await mutate(`/api/lab/batches/${batch.id}/feedback`, value); if (saved) await load(); return !!saved; }
  async function draft(key: string) { if (!batch) return; const saved = await mutate<KnowledgeEntry>(`/api/lab/batches/${batch.id}/drafts`, { key }); if (saved) { notice = "已保存为知识库草稿，审核并发布后才生效。"; await load(); } }
  $effect(() => { refreshVersion; if (mounted && !busy && !picking) untrack(() => void load()); });
  onMount(() => { mounted = true; void load(); return () => { mounted = false; read?.abort(); action?.abort(); }; });
</script>

<div class="space-y-6">
  <div class="flex flex-wrap items-start justify-between gap-3">
    <div><h1 class="text-2xl font-semibold">{batchID ? `离线批次 #${batchID}` : "离线实验"}</h1><details class="mt-2 text-sm text-muted-foreground"><summary class="cursor-pointer">使用帮助</summary><div class="mt-3 space-y-2"><p>用历史工单模拟回复，评估后改进 FAQ，不发送给客户。政策和参考资料只读，技术知识可提出修订。</p><p>相同首封来信固定分组，约 60% 学习、20% 开发、20% 留出；至少 5 组。关联工单填写同一关联组。</p><p>每个开发节点最多改写 2 次，失败单步最多重试 1 次；完成后仍需检查失败记录。留出评分不用于下一轮优化。</p></div></details></div>
    {#if batchID}<Button href="#/lab" variant="outline">返回批次列表</Button>{/if}
  </div>
  {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
  {#if notice}<p role="status" class="text-sm text-muted-foreground">{notice}</p>{/if}
  {#if loading}<p class="text-sm text-muted-foreground">正在读取…</p>{/if}
  {#if !batchID}
    <form onsubmit={prepare} class="flex flex-wrap items-end gap-3 border-b pb-5">
      <label class="text-sm">创建时间起点（UTC+8）<Input type="date" class="mt-1 w-44" bind:value={from} required /></label>
      <label class="text-sm">结束日期（可选）<Input type="date" class="mt-1 w-44" bind:value={until} min={from} /></label>
      <Button type="submit" disabled={blocked}>选择本地历史工单</Button>
      <span class="text-xs text-muted-foreground">最近最多 50 张；需要更多数据可先同步工单。</span>
    </form>
    {#if picking}
      <section class="space-y-4">
        <h2 class="font-semibold">本批输入 · 已选 {selected.length} 张</h2><p class="text-xs text-muted-foreground">已加载范围：{dateTime(preparedFrom)} 至 {dateTime(preparedUntil)}。修改日期后请重新选择工单。</p>
        <div class="max-h-80 overflow-y-auto rounded-md border divide-y">
          {#each selection as row}<div class="flex flex-wrap items-center gap-3 p-3 text-sm"><label class="flex min-w-0 flex-1 items-start gap-3"><input type="checkbox" class="mt-1" disabled={!!row.reason} value={row.id} bind:group={selected} /><span class="min-w-0 break-words">{row.subject}<span class="mt-1 block text-xs text-muted-foreground">工单 {row.ticket_number ? `#${row.ticket_number}` : row.id} · {dateTime(row.created_time)} · {row.reason || `${row.nodes} 个模拟节点`}</span></span></label>{#if !row.reason}<Input aria-label={`工单 ${row.ticket_number || row.id} 关联组`} placeholder="关联组（可选）" class="w-36" maxlength={40} bind:value={groups[row.id]} />{/if}</div>{/each}
          {#if !selection.length}<p class="p-4 text-sm text-muted-foreground">此范围没有已完整同步的工单。</p>{/if}
        </div>
        <details open><summary class="cursor-pointer text-sm font-medium">固定知识版本 · 已选 {knowledgeIDs.length} 份</summary><div class="space-y-2">{#each knowledge as entry}<label class="flex gap-2 text-sm"><input type="checkbox" value={entry.id} bind:group={knowledgeIDs} />{entry.title} · 版本 #{entry.id} · {entry.scope === "technical" ? "技术知识" : entry.scope === "policy" ? "售后政策" : "只读参考"}</label>{/each}</div></details>

        <Button disabled={blocked || selected.length < 5} onclick={() => create()}>保存批次并预览</Button>
        <Button variant="ghost" onclick={() => { picking = false; void load(); }}>取消选择</Button>
      </section>
    {/if}
    <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="border-b text-muted-foreground"><tr><th class="p-3 font-medium">批次</th><th class="p-3 font-medium">工单</th><th class="p-3 font-medium">状态</th><th class="p-3 font-medium">调用尝试</th></tr></thead><tbody>{#each batches as b}<tr class="border-b"><td class="p-3"><a class="font-medium underline underline-offset-4" href={`#/lab/${b.id}`}>#{b.id} · {dateTime(b.created_at)}</a></td><td class="p-3 tabular-nums">{b.spec.samples.length}</td><td class="p-3">{labStatus(b.state.status)} · {phaseName(b.state.phase)}</td><td class="p-3 tabular-nums">{b.state.calls.length}</td></tr>{/each}</tbody></table></div>
    {#if !batches.length && !loading}<p class="text-sm text-muted-foreground">暂无批次。先选择已同步的历史工单和已发布知识，再启动模拟。</p>{/if}
  {:else if batch}
    <section class="space-y-4 border-b pb-5">
      <div class="flex flex-wrap items-center gap-4 text-sm"><strong>{labStatus(batch.state.status)}</strong><span>{phaseName(batch.state.phase)}</span><span>{batch.spec.samples.length} 张工单</span>{#each ["learn","development","holdout"] as split}<span>{splitName(split)} {batch.spec.samples.filter((s) => s.split === split).length}</span>{/each}<span>调用尝试 {calls.length} · 失败/中断 {failed}</span></div>
      {#if batch.state.error}<p class="text-sm text-destructive">{batch.state.error}</p>{/if}
      <div class="flex flex-wrap gap-2">
        {#if ["draft","paused"].includes(batch.state.status)}<Button disabled={blocked} onclick={() => run("start")}>{batch.state.status === "draft" ? "启动本批模拟" : "继续批次"}</Button>{/if}
        {#if batch.state.status === "running"}<Button variant="outline" disabled={blocked} onclick={() => run("pause")}>暂停后续调用</Button>{/if}
        {#if ["completed","paused"].includes(batch.state.status)}<Button disabled={blocked} onclick={() => create(batch!.id)}>带入开发反馈，创建下一轮</Button>{/if}
      </div>

      <details class="text-xs text-muted-foreground"><summary class="cursor-pointer">固定输入与版本</summary><p class="mt-2">{dateTime(batch.spec.start_time)} 至 {dateTime(batch.spec.end_time)} · {batch.spec.model} · {batch.spec.schema}{batch.spec.parent_id ? ` · 上一轮 #${batch.spec.parent_id}` : ""}</p><p class="mt-1">知识版本：{batch.spec.knowledge.map((k) => `${k.title} #${k.id}`).join("、") || "未选择"}</p></details>
    </section>
    <section class="space-y-4">
      <h2 class="font-semibold">回复审核</h2>
      <div class="flex flex-wrap gap-3"><label class="min-w-0 max-w-full text-sm">工单<select class="mt-1 block w-full max-w-xl rounded-md border bg-background p-2" value={sampleID} onchange={(e) => chooseSample(e.currentTarget.value)}>{#each batch.spec.samples as s}<option value={s.ticket_id}>{splitName(s.split)} · {s.ticket_number ? `#${s.ticket_number}` : s.ticket_id} · {s.subject.slice(0,70)}</option>{/each}</select></label>{#if sample && sample.split !== "learn"}<label class="text-sm">模拟节点<select class="mt-1 block rounded-md border bg-background p-2" bind:value={nodeID}>{#each sample.nodes as n}<option value={n.id}>{n.label} · {dateTime(n.cutoff_time)}</option>{/each}</select></label>{/if}</div>
      {#if sample?.split === "learn"}<p class="text-sm text-muted-foreground">学习工单只提炼技术候选，结果见下方 FAQ 修订。</p>{:else if node}
        <details class="text-sm" open><summary class="cursor-pointer font-medium">当时可见的会话 · {node.ticket.messages.length} 条</summary><div class="mt-3 space-y-3">{#each node.ticket.messages as m}<div class="rounded-md bg-muted/35 p-3"><p class="mb-2 text-xs text-muted-foreground">{m.role === "customer" ? "客户" : m.role === "agent" ? "客服" : "作者未知"} · {dateTime(m.created_time)} · {m.id}</p><p class="whitespace-pre-wrap break-words leading-6">{m.body}</p></div>{/each}</div></details>
        {#each replies as reply (reply.id)}<LabReply call={reply} evaluation={calls.find((c) => c.key === reply.key.replace(/:reply$/,":evaluate") && c.status === "success") || calls.find((c) => c.key === reply.key.replace(/:reply$/,":evaluate"))} feedback={batch.state.feedback} disabled={blocked} onRate={rate} />{/each}
        {#if !replies.length}<p class="text-sm text-muted-foreground">此节点还没有模拟回复。</p>{/if}
      {/if}
      {#if sample}<details class="text-sm"><summary class="cursor-pointer text-muted-foreground">完整历史会话，仅供人工事后对照</summary><p class="mt-2 text-xs text-muted-foreground">后续真实回复并非对模拟回复的反馈，不能据此认定模拟方案已经解决问题。</p>{#each sample.snapshot.messages as m}<div class="mt-3 border-t pt-3"><p class="text-xs text-muted-foreground">{m.role} · {dateTime(m.created_time)} · {m.id}</p><p class="mt-1 whitespace-pre-wrap break-words leading-6">{m.body}</p></div>{/each}</details>{/if}
    </section>
    <section class="space-y-3 border-t pt-5"><h2 class="font-semibold">留出对比</h2><p class="text-xs text-muted-foreground">同一节点分别使用原 FAQ 和候选 FAQ，单独评估。分数来自模型，不代表人工验收或真实解决率。</p><div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead class="border-b"><tr><th class="p-2 font-medium">节点</th><th class="p-2 font-medium">原 FAQ</th><th class="p-2 font-medium">候选 FAQ</th></tr></thead><tbody>{#each held as n}<tr class="border-b"><td class="p-2"><button class="text-left underline underline-offset-4" onclick={() => { const s = batch!.spec.samples.find((s) => s.nodes.some((node) => node.id === n.id)); if (s) { chooseSample(s.ticket_id); nodeID = n.id; } }}>{n.label} · {n.id}</button></td>{#each ["baseline","candidate"] as variant}{@const score = grade(n.id,variant)}<td class="p-2">{score ? `${score.score}/5 · ${score.findings.filter((f) => f.severity === "blocker").length} 项需处理` : "未完成"}</td>{/each}</tr>{/each}</tbody></table></div></section>
    <section class="space-y-4 border-t pt-5"><h2 class="font-semibold">FAQ 修订候选 · {candidates.length}</h2><p class="text-xs text-muted-foreground">候选只在本批实验中使用。全部留出对比成功返回后可保存为知识库草稿，逐项确认发布。</p>
      {#each candidates as candidate}{@const p = candidate.proposal}{@const original = batch.spec.knowledge.find((k) => k.id === p.replaces_id)}<details class="border-b pb-4"><summary class="cursor-pointer text-sm font-medium">{p.title}{candidate.active ? "" : " · 已被后续修订取代"} · {p.replaces_id ? `修订 #${p.replaces_id}` : "新增"}</summary><div class="mt-3 grid gap-4 lg:grid-cols-2"><div><p class="mb-2 text-xs text-muted-foreground">原版本</p><p class="whitespace-pre-wrap break-words text-sm leading-6">{original?.blocks.map((b) => b.text).join("\n\n") || "新增条目"}</p></div><div><p class="mb-2 text-xs text-muted-foreground">候选版本 · {p.product || "未限定产品"}</p><p class="whitespace-pre-wrap break-words text-sm leading-6">{candidate.entry.blocks.map((b) => b.text).join("\n\n")}</p></div></div><p class="my-3 text-xs text-muted-foreground">来源工单 {candidate.ticket_id} · 消息 {p.message_ids.join("、")}</p>{#if batch.state.drafts[candidate.key]}<Button href={`#/knowledge/${batch.state.drafts[candidate.key]}`} variant="outline">查看草稿并审核</Button>{:else}<Button variant="outline" disabled={blocked || !validationComplete || !candidate.active} onclick={() => draft(candidate.key)}>保存为修订草稿</Button>{/if}</details>{/each}
      {#if !candidates.length}<p class="text-sm text-muted-foreground">暂无候选；未发现可靠技术知识时可以不生成条目。</p>{/if}
    </section>
    {#if failed}<details class="text-sm"><summary class="cursor-pointer text-destructive">失败与中断记录 · {failed}</summary>{#each calls.filter((c) => c.status === "failed" || c.status === "interrupted") as call}<p class="mt-2 break-words">#{call.id} · {call.task} · {call.node_id} · {call.error || labStatus(call.status)}</p>{/each}</details>{/if}
  {/if}
</div>
