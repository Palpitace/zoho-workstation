<!-- input: 浏览器导航、Go 查询/操作 API 及会话防护令牌。 -->
<!-- output: 工单/知识库导航、组合筛选、来源定位和可见页面定期刷新。 -->
<!-- pos: 前端导航与异步数据编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount } from "svelte";
  import { Activity, BookOpen, Inbox, RefreshCw } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import TicketList from "$lib/TicketList.svelte";
  import TicketDetail from "$lib/TicketDetail.svelte";
  import RunStatus from "$lib/RunStatus.svelte";
  import KnowledgePage from "$lib/KnowledgePage.svelte";
  import { getJSON, postAction, type TicketRecord, type SystemStatus, type JevView, type BubView, type AIKind, type Operation, type FilterOptions, type FilterKey, type SearchFilters, defaultSearch, emptyFilters } from "$lib/api";

  let hash = $state(window.location.hash);
  const route = $derived(hash.split("?")[0]);
  const routeQuery = $derived(new URLSearchParams(hash.includes("?") ? hash.slice(hash.indexOf("?") + 1) : ""));
  const ticketID = $derived(route.startsWith("#/tickets/") ? route.slice("#/tickets/".length) : "");
  const knowledgePage = $derived(route === "#/knowledge" || route.startsWith("#/knowledge/"));
  const knowledgeID = $derived(route.startsWith("#/knowledge/") ? route.slice("#/knowledge/".length) : "");
  let knowledgeRevision = $state(0);
  const statusPage = $derived(hash === "#/status");
  let records = $state<TicketRecord[]>([]);
  let status = $state<SystemStatus | null>(null);
  let detail = $state<TicketRecord | null>(null);
  let jev = $state<JevView | null>(null);
  let bub = $state<BubView | null>(null);
  let analysisLoading = $state(false);
  let analysisError = $state("");
  let search = $state<SearchFilters>(defaultSearch());
  let filters = $state(emptyFilters());
  let filterOptions = $state<FilterOptions>({});
  let showHistory = $state(false);
  let loading = $state(false);
  let detailLoading = $state(false);
  let listError = $state("");
  let statusError = $state("");
  let detailError = $state("");
  let submitting = $state({ sync: false, jev: false, bub: false });
  let actionErrors = $state({ sync: "", jev: "", bub: "" });
  let notices = $state({ sync: "", jev: "", bub: "" });
  let controller: AbortController | undefined;
  let pollController: AbortController | undefined;
  let refreshing = $state(false);
  let polling = false;
  let appliedSearch = $state<SearchFilters>(defaultSearch());
  const actionRequests = new Map<Operation, AbortController>();

  function errorText(error: unknown): string {
    return error instanceof Error && error.name !== "TypeError" ? error.message : "无法连接本机服务，请确认服务已启动后重试。";
  }

  function blocked(kind: Operation): string {
    if (!status?.csrf_token || statusError) return "等待有效运行状态";
    if (submitting[kind]) return "正在提交请求";
    if (status.runtime?.[`${kind}_running`]) return "任务进行中";
    if (kind === "sync") return "";
    if (!status[`${kind}_enabled`]) return "能力已关闭";
    if (!detail || detail.pending) return "工单尚未保存";
    if (kind === "jev" && jev?.waiting_for === "bub") return "等待当前会话的 Bub 总结";
    if ((kind === "jev" ? jev : bub)?.current?.status === "running") return "任务进行中";
    return "";
  }
  const controls = $derived({
    jev: { blocked: blocked("jev"), error: actionErrors.jev, notice: notices.jev },
    bub: { blocked: blocked("bub"), error: actionErrors.bub, notice: notices.bub },
  });

  async function refresh(silent = false, knownStatus?: SystemStatus) {
    if (silent && refreshing) return;
    if (!knownStatus) pollController?.abort();
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    refreshing = true;
    const selected = ticketID;
    const sameTicket = !!selected && detail !== null && encodeURIComponent(detail.id) === selected;
    if (!sameTicket) { detail = null; jev = null; bub = null; }
    detailLoading = !!selected && !sameTicket;
    analysisLoading = !!selected && !jev && !bub;
    loading = !silent;
    const params = new URLSearchParams({ q: appliedSearch.q, field: appliedSearch.field, date_field: appliedSearch.dateField, ...filters, show_history: String(showHistory) });
    for (const [key, date] of [["from", appliedSearch.startDate], ["until", appliedSearch.endDate]]) {
      if (!date) continue;
      const boundary = new Date(`${date}T00:00:00`);
      if (key === "until") boundary.setDate(boundary.getDate() + 1);
      params.set(key, boundary.toISOString());
    }
    const [ticketsResult, statusResult, detailResult, analysisResult] = await Promise.allSettled([
      !ticketID && !statusPage && !knowledgePage ? getJSON<{ data: TicketRecord[]; filters: FilterOptions }>(`/api/tickets?${params}`, signal) : Promise.resolve(null),
      knownStatus ? Promise.resolve(knownStatus) : getJSON<SystemStatus>("/api/status", signal),
      selected ? getJSON<TicketRecord>(`/api/tickets/${selected}`, signal) : Promise.resolve(null),
      selected ? getJSON<{ jev: JevView; bub: BubView }>(`/api/tickets/${selected}/analysis`, signal) : Promise.resolve(null),
    ]);
    if (signal.aborted) return;
    if (ticketsResult.status === "fulfilled" && ticketsResult.value) {
      records = ticketsResult.value.data;
      filterOptions = ticketsResult.value.filters;
      listError = "";
    } else if (ticketsResult.status === "rejected") listError = errorText(ticketsResult.reason);
    if (statusResult.status === "fulfilled") { status = statusResult.value; statusError = ""; }
    else statusError = errorText(statusResult.reason);
    if (selected) {
      if (detailResult.status === "fulfilled" && detailResult.value) { detail = detailResult.value; detailError = ""; }
      else if (detailResult.status === "rejected") detailError = `${sameTicket ? "刷新失败，仍显示旧数据。" : ""}${errorText(detailResult.reason)}`;
      if (analysisResult.status === "fulfilled" && analysisResult.value) { jev = analysisResult.value.jev; bub = analysisResult.value.bub; analysisError = ""; }
      else if (analysisResult.status === "rejected") analysisError = `${jev || bub ? "刷新失败，仍显示旧分析数据。" : ""}${errorText(analysisResult.reason)}`;
    }
    loading = refreshing = detailLoading = analysisLoading = false;
    if (knowledgePage) knowledgeRevision += 1;
  }

  function applySearch() {
    appliedSearch = { ...search };
    void refresh();
  }
  function applyFilter(key: FilterKey, value: string) {
    filters = { ...filters, [key]: value };
    void refresh();
  }
  function clearFilters() {
    search = defaultSearch();
    appliedSearch = defaultSearch();
    filters = emptyFilters();
    showHistory = false;
    void refresh();
  }

  async function poll() {
    if (document.visibilityState !== "visible" || polling || refreshing || detailLoading || analysisLoading) return;
    polling = true;
    pollController = new AbortController();
    const signal = pollController.signal;
    try {
      const next = await getJSON<SystemStatus>("/api/status", signal);
      if (signal.aborted) return;
      status = next;
      statusError = "";
      // Aggregate counts can stay identical while individual rows change.
      if (!statusPage) await refresh(true, next);
    } catch (error) { if (!signal.aborted) statusError = errorText(error); }
    finally { polling = false; }
  }

  async function operate(kind: Operation) {
    if (blocked(kind)) return;
    const selected = ticketID;
    const request = new AbortController();
    actionRequests.set(kind, request);
    submitting[kind] = true;
    actionErrors[kind] = notices[kind] = "";
    try {
      await postAction(kind === "sync" ? "/api/sync" : `/api/tickets/${selected}/analysis/${kind}`, status!.csrf_token, request.signal);
      if (!request.signal.aborted && (kind === "sync" || selected === ticketID)) notices[kind] = "请求已受理，完成情况请查看处理状态。";
    } catch (error) {
      if (!request.signal.aborted && (kind === "sync" || selected === ticketID)) actionErrors[kind] = errorText(error);
    } finally {
      if (!request.signal.aborted) await refresh(true);
      submitting[kind] = false;
      actionRequests.delete(kind);
    }
  }

  onMount(() => {
    const navigate = () => {
      hash = window.location.hash;
      actionErrors.jev = actionErrors.bub = notices.jev = notices.bub = "";
      detailError = analysisError = "";
      void refresh();
    };
    window.addEventListener("hashchange", navigate);
    document.addEventListener("visibilitychange", poll);
    const timer = window.setInterval(poll, 5000);
    refresh();
    return () => {
      clearInterval(timer);
      window.removeEventListener("hashchange", navigate);
      document.removeEventListener("visibilitychange", poll);
      controller?.abort(); pollController?.abort();
      for (const request of actionRequests.values()) request.abort();
    };
  });
