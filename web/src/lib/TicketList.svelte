<!-- input: Go 工单记录及附带 AI 状态、查询条件和查询回调。 -->
<!-- output: AI 重试原因与失败时间提示； 顶部字段/日期搜索、五列 Combobox 筛选和一键清除，以及工单与 AI 状态列表。 -->
<!-- pos: 工单列表页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Search, RotateCcw } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import FilterCombobox from "./FilterCombobox.svelte";
  import * as Table from "$lib/components/ui/table/index.js";
  import { aiLabel, failureHint, dateTime, jevChoice, syncLabel, ticketURL, type TicketRecord, type AIKind, type SearchFilters, type FilterSelection, type FilterOptions, type FilterKey } from "./api";
  const kinds: AIKind[] = ["jev", "bub"];

  let { records, loading, error, hasAppliedFilters, search = $bindable(), showHistory = $bindable(false), filters, filterOptions, onSearch, onFilter, onClear, onHistory }: {
    records: TicketRecord[]; loading: boolean; error: string; hasAppliedFilters: boolean; search: SearchFilters; showHistory?: boolean;
    filters: FilterSelection; filterOptions: FilterOptions; onSearch: () => void;
    onFilter: (key: FilterKey, value: string) => void; onClear: () => void; onHistory: () => void;
  } = $props();
  const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm shadow-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";
</script>

