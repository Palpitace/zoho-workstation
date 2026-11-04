<!-- input: 浏览器 hash 导航与 Go 只读查询 API。 -->
<!-- output: 工单列表、详情和运行状态的应用外壳。 -->
<!-- pos: 前端导航与异步数据编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount } from "svelte";
  import { Activity, Inbox, RefreshCw } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import TicketList from "$lib/TicketList.svelte";
  import TicketDetail from "$lib/TicketDetail.svelte";
  import RunStatus from "$lib/RunStatus.svelte";
  import { getJSON, type TicketRecord, type SystemStatus, type JevView, type BubView } from "$lib/api";

  let hash = $state(window.location.hash);
  const ticketID = $derived(hash.startsWith("#/tickets/") ? hash.slice("#/tickets/".length) : "");
  const statusPage = $derived(hash === "#/status");
  let records = $state<TicketRecord[]>([]);
  let status = $state<SystemStatus | null>(null);
  let detail = $state<TicketRecord | null>(null);
  let jev = $state<JevView | null>(null);
  let bub = $state<BubView | null>(null);
  let analysisLoading = $state(false);
  let analysisError = $state("");
  let query = $state("");
  let statusFilter = $state("");
  let statusOptions = $state<string[]>([]);
  let loading = $state(false);
  let detailLoading = $state(false);
  let listError = $state("");
  let statusError = $state("");
  let detailError = $state("");
  let revision = $state(0);
  let controller: AbortController | undefined;

  function errorText(error: unknown): string {
    return error instanceof Error && error.name !== "TypeError" ? error.message : "无法连接本机服务，请确认服务已启动后重试。";
  }

  async function refresh() {
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    loading = true;
    listError = statusError = "";
    const params = new URLSearchParams({ q: query, status: statusFilter });
    const [ticketsResult, statusResult] = await Promise.allSettled([
      getJSON<{ data: TicketRecord[] }>(`/api/tickets?${params}`, signal),
      getJSON<SystemStatus>("/api/status", signal),
    ]);
    if (signal.aborted) return;
    if (ticketsResult.status === "fulfilled") {
      records = ticketsResult.value.data;
      statusOptions = [...new Set([...statusOptions, ...records.map((record) => record.status).filter(Boolean)])].sort();
    } else listError = errorText(ticketsResult.reason);
    if (statusResult.status === "fulfilled") status = statusResult.value;
    else statusError = errorText(statusResult.reason);
    loading = false;
    revision++;
  }

  $effect(() => {
    const id = ticketID;
    revision;
    detail = null;
    jev = null;
    bub = null;
    detailError = "";
    analysisError = "";
    if (!id) { detailLoading = analysisLoading = false; return; }
    const request = new AbortController();
    detailLoading = true;
    analysisLoading = true;
    getJSON<TicketRecord>(`/api/tickets/${id}`, request.signal)
      .then((value) => { if (!request.signal.aborted) detail = value; })
      .catch((error) => { if (!request.signal.aborted) detailError = errorText(error); })
      .finally(() => { if (!request.signal.aborted) detailLoading = false; });
    getJSON<{ jev: JevView; bub: BubView }>(`/api/tickets/${id}/analysis`, request.signal)
      .then((value) => { if (!request.signal.aborted) { jev = value.jev; bub = value.bub ?? null; } })
      .catch((error) => { if (!request.signal.aborted) analysisError = errorText(error); })
      .finally(() => { if (!request.signal.aborted) analysisLoading = false; });
    return () => request.abort();
  });

  onMount(() => {
    const navigate = () => { hash = window.location.hash; };
    window.addEventListener("hashchange", navigate);
    refresh();
    return () => { window.removeEventListener("hashchange", navigate); controller?.abort(); };
  });
</script>

<a href="#main-content" onclick={(event) => { event.preventDefault(); document.getElementById("main-content")?.focus(); }} class="sr-only fixed top-3 left-3 z-50 rounded-md bg-primary px-4 py-2 text-primary-foreground focus:not-sr-only">跳到主要内容</a>
<div class="min-h-screen bg-background text-foreground md:grid md:grid-cols-[208px_minmax(0,1fr)]">
  <aside class="border-b bg-muted/45 md:sticky md:top-0 md:flex md:h-screen md:flex-col md:border-r md:border-b-0">
    <div class="px-5 py-5 md:py-7"><p class="font-semibold">工单工作台</p><p class="mt-1 text-xs text-muted-foreground">Zoho Desk · 只读</p></div>
    <nav aria-label="主导航" class="flex gap-1 px-3 pb-3 md:flex-col">
      <Button href="#/tickets" variant={!statusPage ? "secondary" : "ghost"} aria-current={!statusPage ? "page" : undefined} class="h-10 justify-start"><Inbox size={17} aria-hidden="true" />工单</Button>
      <Button href="#/status" variant={statusPage ? "secondary" : "ghost"} aria-current={statusPage ? "page" : undefined} class="h-10 justify-start"><Activity size={17} aria-hidden="true" />运行状态</Button>
    </nav>
    <div class="mt-auto hidden border-t px-5 py-5 text-xs text-muted-foreground md:block"><p>当前部门</p><p class="mt-2 break-all font-medium text-foreground">{status?.sync.department_id || "等待加载"}</p></div>
  </aside>
  <div class="min-w-0">
    <header class="flex min-h-16 flex-wrap items-center justify-between gap-3 border-b px-5 py-3 lg:px-8">
      <span class="text-sm text-muted-foreground">{statusPage ? "运行状态" : ticketID ? "工单 / 详情" : "工单 / 列表"}</span>
      <Button variant="outline" class="h-10" onclick={refresh} disabled={loading || detailLoading}><RefreshCw size={15} aria-hidden="true" />{loading ? "正在刷新…" : "刷新数据"}</Button>
    </header>
    <main id="main-content" tabindex="-1" class="mx-auto max-w-7xl p-5 outline-none lg:p-8">
      {#if !statusPage && status}
        <dl class="mb-7 flex flex-wrap gap-x-7 gap-y-2 border-b pb-5 text-sm text-muted-foreground">
          <div class="flex gap-2"><dt>已选取</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.selected}</dd></div>
          <div class="flex gap-2"><dt>已有快照</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.ready}</dd></div>
          <div class="flex gap-2"><dt>待首次保存</dt><dd class="font-medium text-foreground tabular-nums">{Math.max(0, status.sync.selected - status.sync.ready)}</dd></div>
          <div class="flex gap-2"><dt>失败或不完整</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.failed}</dd></div>
        </dl>
      {/if}
      {#if !statusPage && statusError}<p role="alert" class="mb-5 text-sm text-destructive">运行状态：{statusError}</p>{/if}
      {#if statusPage}
        <RunStatus {status} {loading} error={statusError} />
      {:else if ticketID}
        <TicketDetail ticket={detail} loading={detailLoading} error={detailError} {jev} {bub} {analysisLoading} {analysisError} />
      {:else}
        <TicketList {records} {loading} error={listError} bind:query bind:statusFilter {statusOptions} onSearch={refresh} />
      {/if}
    </main>
  </div>
</div>
