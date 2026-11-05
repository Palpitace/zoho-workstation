<!-- input: 浏览器导航、Go 查询/操作 API 及会话防护令牌。 -->
<!-- output: 支持表头状态/产品筛选及 Bub→Jev的工单界面、受控人工操作（无累计额度拦截）及可见页面的轻量进度刷新。 -->
<!-- pos: 前端导航与异步数据编排。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Activity, Inbox, RefreshCw } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import TicketList from "$lib/TicketList.svelte";
  import TicketDetail from "$lib/TicketDetail.svelte";
  import RunStatus from "$lib/RunStatus.svelte";
  import { getJSON, postAction, type TicketRecord, type SystemStatus, type JevView, type BubView, type AIKind, type Operation } from "$lib/api";

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
  let productFilter = $state("");
  let productOptions = $state<string[]>([]);
  let statusOptions = $state<string[]>([]);
  let loading = $state(false);
  let detailLoading = $state(false);
  let listError = $state("");
  let statusError = $state("");
  let detailError = $state("");
  let revision = $state(0);
  let submitting = $state({ sync: false, jev: false, bub: false });
  let actionErrors = $state({ sync: "", jev: "", bub: "" });
  let notices = $state({ sync: "", jev: "", bub: "" });
  let controller: AbortController | undefined;
  let pollController: AbortController | undefined;
  let refreshing = false;
  let polling = false;
  let appliedQuery = "";
  let appliedStatusFilter = "";
  let appliedProductFilter = "";
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
    loading = !silent;
    if (!silent) { appliedQuery = query; appliedStatusFilter = statusFilter; appliedProductFilter = productFilter; }
    const params = new URLSearchParams({ q: appliedQuery, status: appliedStatusFilter, product: appliedProductFilter });
    const [ticketsResult, statusResult] = await Promise.allSettled([
      !ticketID && !statusPage ? getJSON<{ data: TicketRecord[]; filters: { statuses: string[]; products: string[] } }>(`/api/tickets?${params}`, signal) : Promise.resolve(null),
      knownStatus ? Promise.resolve(knownStatus) : getJSON<SystemStatus>("/api/status", signal),
    ]);
    if (signal.aborted) return;
    if (ticketsResult.status === "fulfilled" && ticketsResult.value) {
      records = ticketsResult.value.data;
      statusOptions = ticketsResult.value.filters.statuses;
      productOptions = ticketsResult.value.filters.products;
      listError = "";
    } else if (ticketsResult.status === "rejected") listError = errorText(ticketsResult.reason);
    if (statusResult.status === "fulfilled") { status = statusResult.value; statusError = ""; }
    else statusError = errorText(statusResult.reason);
    loading = refreshing = false;
    revision++;
  }

  function progress(value: SystemStatus | null): string {
    return JSON.stringify(value ? { sync: value.sync, runtime: value.runtime, ai: value.ai, budget: value.budget } : null);
  }
  async function poll() {
    if (document.visibilityState !== "visible" || polling || refreshing || detailLoading || analysisLoading) return;
    polling = true;
    pollController = new AbortController();
    const signal = pollController.signal;
    try {
      const next = await getJSON<SystemStatus>("/api/status", signal);
      if (signal.aborted) return;
      const changed = progress(next) !== progress(status);
      status = next;
      statusError = "";
      if (changed && !statusPage) await refresh(true, next);
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

  $effect(() => {
    const id = ticketID;
    revision;
    const sameTicket = untrack(() => detail !== null && encodeURIComponent(detail.id) === id);
    if (!sameTicket) { detail = null; jev = null; bub = null; }
    detailError = analysisError = "";
    if (!id) { detailLoading = analysisLoading = false; return; }
    const request = new AbortController();
    detailLoading = !sameTicket;
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
    const navigate = () => {
      hash = window.location.hash;
      actionErrors.jev = actionErrors.bub = notices.jev = notices.bub = "";
      if (!hash.startsWith("#/tickets/") && hash !== "#/status") refresh(true);
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
      <Button href="#/tickets" variant={!statusPage ? "secondary" : "ghost"} aria-current={!statusPage ? "page" : undefined} class="h-10 justify-start"><Inbox size={17} aria-hidden="true" />工单</Button>
      <Button href="#/status" variant={statusPage ? "secondary" : "ghost"} aria-current={statusPage ? "page" : undefined} class="h-10 justify-start"><Activity size={17} aria-hidden="true" />运行状态</Button>
    </nav>
    <div class="mt-auto hidden border-t px-5 py-5 text-xs text-muted-foreground md:block"><p>当前部门</p><p class="mt-2 break-all font-medium text-foreground">{status?.sync.department_id || "等待加载"}</p></div>
  </aside>
  <div class="min-w-0">
    <header class="flex min-h-16 flex-wrap items-center justify-between gap-3 border-b px-5 py-3 lg:px-8">
      <span class="text-sm text-muted-foreground">{statusPage ? "运行状态" : ticketID ? "工单 / 详情" : "工单 / 列表"}</span>
      <div class="flex flex-wrap gap-2">
        <Button variant="outline" class="h-10" onclick={() => refresh()} disabled={loading || detailLoading}><RefreshCw size={15} aria-hidden="true" />{loading ? "正在刷新…" : "刷新数据"}</Button>
        <Button class="h-10" onclick={() => operate("sync")} disabled={!!blocked("sync")} title={blocked("sync") || "从 Zoho 只读同步固定工单样本"}>{status?.runtime?.sync_running ? "同步进行中" : submitting.sync ? "正在提交…" : "同步工单"}</Button>
      </div>
    </header>
    <main id="main-content" tabindex="-1" class="mx-auto max-w-7xl p-5 outline-none lg:p-8">
      {#if actionErrors.sync}<p role="alert" class="mb-5 text-sm text-destructive">同步操作：{actionErrors.sync}</p>{/if}
      {#if notices.sync}<p role="status" class="mb-5 text-sm text-muted-foreground">同步操作：{notices.sync}</p>{/if}
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
        <TicketDetail ticket={detail} loading={detailLoading} error={detailError} {jev} {bub} {analysisLoading} {analysisError} {controls} onRerun={(kind: AIKind) => operate(kind)} />
      {:else}
        <TicketList {records} {loading} error={listError} bind:query bind:statusFilter bind:productFilter {statusOptions} {productOptions} onSearch={() => refresh()} />
      {/if}
    </main>
  </div>
</div>
