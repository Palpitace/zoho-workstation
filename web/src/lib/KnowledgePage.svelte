<!-- input: 知识导航、Go 来源查询/发布 API 和用户选择的文档。 -->
<!-- output: 分类资料导入、不可变 SOP 修订、版本差异与显式发布/停用/恢复。 -->
<!-- pos: 知识沉淀和人工确认页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount, tick, untrack } from "svelte";
  import { ArrowLeft, Search, Upload } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { dateTime } from "./api";
  import { knowledgeJSON, knowledgeLink, knowledgeStatus, knowledgeScope, knowledgeDiff, type KnowledgeScope, type KnowledgeEntry } from "./knowledge-api";

  let { entryID, blockID = "", token, refreshVersion }: { entryID: string; blockID?: string; token: string; refreshVersion: number } = $props();
  let entries = $state<KnowledgeEntry[]>([]);
  let entry = $state<KnowledgeEntry | null>(null);
  let history = $state<KnowledgeEntry[]>([]);
  let compareID = $state("");
  const compare = $derived(history.find((version) => String(version.id) === compareID));
  const active = $derived(history.find((version) => version.status === "published"));
  const compareText = $derived(compare?.blocks.map((block) => block.text).join("\n\n") || "");
  const currentText = $derived(entry?.blocks.map((block) => block.text).join("\n\n") || "");
  const diff = $derived(compare ? knowledgeDiff(compareText, currentText) : []);
  let query = $state("");
  let appliedQuery = $state("");
  let statusFilter = $state("");
  let loading = $state(true);
  let busy = $state(false);
  let error = $state("");
  let readError = $state("");
  let notice = $state("");
  let editing = $state(false);
  let editTitle = $state("");
  let editProduct = $state("");
  let editText = $state("");
  let editScope = $state<KnowledgeScope>("reference");
  let file = $state<File | null>(null);
  let importTitle = $state("");
  let importProduct = $state("");
  let importReplaces = $state("");
  let importScope = $state<KnowledgeScope>("reference");
  let mounted = false;
  let request: AbortController | undefined;
  let action: AbortController | undefined;
  const blocked = $derived(busy || !token);
  let lastFocusedBlock = "";

  async function load() {
    request?.abort(); request = new AbortController();
    const signal = request.signal;
    loading = !entry && entries.length === 0;
    try {
      if (entryID) {
        const [next, versions] = await Promise.all([
          knowledgeJSON<KnowledgeEntry>(`/api/knowledge/${encodeURIComponent(entryID)}`, signal),
          knowledgeJSON<{ data: KnowledgeEntry[] }>(`/api/knowledge/${encodeURIComponent(entryID)}/history`, signal),
        ]);
        if (!signal.aborted) {
          entry = next; history = versions.data;
          if (!compareID) compareID = String(next.replaces_id || history.find((version) => version.id !== next.id)?.id || "");
        }
      } else {
        const params = new URLSearchParams({ q: appliedQuery, status: statusFilter });
        const next = await knowledgeJSON<{ data: KnowledgeEntry[] }>(`/api/knowledge?${params}`, signal);
        if (!signal.aborted) entries = next.data;
      }
      if (!signal.aborted) { readError = ""; await focusBlock(); }
    } catch (failure) { if (!signal.aborted) readError = failure instanceof Error ? failure.message : "无法读取知识资料。"; }
    finally { if (!signal.aborted) loading = false; }
  }
  async function focusBlock() {
    if (!blockID || !entry || lastFocusedBlock === `${entry.id}:${blockID}`) return;
    const selectedID = entry.id, selectedBlock = blockID;
    await tick();
    if (!mounted || entry?.id !== selectedID || blockID !== selectedBlock) return;
    const target = document.getElementById(`knowledge-block-${blockID}`);
    if (target) { target.focus({ preventScroll: true }); target.scrollIntoView({ block: "start" }); lastFocusedBlock = `${entry.id}:${blockID}`; }
  }
  async function mutate<T = KnowledgeEntry>(path: string, body?: unknown): Promise<T | null> {
    if (blocked) return null;
    busy = true; error = notice = ""; action = new AbortController();
    try {
      const result = await knowledgeJSON<T>(path, action.signal, token, body);
      if (action.signal.aborted) return null;
      return result;
    } catch (failure) { if (!action.signal.aborted) error = failure instanceof Error ? failure.message : "操作未完成，请重试。"; return null; }
    finally { busy = false; }
  }
  async function importDocument(event: SubmitEvent) {
    event.preventDefault(); if (!file) return;
    const form = new FormData(); form.set("file", file); form.set("title", importTitle); form.set("product", importProduct); form.set("scope", importScope);
    if (importReplaces) form.set("replaces_id", importReplaces);
    const saved = await mutate("/api/knowledge/import", form);
    if (saved?.id) window.location.hash = knowledgeLink(saved.id);
  }
  function startEdit() {
    if (!entry) return;
    editTitle = entry.title; editProduct = entry.product; editText = entry.blocks.map((block) => block.text).join("\n\n"); editScope = entry.scope; editing = true;
  }
  async function saveDraft(event: SubmitEvent) {
    event.preventDefault(); if (!entry) return;
    const saved = await mutate("/api/knowledge/draft", { title: editTitle, product: editProduct, text: editText, scope: editScope, source_entry_id: entry.id, replaces_id: entry.id });
    if (saved?.id) { editing = false; window.location.hash = knowledgeLink(saved.id); if (saved.id === entry.id) await load(); }
  }
  async function draftFromCase() {
    if (!entry) return;
    const saved = await mutate("/api/knowledge/draft", { title: `${entry.title}：排查草稿`, product: entry.product, text: "", source_entry_id: entry.id });
    if (saved?.id) window.location.hash = knowledgeLink(saved.id);
  }
  async function restoreVersion() {
    if (!entry) return;
    const saved = await mutate(`/api/knowledge/${entry.id}/restore`);
    if (saved?.id) window.location.hash = knowledgeLink(saved.id);
  }
  async function setStatus(operation: "publish" | "archive") {
    if (!entry) return;
    const done = await mutate<{ status: string }>(`/api/knowledge/${entry.id}/${operation}`);
    if (done) { notice = operation === "publish" ? "已发布，可用于工单建议。" : "已停用，保留来源和历史记录。"; await load(); }
  }
  $effect(() => {
    refreshVersion;
    if (mounted) untrack(() => { void load(); });
  });
  $effect(() => { blockID; untrack(() => { void focusBlock(); }); });
  onMount(() => {
    mounted = true; void load();
    return () => { mounted = false; request?.abort(); action?.abort(); };
  });
