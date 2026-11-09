<!-- input: 所选知识库、用户多文件或 ZIP、受保护导入接口。 -->
<!-- output: 集中预览、逐项编辑/重试和显式批量草稿保存或发布。 -->
<!-- pos: 知识库批量导入面板。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import MarkdownEditor from "./MarkdownEditor.svelte";
  import MarkdownView from "./MarkdownView.svelte";
  import { knowledgeJSON, knowledgeScope, knowledgeReviewLabel, type ImportBatch, type KnowledgeEntry } from "./knowledge-api";
  let { libraryID, token, oncomplete }: { libraryID: number; token: string; oncomplete: () => void } = $props();
  let open = $state(false);
  let files = $state<File[]>([]);
  let batch = $state<ImportBatch | null>(null);
  let selected = $state<string[]>([]);
  let editing = $state("");
  let confirmedRisks = $state(false);
  let busy = $state(false);
  let error = $state("");
  let notice = $state("");
  let action: AbortController | undefined;
  function missingImages(item: ImportBatch["items"][number]): boolean { return item.warnings?.some((warning) => /图片缺失|图片格式或大小不支持|不支持的图片语法/.test(warning)) || false; }
  const selectedMissingImages = $derived(batch?.items.some((item) => selected.includes(item.id) && missingImages(item)));
  const riskSelected = $derived(batch?.items.some((item) => selected.includes(item.id) && item.requires_confirmation));
  const reviewSignature = $derived(JSON.stringify(batch?.items.filter((item) => selected.includes(item.id))));
  $effect(() => { reviewSignature; confirmedRisks = false; });
  onDestroy(() => action?.abort());
  onMount(() => {
    let id = ""; try { id = sessionStorage.getItem(`knowledge-import:${libraryID}`) || ""; } catch { return; }
    if (!id) return;
    busy = true; action = new AbortController();
    void knowledgeJSON<ImportBatch>(`/api/knowledge/import-batch/${encodeURIComponent(id)}`, action.signal).then((result) => { batch = result; open = true; }).catch(() => { /* User may start a new import if the previous task no longer exists. */ }).finally(() => { busy = false; });
  });
  async function request<T>(path: string, body: unknown): Promise<T | null> {
    if (busy || !token) return null;
    busy = true; error = notice = ""; action = new AbortController();
    try { return await knowledgeJSON<T>(path, action.signal, token, body); }
    catch (failure) { if (!action.signal.aborted) error = failure instanceof Error ? failure.message : "导入未完成。"; return null; }
    finally { busy = false; }
  }
  async function preview(event: SubmitEvent) {
    event.preventDefault(); if (!files.length) return;
    const form = new FormData(); form.set("library_id", String(libraryID)); for (const file of files) form.append("files", file);
    const result = await request<ImportBatch>("/api/knowledge/import-batch", form);
    if (result) { try { sessionStorage.setItem(`knowledge-import:${libraryID}`, result.id); } catch { /* Resume is optional. */ } batch = result; selected = result.items.filter((item) => item.status === "ready" && !item.requires_confirmation).map((item) => item.id); confirmedRisks = false; }
  }
  async function retry(id: string) {
    if (!batch) return;
    const next = await request<ImportBatch>(`/api/knowledge/import-batch/${batch.id}/retry`, { item_ids: [id] });
    if (next) { batch = next; selected = selected.filter((id) => next.items.some((item) => item.id === id && item.status === "ready")); }
  }
  async function confirm(publish: boolean) {
    if (!batch || !selected.length) return;
    const result = await request<{ data: KnowledgeEntry[]; errors?: string[] }>(`/api/knowledge/import-batch/${batch.id}/confirm`, { item_ids: selected, items: batch.items.filter((item) => selected.includes(item.id)).map(({ id, title, markdown, product, applicable_products, excluded_products, category, scope }) => ({ id, title, markdown, product, applicable_products, excluded_products, category, scope })), publish, confirmed_risks: confirmedRisks });
    if (result) {
      notice = `${publish ? "已发布" : "已保存草稿"} ${result.data.length} 条。${result.errors?.join("；") || ""}`; selected = [];
      busy = true;
      try { batch = await knowledgeJSON<ImportBatch>(`/api/knowledge/import-batch/${batch.id}`, action!.signal); }
      catch { notice += "预览状态未刷新，请重新打开导入区。"; }
      finally { busy = false; oncomplete(); }
    }
  }