<section aria-labelledby="page-title" class="space-y-6">
  <div>
    <h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">工单</h1>
    <p class="mt-2 text-sm text-muted-foreground">查看当前部门已选取的工单和沟通记录。</p>
  </div>

  <form aria-label="组合搜索工单" class="flex flex-wrap items-center gap-2" onsubmit={(event) => { event.preventDefault(); onSearch(); }}>
    <select aria-label="搜索字段" bind:value={search.field} class={selectClass}>
      <option value="all">全部字段</option><option value="subject">工单标题</option><option value="ticket_number">工单号</option><option value="id">工单 ID</option><option value="product">产品/机型</option>
    </select>
    <Input aria-label="搜索关键词" placeholder="标题 / 工单号 / ID / 机型" bind:value={search.q} class="h-9 w-64 min-w-40 flex-1" />
    <select aria-label="日期字段" bind:value={search.dateField} class={selectClass}>
      <option value="first_incoming">首份来信时间</option><option value="created">工单创建时间</option><option value="updated">最近更新时间</option>
    </select>
    <div class="flex items-center gap-2">
      <Input type="date" aria-label="开始日期" bind:value={search.startDate} max={search.endDate || undefined} class="h-9 w-36" />
      <span class="text-muted-foreground">至</span>
      <Input type="date" aria-label="结束日期" bind:value={search.endDate} min={search.startDate || undefined} class="h-9 w-36" />
    </div>
    <Button type="submit" size="sm" disabled={loading}><Search size={14} aria-hidden="true" />查询</Button>
    <Button type="button" variant="ghost" size="sm" class="ml-auto" onclick={onClear}><RotateCcw size={14} aria-hidden="true" />清除筛选</Button>
  </form>

  <label class="flex items-center gap-2 text-sm text-muted-foreground">
    <input type="checkbox" bind:checked={showHistory} onchange={(event) => { showHistory = event.currentTarget.checked; onHistory(); }} class="size-4 accent-primary" />
    显示已合并、删除或移出部门的历史工单
  </label>

  <div aria-live="polite" class="text-sm text-muted-foreground">
    {loading ? "正在读取工单…" : `当前显示 ${records.length} 张工单`}
  </div>
  {#if error}
    <p role="alert" class="rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">{error}</p>
  {/if}

  <div class="rounded-lg border" aria-busy={loading}>
    <Table.Root>
      <Table.Header>
        <Table.Row>
          <Table.Head class="min-w-64 pl-5">工单</Table.Head>
          <Table.Head><FilterCombobox label="状态" field="status" value={filters.status} options={filterOptions.status || []} onSelect={(value) => onFilter("status", value)} /></Table.Head>
          <Table.Head><FilterCombobox label="产品" field="product" value={filters.product} options={filterOptions.product || []} onSelect={(value) => onFilter("product", value)} /></Table.Head>
          <Table.Head class="min-w-40">更新时间</Table.Head>
          <Table.Head><FilterCombobox label="同步" field="sync_status" value={filters.sync_status} options={filterOptions.sync_status || []} onSelect={(value) => onFilter("sync_status", value)} /></Table.Head>
          <Table.Head><FilterCombobox label="情绪" field="emotion" value={filters.emotion} options={filterOptions.emotion || []} onSelect={(value) => onFilter("emotion", value)} /></Table.Head>
          <Table.Head><FilterCombobox label="优先级" field="priority" value={filters.priority} options={filterOptions.priority || []} onSelect={(value) => onFilter("priority", value)} /></Table.Head>
          <Table.Head class="min-w-24">问题分类</Table.Head>
          <Table.Head class="min-w-32 pr-5">AI 状态</Table.Head>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {#each records as record (record.id)}
          {@const link = ticketURL(record.web_url)}
          <Table.Row>
            <Table.Cell class="max-w-xl py-4 pl-5 whitespace-normal">
              <a href={`#/tickets/${encodeURIComponent(record.id)}`} class="inline-block rounded-sm font-medium leading-relaxed break-words underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {record.subject || (record.pending ? "等待获取工单详情" : "无标题工单")}
              </a>
              <div class="mt-1 text-xs text-muted-foreground">
                {#if link}
                  <a href={link} target="_blank" rel="noopener noreferrer" class="underline-offset-4 hover:underline" aria-label={record.ticket_number ? `在 Zoho 打开工单 ${record.ticket_number}` : "在 Zoho 打开工单"}>{record.ticket_number ? `工单 #${record.ticket_number}` : "打开 Zoho 工单"} ↗</a>
                {:else}{record.ticket_number ? `工单 #${record.ticket_number}` : "工单号待同步"}{/if}
              </div>
            </Table.Cell>
            <Table.Cell><Badge variant="outline">{record.status || "未知"}</Badge></Table.Cell>
            <Table.Cell class="max-w-52 whitespace-normal break-words text-muted-foreground">{record.product || "未提供"}</Table.Cell>
            <Table.Cell class="text-sm text-muted-foreground tabular-nums">{dateTime(record.modified_time)}</Table.Cell>
            <Table.Cell><Badge variant={record.sync_error && !record.sync_stopped ? "destructive" : "secondary"}>{syncLabel(record)}</Badge></Table.Cell>
            <Table.Cell class="text-xs">{jevChoice("emotion", record.ai?.jev.emotion)}</Table.Cell>
            <Table.Cell class="text-xs">{jevChoice("priority", record.ai?.jev.priority)}</Table.Cell>
            <Table.Cell class="text-xs">{jevChoice("category", record.ai?.jev.category)}</Table.Cell>
            <Table.Cell class="pr-5">
              <div class="grid grid-cols-[2rem_minmax(0,1fr)] items-center gap-x-2 gap-y-1 text-xs" aria-label="AI 处理状态">
                {#each kinds as kind}
                  {@const state = record.ai?.[kind]}
                  <span class="text-muted-foreground">{kind === "jev" ? "Jev" : "Bub"}</span>
                  <Badge class="w-fit rounded px-1.5 py-0 text-[11px] whitespace-nowrap" variant={state?.status === "failed" && !record.sync_stopped ? "destructive" : "secondary"} title={state ? [state.waiting_reason, state.status === "failed" ? failureHint(state) : "", state.error, state.stale ? "已有结果过期，等待或正在处理当前会话" : ""].filter(Boolean).join("\n") : undefined}>{record.sync_stopped ? "已停止" : aiLabel(state)}</Badge>
                {/each}
              </div>
            </Table.Cell>
          </Table.Row>
        {:else}
          <Table.Row><Table.Cell colspan={9} class="h-36 text-center text-muted-foreground">
            {loading ? "正在加载…" : error ? "暂无可显示数据。" : hasAppliedFilters ? "没有符合条件的工单，请调整搜索或表头筛选。" : "当前没有可显示的工单。可检查同步状态，或勾选查看历史工单。"}
          </Table.Cell></Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  </div>
</section>