</script>

<section class="space-y-6" aria-label="知识库" aria-busy={loading}>
  {#if entryID}<Button href="#/knowledge" variant="ghost" class="-ml-3 h-10"><ArrowLeft size={16} aria-hidden="true" />返回知识库</Button>{/if}
  {#if readError}<p role="alert" class="text-sm break-words text-destructive">{readError}</p>{/if}
  {#if error}<p role="alert" class="text-sm break-words text-destructive">{error}</p>{/if}
  {#if notice}<p role="status" class="text-sm text-muted-foreground">{notice}</p>{/if}
  {#if loading && !entry && !entries.length}<p role="status" class="py-8 text-muted-foreground">正在读取知识资料…</p>{/if}
  {#if !entryID}
    <header class="space-y-2"><h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">知识库</h1><p class="text-sm text-muted-foreground">历史案例自动积累；SOP 经确认发布后用于工单建议。</p></header>
    <details class="rounded-lg border p-4">
      <summary class="cursor-pointer text-sm font-medium">导入文档</summary>
      <form class="mt-5 space-y-4" onsubmit={importDocument}>
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="space-y-2 sm:col-span-2"><label for="knowledge-file" class="text-sm">资料文件</label><Input id="knowledge-file" type="file" accept=".md,.markdown,.txt,.docx,.pdf" required onchange={(event) => { file = event.currentTarget.files?.[0] || null; }} /><p class="text-xs text-muted-foreground">支持 Word（.docx）、文字 PDF、Markdown 和文本。导入后先预览；扫描图片不会自动识别。</p></div>
          <div class="space-y-2"><label for="knowledge-import-title" class="text-sm">标题</label><Input id="knowledge-import-title" bind:value={importTitle} placeholder="留空使用文件名" maxlength={300} /></div>
          <div class="space-y-2"><label for="knowledge-import-product" class="text-sm">适用机型</label><Input id="knowledge-import-product" bind:value={importProduct} placeholder="留空表示通用，发布前请核对" maxlength={160} /></div>
          <div class="space-y-2"><label for="knowledge-import-scope" class="block text-sm">资料分类</label><select id="knowledge-import-scope" bind:value={importScope} class="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"><option value="reference">参考资料（默认）</option><option value="technical">技术知识（可提议修订）</option><option value="policy">售后政策（仅引用正式规则）</option></select></div>
          <div class="space-y-2"><label for="knowledge-import-replaces" class="text-sm">修订的 SOP 版本编号（可选）</label><Input id="knowledge-import-replaces" bind:value={importReplaces} inputmode="numeric" pattern="[0-9]+" placeholder="例如 12" /></div>
        </div>
        <Button type="submit" disabled={blocked || !file} class="h-10"><Upload size={15} aria-hidden="true" />{busy ? "导入中…" : "导入并预览"}</Button>
      </form>
    </details>
    <form class="flex flex-wrap items-end gap-3" onsubmit={(event) => { event.preventDefault(); appliedQuery = query; void load(); }}>
      <div class="min-w-48 flex-1 space-y-2"><label for="knowledge-query" class="text-sm">搜索知识</label><Input id="knowledge-query" bind:value={query} placeholder="标题、机型或正文关键词" /></div>
      <div class="space-y-2"><label for="knowledge-status" class="block text-sm">状态</label><select id="knowledge-status" class="h-9 rounded-md border border-input bg-background px-3 text-sm focus-visible:ring-2 focus-visible:ring-ring" value={statusFilter} onchange={(event) => { statusFilter = event.currentTarget.value; void load(); }}><option value="">全部状态</option><option value="draft">待确认</option><option value="published">可用于建议</option><option value="archived">已停用</option></select></div>
      <Button type="submit" variant="outline" class="h-9"><Search size={15} aria-hidden="true" />查询</Button>
    </form>
    <div class="divide-y rounded-lg border">
      {#each entries as item (item.id)}
        <article class="space-y-2 p-4 sm:p-5">
          <div class="flex flex-wrap items-start justify-between gap-3"><a href={knowledgeLink(item.id)} class="min-w-0 text-sm leading-6 font-medium break-words underline-offset-4 hover:underline">{item.title}</a><Badge variant={item.status === "published" ? "secondary" : "outline"}>{knowledgeStatus(item)}</Badge></div>
          <p class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"><span>{item.kind === "case" ? "历史工单案例" : knowledgeScope(item.scope)}</span><span>{item.product || "通用"}</span><span>版本 #{item.id}</span><span>{dateTime(item.created_at)}</span></p>
        </article>
      {:else}{#if !loading}<p class="p-6 text-sm text-muted-foreground">{appliedQuery || statusFilter ? "没有符合条件的资料。" : "尚无知识资料。可导入 SOP；已同步工单总结完成后会自动积累案例。"}</p>{/if}{/each}
    </div>
  {:else if entry}
    <header class="space-y-3">
      <div class="flex flex-wrap items-center gap-2"><Badge variant="outline">{entry.kind === "case" ? "历史案例" : "SOP"}</Badge><Badge variant={entry.status === "published" ? "secondary" : "outline"}>{knowledgeStatus(entry)}</Badge><span class="text-xs text-muted-foreground">版本 #{entry.id} · {knowledgeScope(entry.scope)}</span></div>
      <h1 id="page-title" tabindex="-1" class="text-xl leading-relaxed font-semibold break-words">{entry.title}</h1>
      <dl class="flex flex-wrap gap-x-6 gap-y-2 text-sm"><div><dt class="inline text-muted-foreground">适用机型：</dt><dd class="inline">{entry.product || "通用 / 未指定"}</dd></div><div><dt class="inline text-muted-foreground">来源：</dt><dd class="inline break-words">{entry.source_name || "本地草稿"}</dd></div><div><dt class="inline text-muted-foreground">保存：</dt><dd class="inline">{dateTime(entry.created_at)}</dd></div></dl>
      {#if entry.replaces_id}<p class="text-sm text-muted-foreground">修订自 <a class="underline underline-offset-4" href={knowledgeLink(entry.replaces_id)}>版本 #{entry.replaces_id}</a></p>{/if}
      {#if entry.lab_batch_id}<p class="text-sm text-muted-foreground">来自 <a class="underline underline-offset-4" href={`#/lab/${entry.lab_batch_id}`}>离线批次 #{entry.lab_batch_id}</a>，发布前请核对批次对照结果。</p>{/if}
      {#if entry.source_ticket_id}<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs"><a class="inline-flex min-h-10 items-center underline underline-offset-4" href={`#/tickets/${encodeURIComponent(entry.source_ticket_id)}`}>查看来源工单</a>{#each entry.message_ids as id}<a class="inline-flex min-h-10 items-center break-all text-muted-foreground underline underline-offset-4" href={`#/tickets/${encodeURIComponent(entry.source_ticket_id)}?message=${encodeURIComponent(id)}`}>消息 {id}</a>{/each}</div>{/if}
    </header>
    {#if entry.status === "archived"}<p class="rounded-md border bg-muted/40 p-4 text-sm">此版本已停用，不再用于新的处理建议，历史来源继续保留。</p>{/if}
    {#if entry.kind === "sop" && entry.status === "draft"}<p class="rounded-md border bg-muted/40 p-4 text-sm">请核对适用机型、步骤和注意事项。此草稿尚未用于工单建议，确认发布后生效。</p>{/if}
    <div class="flex flex-wrap gap-2">
      {#if entry.kind === "case" && entry.status !== "archived"}<Button variant="outline" class="h-10" disabled={blocked} onclick={draftFromCase}>整理为 SOP 草稿</Button>{/if}
      {#if entry.kind === "sop" && entry.status !== "archived"}
        <Button variant="outline" class="h-10" disabled={blocked || editing} onclick={startEdit}>编辑为新草稿</Button>
        {#if entry.status === "draft"}<Button class="h-10" disabled={blocked || editing} onclick={() => setStatus("publish")}>确认发布</Button>{/if}
      {/if}
      {#if entry.kind === "sop" && entry.status === "archived" && active && active.id !== entry.id}<Button variant="outline" class="h-10" disabled={blocked} onclick={restoreVersion}>恢复此内容为新草稿</Button>{/if}
      {#if entry.status !== "archived"}<Button variant="ghost" class="h-10" disabled={blocked || editing} onclick={() => setStatus("archive")}>停用此版本</Button>{/if}
    </div>
    {#if editing}
      <form class="space-y-4 rounded-lg border p-4 sm:p-5" onsubmit={saveDraft}>
        <div class="grid gap-4 sm:grid-cols-2"><div class="space-y-2"><label for="draft-title" class="text-sm">标题</label><Input id="draft-title" bind:value={editTitle} required maxlength={300} /></div><div class="space-y-2"><label for="draft-product" class="text-sm">适用机型</label><Input id="draft-product" bind:value={editProduct} maxlength={160} /></div></div>
        <div class="space-y-2"><label for="draft-scope" class="block text-sm">资料分类</label><select id="draft-scope" bind:value={editScope} disabled={entry.scope === "policy" || !!entry.lab_batch_id} class="h-9 rounded-md border border-input bg-background px-3 text-sm"><option value="reference">参考资料</option><option value="technical">技术知识（可提议修订）</option><option value="policy">售后政策（仅引用）</option></select></div>
        <div class="space-y-2"><label for="draft-text" class="text-sm">SOP 草稿内容</label><textarea id="draft-text" bind:value={editText} required rows={15} class="min-h-64 w-full rounded-md border border-input bg-background px-3 py-2 text-sm leading-7 focus-visible:ring-2 focus-visible:ring-ring"></textarea></div>
        <p class="text-xs text-muted-foreground">保存会创建新版本，当前已发布内容保持有效；新草稿仍需确认发布。</p>
        <div class="flex flex-wrap gap-2"><Button type="submit" disabled={blocked}>保存新草稿</Button><Button variant="ghost" disabled={busy} onclick={() => { editing = false; }}>取消编辑</Button></div>
      </form>
    {/if}
    {#if entry.kind === "sop" && history.length > 1}
      <details class="rounded-lg border p-4">
        <summary class="cursor-pointer text-sm font-medium">版本历史与差异（{history.length} 版）</summary>
        <div class="mt-4 space-y-5">
          <ol class="space-y-2 text-sm">{#each history as version (version.id)}<li class="flex flex-wrap items-center gap-x-3 gap-y-1"><a href={knowledgeLink(version.id)} aria-current={version.id === entry.id ? "page" : undefined} class="underline underline-offset-4">版本 #{version.id}</a><span class="text-muted-foreground">{knowledgeStatus(version)} · {dateTime(version.created_at)}</span></li>{/each}</ol>
          <div class="space-y-2"><label for="knowledge-compare" class="block text-sm">与当前查看版本 #{entry.id} 对照</label><select id="knowledge-compare" bind:value={compareID} class="h-9 max-w-full rounded-md border border-input bg-background px-3 text-sm"><option value="">选择对照版本</option>{#each history.filter((version) => version.id !== entry?.id) as version}<option value={String(version.id)}>版本 #{version.id} · {knowledgeStatus(version)}</option>{/each}</select></div>
          {#if compare}
            <p class="text-xs text-muted-foreground">对照 #{compare.id} → 当前查看 #{entry.id}。− 表示移除，+ 表示加入；这里只展示差异，不会发布或修改内容。</p>
            {#if compare.title !== entry.title || compare.product !== entry.product || compare.scope !== entry.scope}<dl class="space-y-2 text-sm"><div><dt class="text-muted-foreground">标题</dt><dd class="break-words">{compare.title} → {entry.title}</dd></div><div><dt class="text-muted-foreground">机型 / 分类</dt><dd>{compare.product || "通用"} / {knowledgeScope(compare.scope)} → {entry.product || "通用"} / {knowledgeScope(entry.scope)}</dd></div></dl>{/if}
            {#if diff}
              <div class="rounded-md border p-3 text-sm leading-7" aria-label="正文差异">{#each diff as line}<p class={`whitespace-pre-wrap break-words ${line.kind === "added" ? "bg-emerald-50 dark:bg-emerald-950/30" : line.kind === "removed" ? "bg-rose-50 dark:bg-rose-950/30" : "text-muted-foreground"}`}><span class="mr-2 inline-block w-3 select-none" aria-label={line.kind === "added" ? "加入" : line.kind === "removed" ? "移除" : "相同"}>{line.kind === "added" ? "+" : line.kind === "removed" ? "−" : " "}</span>{line.text || " "}</p>{/each}</div>
            {:else}
              <p class="text-xs text-muted-foreground">正文较长，显示完整并排文本供核对。</p><div class="grid gap-4 md:grid-cols-2"><section class="min-w-0 rounded-md border p-3"><h3 class="mb-3 text-sm font-medium">对照版本 #{compare.id}</h3><p class="text-sm leading-7 whitespace-pre-wrap break-words">{compareText}</p></section><section class="min-w-0 rounded-md border p-3"><h3 class="mb-3 text-sm font-medium">当前查看 #{entry.id}</h3><p class="text-sm leading-7 whitespace-pre-wrap break-words">{currentText}</p></section></div>
            {/if}
          {/if}
        </div>
      </details>
    {/if}
    <div class="divide-y border-y">
      {#each entry.blocks as block (block.id)}
        <section id={`knowledge-block-${block.id}`} tabindex="-1" class="scroll-mt-5 space-y-3 py-5 focus:outline-2 focus:outline-ring focus:-outline-offset-2">
          <h2 class="text-xs text-muted-foreground">{block.id}</h2>
          <p class="text-sm leading-7 whitespace-pre-wrap break-words">{block.text}</p>
        </section>
      {/each}
    </div>
  {/if}
</section>