</script>
<details bind:open class="rounded-lg border p-4">
  <summary class="cursor-pointer text-sm font-medium">批量导入</summary>
  <form class="mt-4 flex flex-wrap items-end gap-3" onsubmit={preview}>
    <div class="min-w-0 flex-1 space-y-2"><label for="knowledge-files" class="text-sm">ZIP / Markdown / Word / PDF</label><Input id="knowledge-files" type="file" multiple accept=".zip,.md,.markdown,.txt,.docx,.pdf" required onchange={(event) => { files = Array.from(event.currentTarget.files || []); }} /></div>
    <Button type="submit" disabled={busy || !token || !libraryID || !files.length}>{busy ? "正在解析并下载引用图片…" : "导入预览"}</Button>
  </form>
  {#if error}<p role="alert" class="mt-3 text-sm text-destructive">{error}</p>{/if}
  {#if notice}<p role="status" class="mt-3 text-sm">{notice}</p>{/if}
  {#if batch}
    <div class="mt-5 flex flex-wrap items-center gap-3 border-b pb-3"><span class="text-sm">{batch.items.length} 条，已选 {selected.length} 条</span><Button size="sm" variant="ghost" disabled={busy} onclick={() => { selected = batch!.items.filter((item) => item.status === "ready" && !item.requires_confirmation).map((item) => item.id); confirmedRisks = false; }}>选择可直接审核项</Button><Button size="sm" variant="ghost" disabled={busy} onclick={() => { selected = batch!.items.filter((item) => item.status === "ready").map((item) => item.id); confirmedRisks = false; }}>全选待审核项</Button><Button size="sm" variant="ghost" onclick={() => { selected = []; }}>清空选择</Button></div>
    <div class="divide-y">
      {#each batch.items as item (item.id)}
        <article class="min-w-0 py-4">
          <div class="flex items-start gap-3"><input aria-label={`选择 ${item.title}`} class="mt-1.5" type="checkbox" value={item.id} bind:group={selected} disabled={busy || item.status !== "ready"} /><div class="min-w-0 flex-1"><h3 class="text-sm font-medium break-words">{item.title}</h3><p class="mt-1 text-xs text-muted-foreground">{item.category || "未分类"} · {knowledgeScope(item.scope)} · {item.status === "duplicate" ? "重复，已跳过" : item.status === "failed" ? "导入失败" : item.requires_confirmation ? "需要单独确认" : "待审核"}</p></div>{#if item.status === "failed" || missingImages(item)}<Button size="sm" variant="outline" disabled={busy} onclick={() => retry(item.id)}>重试此项</Button>{/if}</div>
          {#if item.error}<p class="mt-2 text-sm text-destructive">{item.error}</p>{/if}
          {#if item.warnings?.length}<ul class="mt-2 list-inside list-disc text-sm text-amber-800 dark:text-amber-300">{#each item.warnings as warning}<li>{knowledgeReviewLabel(warning)}</li>{/each}</ul>{/if}
          {#if item.markdown}<details class="mt-3"><summary class="cursor-pointer text-sm text-muted-foreground">查看并修订内容</summary><div class="mt-4 space-y-4">
            <div class="grid gap-3 sm:grid-cols-2"><label class="space-y-1 text-sm">标题<Input bind:value={item.title} /></label><label class="space-y-1 text-sm">分类<Input bind:value={item.category} /></label><label class="space-y-1 text-sm">知识用途<select bind:value={item.scope} disabled={item.scope === "policy"} class="h-9 w-full rounded-md border bg-background px-3"><option value="reference">参考资料</option><option value="technical">技术知识</option><option value="policy">售后政策</option></select></label><label class="space-y-1 text-sm">适用机型<Input value={item.applicable_products?.join(", ") || item.product || ""} onchange={(event) => { item.applicable_products = event.currentTarget.value.split(/[,，\n]/).map((v) => v.trim()).filter(Boolean); item.product = item.applicable_products.length === 1 ? item.applicable_products[0] : ""; }} /></label><label class="space-y-1 text-sm">排除机型<Input value={(item.excluded_products || []).join(", ")} onchange={(event) => { item.excluded_products = event.currentTarget.value.split(/[,，\n]/).map((v) => v.trim()).filter(Boolean); }} /></label></div>
            {#if editing === item.id}<MarkdownEditor bind:value={item.markdown} disabled={busy} {token} />{:else}<MarkdownView value={item.markdown} />{/if}
            <Button size="sm" variant="outline" onclick={() => { editing = editing === item.id ? "" : item.id; }}>{editing === item.id ? "完成编辑" : "编辑 Markdown"}</Button>
          </div></details>{/if}
        </article>
      {/each}
    </div>
    {#if riskSelected}<label class="my-4 flex items-start gap-2 text-sm"><input type="checkbox" bind:checked={confirmedRisks} class="mt-1" />我已逐项核对选中的政策、冲突、适用条件和素材缺失提示</label>{/if}
    {#if selectedMissingImages}<p class="mt-3 text-sm text-destructive">所选条目有缺失图片，可先保存草稿，补齐后发布。</p>{/if}
    <div class="mt-3 flex flex-wrap gap-2"><Button variant="outline" disabled={busy || !selected.length} onclick={() => confirm(false)}>保存所选为草稿</Button><Button disabled={busy || !selected.length || !!selectedMissingImages || (!!riskSelected && !confirmedRisks)} onclick={() => confirm(true)}>确认发布所选</Button></div>
  {/if}
</details>
