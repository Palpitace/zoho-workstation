<!-- input: 知识导航、Go 来源查询/发布 API 和用户选择的文档。 -->
<!-- output: 具名分类导航、Markdown 编辑与版本审核、批量导入及本地检索。 -->
<!-- pos: 知识沉淀和人工确认页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount, tick, untrack } from "svelte";
  import { ArrowLeft, Search } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import KnowledgeImport from "./KnowledgeImport.svelte";
  import KnowledgeRetrieval from "./KnowledgeRetrieval.svelte";
  import MarkdownEditor from "./MarkdownEditor.svelte";
  import MarkdownView from "./MarkdownView.svelte";
  import { dateTime } from "./api";
  import { knowledgeJSON, knowledgeLink, knowledgeStatus, knowledgeScope, knowledgeDiff, knowledgeMarkdown, knowledgeReviewLabel, type KnowledgeLibrary, type KnowledgeCategory, type KnowledgeScope, type KnowledgeEntry } from "./knowledge-api";

  let { entryID, blockID = "", token, refreshVersion }: { entryID: string; blockID?: string; token: string; refreshVersion: number } = $props();
  let libraries = $state<KnowledgeLibrary[]>([]);
  let categories = $state<KnowledgeCategory[]>([]);
  let libraryID = $state(0);
  let categoryID = $state(0);
  let libraryName = $state("");
  let departmentIDs = $state("");
  let defaultDepartment = $state("");
  let categoryName = $state("");
  let categoryEditID = $state(0);
  let editCategory = $state(0);
  let editApplicable = $state("");
  let editExcluded = $state("");
  const library = $derived(libraries.find((item) => item.id === libraryID));
  let entries = $state<KnowledgeEntry[]>([]);
  let entry = $state<KnowledgeEntry | null>(null);
  let history = $state<KnowledgeEntry[]>([]);
  let compareID = $state("");
  const compare = $derived(history.find((version) => String(version.id) === compareID));
  const active = $derived(history.find((version) => version.status === "published"));
  const compareText = $derived(compare ? knowledgeMarkdown(compare) : "");
  const currentText = $derived(entry ? knowledgeMarkdown(entry) : "");
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
  let editText = $state("");
  let editScope = $state<KnowledgeScope>("reference");
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
      const catalog = await knowledgeJSON<{ data: KnowledgeLibrary[]; default_department_id?: string }>("/api/knowledge/libraries", signal);
      if (signal.aborted) return;
      libraries = catalog.data; defaultDepartment = catalog.default_department_id || "";
      if (!libraryID) libraryID = libraries[0]?.id || 0;
      if (entryID) {
        const [next, versions] = await Promise.all([
          knowledgeJSON<KnowledgeEntry>(`/api/knowledge/${encodeURIComponent(entryID)}`, signal),
          knowledgeJSON<{ data: KnowledgeEntry[] }>(`/api/knowledge/${encodeURIComponent(entryID)}/history`, signal),
        ]);
        if (!signal.aborted) {
          entry = next; history = versions.data; libraryID = next.library_id || libraryID;
          if (!compareID) compareID = String(next.replaces_id || history.find((version) => version.id !== next.id)?.id || "");
        }
      } else {
        const params = new URLSearchParams({ q: appliedQuery, status: statusFilter, library_id: String(libraryID), category_id: String(categoryID) });
        const next = await knowledgeJSON<{ data: KnowledgeEntry[] }>(`/api/knowledge?${params}`, signal);
        if (!signal.aborted) entries = next.data;
      }
      if (!signal.aborted && libraryID) {
        const result = await knowledgeJSON<{ data: KnowledgeCategory[] }>(`/api/knowledge/categories?library_id=${libraryID}`, signal);
        if (!signal.aborted) categories = result.data;
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
  async function saveLibrary(newLibrary: boolean) {
    const saved = await mutate<KnowledgeLibrary>("/api/knowledge/libraries", { id: newLibrary ? 0 : libraryID, name: libraryName, department_ids: departmentIDs.split(/[,，\n]/).map((v) => v.trim()).filter(Boolean) });
    if (saved) { libraryID = saved.id; categoryID = 0; libraryName = ""; departmentIDs = ""; await load(); }
  }
  async function saveCategory() {
    const saved = await mutate<KnowledgeCategory>("/api/knowledge/categories", { id: categoryEditID, library_id: libraryID, name: categoryName });
    if (saved) { categoryName = ""; categoryEditID = 0; await load(); }
  }
  async function moveCategory(category_id: number) {
    if (!entry) return;
    if (await mutate(`/api/knowledge/${entry.id}/move`, { category_id })) await load();
  }
  function startEdit() {
    if (!entry) return;
    editTitle = entry.title; editText = knowledgeMarkdown(entry); editScope = entry.scope; editCategory = entry.category_id || 0; editApplicable = entry.applicable_products?.length ? entry.applicable_products.join(", ") : entry.product; editExcluded = (entry.excluded_products || []).join(", "); editing = true;
  }
  async function saveDraft(event: SubmitEvent) {
    event.preventDefault(); if (!entry) return;
    const models = editApplicable.split(/[,，\n]/).map((v) => v.trim()).filter(Boolean);
    const saved = await mutate("/api/knowledge/draft", { title: editTitle, product: models.length === 1 ? models[0] : "", markdown: editText, scope: editScope, library_id: entry.library_id || libraryID, category_id: editCategory, applicable_products: models, excluded_products: editExcluded.split(/[,，\n]/).map((v) => v.trim()).filter(Boolean), source_entry_id: entry.id, replaces_id: entry.id });
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
    <header class="flex flex-wrap items-center justify-between gap-3"><h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">知识库</h1><details class="max-w-xl text-sm"><summary class="cursor-pointer text-muted-foreground">使用帮助</summary><div class="mt-3 space-y-2 text-muted-foreground"><p>资料导入后集中审核。保存产生新草稿，确认发布后才供工单检索；历史版本与引用保留。</p><p>技术知识可提议修订，政策只读引用。图片随资料保存；转换失败或缺少图示的内容需要核对。</p><p>语义模型在本机运行。首次下载后可离线检索；生成回复仍使用已配置的模型服务。</p></div></details></header>
    <div class="flex flex-wrap items-center gap-3">
      <label class="flex min-w-0 items-center gap-2 text-sm">知识库<select aria-label="知识库" value={libraryID} onchange={(event) => { libraryID = Number(event.currentTarget.value); categoryID = 0; void load(); }} class="h-10 max-w-full rounded-md border bg-background px-3">{#each libraries as item}<option value={item.id}>{item.name}</option>{/each}</select></label>
      <label class="flex min-w-0 items-center gap-2 text-sm">分类<select aria-label="知识分类" value={categoryID} onchange={(event) => { categoryID = Number(event.currentTarget.value); void load(); }} class="h-10 max-w-full rounded-md border bg-background px-3"><option value={0}>全部分类</option>{#each categories as item}<option value={item.id}>{item.name}</option>{/each}</select></label>
      <details class="text-sm"><summary class="cursor-pointer text-muted-foreground">管理知识库与分类</summary><div class="mt-4 space-y-4 rounded-lg border p-4">
        <div class="flex flex-wrap gap-2"><Button variant="outline" size="sm" onclick={() => { libraryName = ""; departmentIDs = defaultDepartment; }}>新建</Button><Button variant="outline" size="sm" disabled={!library} onclick={() => { libraryName = library!.name; departmentIDs = library!.department_ids.join(", "); }}>编辑当前知识库</Button></div>
        <label class="block space-y-1">知识库名称<Input bind:value={libraryName} maxlength={120} /></label><label class="block space-y-1">关联部门 ID（逗号分隔）<Input bind:value={departmentIDs} placeholder={defaultDepartment} /></label>
        <div class="flex flex-wrap gap-2"><Button size="sm" disabled={blocked || !libraryName.trim() || !departmentIDs.trim()} onclick={() => saveLibrary(true)}>创建知识库</Button><Button size="sm" variant="outline" disabled={blocked || !libraryID || !libraryName.trim() || !departmentIDs.trim()} onclick={() => saveLibrary(false)}>更新当前知识库</Button></div>
        <div class="border-t pt-4"><label class="block space-y-1">分类<select value={categoryEditID} onchange={(event) => { categoryEditID = Number(event.currentTarget.value); categoryName = categories.find((item) => item.id === categoryEditID)?.name || ""; }} class="h-9 w-full rounded-md border bg-background px-3"><option value={0}>新增分类</option>{#each categories as item}<option value={item.id}>{item.name}</option>{/each}</select></label><div class="mt-2 flex gap-2"><Input aria-label="分类名称" bind:value={categoryName} maxlength={120} /><Button size="sm" disabled={blocked || !libraryID || !categoryName.trim()} onclick={saveCategory}>{categoryEditID ? "重命名" : "新增"}</Button></div></div>
      </div></details>
    </div>
    {#key libraryID}<KnowledgeImport {libraryID} {token} oncomplete={() => { void load(); }} />{/key}
    {#key libraryID}<KnowledgeRetrieval {libraryID} departments={library?.department_ids || []} {token} />{/key}
    <form class="flex flex-wrap items-end gap-3" onsubmit={(event) => { event.preventDefault(); appliedQuery = query; void load(); }}>
      <div class="min-w-48 flex-1 space-y-2"><label for="knowledge-query" class="text-sm">搜索知识</label><Input id="knowledge-query" bind:value={query} placeholder="标题、机型或正文关键词" /></div>
      <div class="space-y-2"><label for="knowledge-status" class="block text-sm">状态</label><select id="knowledge-status" class="h-9 rounded-md border border-input bg-background px-3 text-sm focus-visible:ring-2 focus-visible:ring-ring" value={statusFilter} onchange={(event) => { statusFilter = event.currentTarget.value; void load(); }}><option value="">全部状态</option><option value="draft">待确认</option><option value="published">可用于建议</option><option value="archived">已停用</option></select></div>
      <Button type="submit" variant="outline" class="h-9"><Search size={15} aria-hidden="true" />查询</Button>
    </form>
    <div class="divide-y rounded-lg border">
      {#each entries as item (item.id)}
        <article class="space-y-2 p-4 sm:p-5">
          <div class="flex flex-wrap items-start justify-between gap-3"><a href={knowledgeLink(item.id)} class="min-w-0 text-sm leading-6 font-medium break-words underline-offset-4 hover:underline">{item.title}</a><Badge variant={item.status === "published" ? "secondary" : "outline"}>{knowledgeStatus(item)}</Badge></div>
          <p class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"><span>{item.kind === "case" ? "历史工单案例" : knowledgeScope(item.scope)}</span><span>{categories.find((category) => category.id === item.category_id)?.name || "未分类"}</span><span>{item.applicable_products?.join("、") || item.product || "通用"}</span><span>版本 #{item.id}</span><span>{dateTime(item.created_at)}</span></p>
        </article>
      {:else}{#if !loading}<p class="p-6 text-sm text-muted-foreground">{appliedQuery || statusFilter ? "没有符合条件的资料。" : "暂无资料，导入 ZIP 或多个文件开始整理。"}</p>{/if}{/each}
    </div>
  {:else if entry}
    <header class="space-y-3">
      <div class="flex flex-wrap items-center gap-2"><Badge variant="outline">{entry.kind === "case" ? "历史案例" : "SOP"}</Badge><Badge variant={entry.status === "published" ? "secondary" : "outline"}>{knowledgeStatus(entry)}</Badge><span class="text-xs text-muted-foreground">版本 #{entry.id} · {knowledgeScope(entry.scope)}</span></div>
      <h1 id="page-title" tabindex="-1" class="text-xl leading-relaxed font-semibold break-words">{entry.title}</h1>
      <dl class="flex flex-wrap gap-x-6 gap-y-2 text-sm"><div><dt class="inline text-muted-foreground">适用机型：</dt><dd class="inline">{entry.applicable_products?.join("、") || entry.product || "通用 / 未指定"}</dd></div><div><dt class="inline text-muted-foreground">来源：</dt><dd class="inline break-words">{entry.source_name || "本地草稿"}</dd></div><div><dt class="inline text-muted-foreground">保存：</dt><dd class="inline">{dateTime(entry.created_at)}</dd></div></dl>
      {#if entry.review_flags?.length}<p class="text-sm text-amber-800 dark:text-amber-300">待核对：{entry.review_flags.map(knowledgeReviewLabel).join("；")}</p>{/if}
      {#if entry.excluded_products?.length}<p class="text-sm">不适用：{entry.excluded_products.join("、")}</p>{/if}
      <label class="flex items-center gap-2 text-sm">分类<select value={entry.category_id || 0} disabled={blocked} onchange={(event) => { void moveCategory(Number(event.currentTarget.value)); }} class="h-9 max-w-full rounded-md border bg-background px-3"><option value={0}>未分类</option>{#each categories as category}<option value={category.id}>{category.name}</option>{/each}</select></label>
      {#if entry.replaces_id}<p class="text-sm text-muted-foreground">修订自 <a class="underline underline-offset-4" href={knowledgeLink(entry.replaces_id)}>版本 #{entry.replaces_id}</a></p>{/if}
      {#if entry.lab_batch_id}<p class="text-sm text-muted-foreground">来自 <a class="underline underline-offset-4" href={`#/lab/${entry.lab_batch_id}`}>离线批次 #{entry.lab_batch_id}</a>，发布前请核对批次对照结果。</p>{/if}
      {#if entry.source_ticket_id}<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs"><a class="inline-flex min-h-10 items-center underline underline-offset-4" href={`#/tickets/${encodeURIComponent(entry.source_ticket_id)}`}>查看来源工单</a>{#each entry.message_ids as id}<a class="inline-flex min-h-10 items-center break-all text-muted-foreground underline underline-offset-4" href={`#/tickets/${encodeURIComponent(entry.source_ticket_id)}?message=${encodeURIComponent(id)}`}>消息 {id}</a>{/each}</div>{/if}
    </header>
    {#if entry.status === "archived"}<p class="rounded-md border bg-muted/40 p-4 text-sm">此版本已停用，不再用于新的处理建议，历史来源继续保留。</p>{/if}
    {#if entry.kind === "sop" && entry.status === "draft"}<p class="rounded-md border bg-muted/40 p-4 text-sm">未发布草稿，确认发布后生效。</p>{/if}
    <div class="flex flex-wrap gap-2">
      <Button href={`/api/knowledge/${entry.id}/export?format=md`} variant="outline" class="h-10">导出 Markdown</Button><Button href={`/api/knowledge/${entry.id}/export?format=zip`} variant="outline" class="h-10">导出含图片 ZIP</Button>
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
        <div class="space-y-2"><label for="draft-title" class="text-sm">标题</label><Input id="draft-title" bind:value={editTitle} required maxlength={300} /></div>
        <div class="space-y-2"><label for="draft-scope" class="block text-sm">知识用途</label><select id="draft-scope" bind:value={editScope} disabled={entry.scope === "policy" || !!entry.lab_batch_id} class="h-9 rounded-md border border-input bg-background px-3 text-sm"><option value="reference">参考资料</option><option value="technical">技术知识（可提议修订）</option><option value="policy">售后政策（仅引用）</option></select></div>
        <div class="grid gap-3 sm:grid-cols-2"><label class="space-y-1 text-sm">分类<select bind:value={editCategory} class="h-9 w-full rounded-md border bg-background px-3"><option value={0}>未分类</option>{#each categories as category}<option value={category.id}>{category.name}</option>{/each}</select></label><label class="space-y-1 text-sm">适用机型<Input bind:value={editApplicable} placeholder="用逗号分隔" /></label><label class="space-y-1 text-sm">排除机型<Input bind:value={editExcluded} placeholder="用逗号分隔" /></label></div>
        <MarkdownEditor bind:value={editText} disabled={busy} {token} />
        <div class="flex flex-wrap gap-2"><Button type="submit" disabled={blocked || !editText.trim()}>保存新草稿</Button><Button variant="ghost" disabled={busy} onclick={() => { editing = false; }}>取消编辑</Button></div>
      </form>
    {/if}
    {#if entry.kind === "sop" && history.length > 1}
      <details class="rounded-lg border p-4">
        <summary class="cursor-pointer text-sm font-medium">版本历史与差异（{history.length} 版）</summary>
        <div class="mt-4 space-y-5">
          <ol class="space-y-2 text-sm">{#each history as version (version.id)}<li class="flex flex-wrap items-center gap-x-3 gap-y-1"><a href={knowledgeLink(version.id)} aria-current={version.id === entry.id ? "page" : undefined} class="underline underline-offset-4">版本 #{version.id}</a><span class="text-muted-foreground">{knowledgeStatus(version)} · {dateTime(version.created_at)}</span></li>{/each}</ol>
          <div class="space-y-2"><label for="knowledge-compare" class="block text-sm">与当前查看版本 #{entry.id} 对照</label><select id="knowledge-compare" bind:value={compareID} class="h-9 max-w-full rounded-md border border-input bg-background px-3 text-sm"><option value="">选择对照版本</option>{#each history.filter((version) => version.id !== entry?.id) as version}<option value={String(version.id)}>版本 #{version.id} · {knowledgeStatus(version)}</option>{/each}</select></div>
          {#if compare}
            <p class="text-xs text-muted-foreground">#{compare.id} → #{entry.id} · − 移除 · + 加入</p>
            {#if compare.title !== entry.title || compare.product !== entry.product || compare.scope !== entry.scope || JSON.stringify(compare.applicable_products) !== JSON.stringify(entry.applicable_products) || JSON.stringify(compare.excluded_products) !== JSON.stringify(entry.excluded_products)}<dl class="space-y-2 text-sm"><div><dt class="text-muted-foreground">标题</dt><dd class="break-words">{compare.title} → {entry.title}</dd></div><div><dt class="text-muted-foreground">机型 / 分类</dt><dd>{compare.applicable_products?.join("、") || compare.product || "通用"} / {knowledgeScope(compare.scope)} → {entry.applicable_products?.join("、") || entry.product || "通用"} / {knowledgeScope(entry.scope)}</dd></div><div><dt class="text-muted-foreground">排除机型</dt><dd>{compare.excluded_products?.join("、") || "无"} → {entry.excluded_products?.join("、") || "无"}</dd></div></dl>{/if}
            {#if diff}
              <div class="rounded-md border p-3 text-sm leading-7" aria-label="正文差异">{#each diff as line}<p class={`whitespace-pre-wrap break-words ${line.kind === "added" ? "bg-emerald-50 dark:bg-emerald-950/30" : line.kind === "removed" ? "bg-rose-50 dark:bg-rose-950/30" : "text-muted-foreground"}`}><span class="mr-2 inline-block w-3 select-none" aria-label={line.kind === "added" ? "加入" : line.kind === "removed" ? "移除" : "相同"}>{line.kind === "added" ? "+" : line.kind === "removed" ? "−" : " "}</span>{line.text || " "}</p>{/each}</div>
            {:else}
              <p class="text-xs text-muted-foreground">正文较长，显示完整并排文本供核对。</p><div class="grid gap-4 md:grid-cols-2"><section class="min-w-0 rounded-md border p-3"><h3 class="mb-3 text-sm font-medium">对照版本 #{compare.id}</h3><p class="text-sm leading-7 whitespace-pre-wrap break-words">{compareText}</p></section><section class="min-w-0 rounded-md border p-3"><h3 class="mb-3 text-sm font-medium">当前查看 #{entry.id}</h3><p class="text-sm leading-7 whitespace-pre-wrap break-words">{currentText}</p></section></div>
            {/if}
          {/if}
        </div>
      </details>
    {/if}
    <div class="rounded-lg border p-4 sm:p-6"><MarkdownView value={currentText} /></div>
    <details open={!!blockID} class="border-t pt-4"><summary class="cursor-pointer text-sm text-muted-foreground">引用位置（{entry.blocks.length}）</summary><div class="divide-y">
      {#each entry.blocks as block (block.id)}
        <section id={`knowledge-block-${block.id}`} tabindex="-1" class="scroll-mt-5 space-y-3 py-5 focus:outline-2 focus:outline-ring focus:-outline-offset-2">
          <h2 class="text-xs text-muted-foreground">{block.id}</h2>
          <p class="text-sm leading-7 whitespace-pre-wrap break-words">{block.text}</p>
        </section>
      {/each}
    </div></details>
  {/if}
</section>
