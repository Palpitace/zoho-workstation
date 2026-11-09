<!-- input: 知识库/部门、查询和本地索引状态 API。 -->
<!-- output: 模型下载状态、降级提示及带版本引用的检索测试。 -->
<!-- pos: 知识库检索与本地模型管理面板。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount } from "svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import { knowledgeJSON, knowledgeLink, type KnowledgeSource, type EmbeddingStatus } from "./knowledge-api";
  let { libraryID, departments, token }: { libraryID: number; departments: string[]; token: string } = $props();
  let status = $state<EmbeddingStatus | null>(null);
  let query = $state(""); let product = $state(""); let department = $state("");
  let sources = $state<KnowledgeSource[] | null>(null); let mode = $state("");
  let error = $state(""); let busy = $state(false);
  let controller: AbortController; let timer: ReturnType<typeof setTimeout>;
  async function poll() { try { status = await knowledgeJSON<EmbeddingStatus>("/api/knowledge/embedding", controller.signal); } catch { /* Retrieval remains usable without model status. */ } finally { if (!controller.signal.aborted) timer = setTimeout(poll, 3000); } }
  onMount(() => { controller = new AbortController(); void poll(); return () => { controller.abort(); clearTimeout(timer); }; });
  async function run(download = false) {
    if (busy) return; busy = true; error = "";
    try {
      if (download) { await knowledgeJSON("/api/knowledge/embedding/download", controller.signal, token, {}); status = await knowledgeJSON<EmbeddingStatus>("/api/knowledge/embedding", controller.signal); }
      else { const result = await knowledgeJSON<{ sources: KnowledgeSource[]; mode: string }>("/api/knowledge/retrieve", controller.signal, token, { library_id: libraryID, department_id: department || departments[0], product, query }); sources = result.sources; mode = result.mode; }
    } catch (failure) { if (!controller.signal.aborted) error = failure instanceof Error ? failure.message : "操作失败。"; }
    finally { busy = false; }
  }
  const stateLabel = $derived(({ ready: "本地语义检索已就绪", stopped: "本地模型已停止，可重试", downloading: "模型下载中", indexing: "正在更新索引", starting: "模型启动中", missing: "尚未下载语义模型", not_installed: "尚未下载语义模型", error: "语义检索不可用", failed: "语义检索不可用", disabled: "语义检索未启用" } as Record<string, string>)[status?.state || ""] || "正在读取索引状态");
</script>
<details class="rounded-lg border p-4">
  <summary class="cursor-pointer text-sm font-medium">检索测试与本地模型</summary>
  <div class="mt-4 space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3"><div class="space-y-1"><p class="text-sm">{stateLabel}{status?.state === "downloading" && typeof status?.progress === "number" ? ` · ${Math.round(status.progress)}%` : ""}</p>{#if status?.total !== undefined}<p class="text-xs text-muted-foreground">已索引 {status.indexed || 0} / {status.total}</p>{/if}{#if status?.degraded}<p class="text-xs text-amber-800 dark:text-amber-300">当前使用关键词检索</p>{/if}{#if status?.error}<p class="text-sm text-destructive">{status.error}</p>{/if}</div><Button variant="outline" size="sm" disabled={busy || !token || status?.state === "downloading" || status?.state === "starting"} onclick={() => run(true)}>{status?.state === "ready" ? "检查模型与索引" : "下载 / 重试本地模型"}</Button></div>
    <form class="space-y-3" onsubmit={(event) => { event.preventDefault(); void run(); }}><div class="grid gap-3 sm:grid-cols-2"><label class="space-y-1 text-sm">部门<select bind:value={department} class="h-9 w-full rounded-md border bg-background px-3"><option value="">{departments[0] || "请选择知识库"}</option>{#each departments as id}<option value={id}>{id}</option>{/each}</select></label><label class="space-y-1 text-sm">机型<Input bind:value={product} placeholder="留空检索通用知识" /></label></div><div class="flex gap-2"><Input aria-label="检索问题" bind:value={query} placeholder="用客户的问法测试检索" required /><Button disabled={busy || !token || !libraryID} type="submit">检索</Button></div></form>
    {#if error}<p role="alert" class="text-sm text-destructive">{error}</p>{/if}
    {#if sources}<p class="text-xs text-muted-foreground">{mode === "hybrid" ? "关键词 + 语义" : "关键词"} · {sources.length} 条命中</p><div class="divide-y">{#each sources as source (source.id)}<article class="space-y-2 py-3"><a class="text-sm font-medium underline underline-offset-4" href={knowledgeLink(source.entry_id, source.block_id)}>{source.title}</a><p class="text-xs text-muted-foreground">{source.product || "通用"} · 版本 #{source.entry_id} · {source.block_id}</p><p class="text-sm leading-6 whitespace-pre-wrap break-words">{source.text}</p></article>{:else}<p class="text-sm text-muted-foreground">没有适用的已发布知识。</p>{/each}</div>{/if}
  </div>
</details>