</script>

<a href="#main-content" onclick={(event) => { event.preventDefault(); document.getElementById("main-content")?.focus(); }} class="sr-only fixed top-3 left-3 z-50 rounded-md bg-primary px-4 py-2 text-primary-foreground focus:not-sr-only">跳到主要内容</a>
<div class="min-h-screen bg-background text-foreground md:grid md:grid-cols-[208px_minmax(0,1fr)]">
  <aside class="border-b bg-muted/45 md:sticky md:top-0 md:flex md:h-screen md:flex-col md:border-r md:border-b-0">
    <div class="px-5 py-5 md:py-7"><p class="font-semibold">工单工作台</p><p class="mt-1 text-xs text-muted-foreground">Zoho Desk · 只读</p></div>
    <nav aria-label="主导航" class="flex gap-1 px-3 pb-3 md:flex-col">
      <Button href="#/tickets" variant={!statusPage && !knowledgePage ? "secondary" : "ghost"} aria-current={!statusPage && !knowledgePage ? "page" : undefined} class="h-10 justify-start"><Inbox size={17} aria-hidden="true" />工单</Button>
      <Button href="#/knowledge" variant={knowledgePage ? "secondary" : "ghost"} aria-current={knowledgePage ? "page" : undefined} class="h-10 justify-start"><BookOpen size={17} aria-hidden="true" />知识库</Button>
      <Button href="#/status" variant={statusPage ? "secondary" : "ghost"} aria-current={statusPage ? "page" : undefined} class="h-10 justify-start"><Activity size={17} aria-hidden="true" />运行状态</Button>
    </nav>
    <div class="mt-auto hidden border-t px-5 py-5 text-xs text-muted-foreground md:block"><p>当前部门</p><p class="mt-2 break-all font-medium text-foreground">{status?.sync.department_id || "等待加载"}</p></div>
  </aside>
  <div class="min-w-0">
    <header class="flex min-h-16 flex-wrap items-center justify-between gap-3 border-b px-5 py-3 lg:px-8">
      <span class="text-sm text-muted-foreground">{statusPage ? "运行状态" : knowledgePage ? "知识库" : ticketID ? "工单 / 详情" : "工单 / 列表"}</span>
      <div class="flex flex-wrap items-center gap-2">
        <span role="status" class="min-h-5 w-24 text-xs text-muted-foreground">{refreshing ? "正在刷新数据…" : ""}</span>
        <Button variant="outline" class="h-10" onclick={() => { if (!refreshing) void refresh(); }} disabled={refreshing}><RefreshCw size={15} aria-hidden="true" class={refreshing ? "motion-safe:animate-spin" : ""} />刷新数据</Button>
        <Button class="h-10" onclick={() => operate("sync")} disabled={!!blocked("sync")} title={blocked("sync") || "从 Zoho 只读同步指定部门工单"}>{status?.runtime?.sync_running ? "同步进行中" : submitting.sync ? "正在提交…" : "同步工单"}</Button>
      </div>
    </header>
    <main aria-busy={refreshing} id="main-content" tabindex="-1" class="mx-auto max-w-7xl p-5 outline-none lg:p-8">
      {#if actionErrors.sync}<p role="alert" class="mb-5 text-sm text-destructive">同步操作：{actionErrors.sync}</p>{/if}
      {#if notices.sync}<p role="status" class="mb-5 text-sm text-muted-foreground">同步操作：{notices.sync}</p>{/if}
      {#if !statusPage && !knowledgePage && status}
        <dl class="mb-7 flex flex-wrap gap-x-7 gap-y-2 border-b pb-5 text-sm text-muted-foreground">
          <div class="flex gap-2"><dt>已发现</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.selected}</dd></div>
          <div class="flex gap-2"><dt>已有快照</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.ready}</dd></div>
          <div class="flex gap-2"><dt>待首次保存</dt><dd class="font-medium text-foreground tabular-nums">{Math.max(0, status.sync.selected - status.sync.ready)}</dd></div>
          <div class="flex gap-2"><dt>失败或不完整</dt><dd class="font-medium text-foreground tabular-nums">{status.sync.failed}</dd></div>
        </dl>
      {/if}
      {#if !statusPage && statusError}<p role="alert" class="mb-5 text-sm text-destructive">运行状态：{statusError}</p>{/if}
      {#if statusPage}
        <RunStatus {status} {loading} error={statusError} onRefresh={() => refresh(true)} />
      {:else if knowledgePage}
        {#key knowledgeID}<KnowledgePage entryID={knowledgeID} blockID={routeQuery.get("block") || ""} token={statusError ? "" : status?.csrf_token || ""} refreshVersion={knowledgeRevision} />{/key}
      {:else if ticketID}
        <TicketDetail token={statusError ? "" : status?.csrf_token || ""} referenceID={routeQuery.get("message") || ""} ticket={detail} loading={detailLoading} error={detailError} {jev} {bub} {analysisLoading} {analysisError} {controls} onRerun={(kind: AIKind) => operate(kind)} />
      {:else}
        <TicketList {records} {loading} error={listError} hasAppliedFilters={Boolean(appliedSearch.q || appliedSearch.startDate || appliedSearch.endDate || Object.values(filters).some(Boolean))} bind:search bind:showHistory {filters} {filterOptions} onSearch={applySearch} onFilter={applyFilter} onClear={clearFilters} onHistory={() => refresh()} />
      {/if}
    </main>
  </div>
</div>
