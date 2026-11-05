<!-- input: Go 工单记录及附带 AI 状态、查询条件和查询回调。 -->
<!-- output: 状态/产品筛选、Zoho 工单号链接、三列 Jev 结果及紧凑对齐的 AI 状态。 -->
<!-- pos: 工单列表页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Search } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import * as Table from "$lib/components/ui/table/index.js";
  import { aiLabel, dateTime, jevChoice, syncLabel, ticketURL, type TicketRecord, type AIKind } from "./api";
  const kinds: AIKind[] = ["jev", "bub"];

  let { records, loading, error, query = $bindable(""), statusFilter = $bindable(""), productFilter = $bindable(""), statusOptions, productOptions, onSearch }: {
    records: TicketRecord[]; loading: boolean; error: string; query?: string;
    statusFilter?: string; productFilter?: string; statusOptions: string[]; productOptions: string[]; onSearch: () => void;
  } = $props();
  let showHistory = $state(false);
  const visibleRecords = $derived(records.filter((record) => showHistory || !record.sync_stopped));
</script>

<section aria-labelledby="page-title" class="space-y-6">
  <div>
    <h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">工单</h1>
    <p class="mt-2 text-sm text-muted-foreground">查看当前部门已选取的工单和沟通记录。</p>
  </div>

  <form class="flex flex-wrap items-end gap-3" onsubmit={(event) => { event.preventDefault(); onSearch(); }}>
    <div class="min-w-48 flex-1 space-y-2">
      <label for="ticket-query" class="text-sm font-medium">搜索标题</label>
      <Input id="ticket-query" class="h-10" placeholder="输入工单标题中的关键词" bind:value={query} />
    </div>
    <div class="w-full space-y-2 sm:w-44">
      <label for="status-filter" class="text-sm font-medium">工单状态</label>
      <select id="status-filter" bind:value={statusFilter} onchange={(event) => { statusFilter = event.currentTarget.value; onSearch(); }} class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <option value="">全部状态</option>
        {#each statusOptions as option}<option value={option}>{option}</option>{/each}
      </select>
    </div>
    <div class="w-full space-y-2 sm:w-44">
      <label for="product-filter" class="text-sm font-medium">产品</label>
      <select id="product-filter" bind:value={productFilter} onchange={(event) => { productFilter = event.currentTarget.value; onSearch(); }} class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <option value="">全部产品</option>
        {#each productOptions as option}<option value={option || "__missing__"}>{option || "未提供"}</option>{/each}
      </select>
    </div>
    <Button type="submit" disabled={loading} class="h-10"><Search size={16} aria-hidden="true" />查询</Button>
  </form>

  <label class="flex items-center gap-2 text-sm text-muted-foreground">
    <input type="checkbox" bind:checked={showHistory} class="size-4 accent-primary" />
    显示已合并、删除或移出部门的历史工单
  </label>

  <div aria-live="polite" class="text-sm text-muted-foreground">
    {loading ? "正在读取工单…" : `当前显示 ${visibleRecords.length} 张工单`}
  </div>
  {#if error}
    <p role="alert" class="rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">{error}</p>
  {/if}

  <div class="rounded-lg border" aria-busy={loading}>
    <Table.Root>
      <Table.Header>
        <Table.Row>
          <Table.Head class="min-w-64 pl-5">工单</Table.Head>
          <Table.Head>状态</Table.Head>
          <Table.Head class="min-w-32">产品</Table.Head>
          <Table.Head class="min-w-40">更新时间</Table.Head>
          <Table.Head>同步</Table.Head>
          <Table.Head class="min-w-24">情绪</Table.Head>
          <Table.Head class="min-w-20">优先级</Table.Head>
          <Table.Head class="min-w-24">问题分类</Table.Head>
          <Table.Head class="min-w-32 pr-5">AI 状态</Table.Head>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {#each visibleRecords as record (record.id)}
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
                  <Badge class="w-fit rounded px-1.5 py-0 text-[11px] whitespace-nowrap" variant={state?.status === "failed" && !record.sync_stopped ? "destructive" : "secondary"} title={state?.error || (state?.stale ? "已有结果过期，等待或正在处理当前会话" : undefined)}>{record.sync_stopped ? "已停止" : aiLabel(state)}</Badge>
                {/each}
              </div>
            </Table.Cell>
          </Table.Row>
        {:else}
          <Table.Row><Table.Cell colspan={9} class="h-36 text-center text-muted-foreground">
            {loading ? "正在加载…" : error ? "暂无可显示数据。" : query || statusFilter || productFilter ? "没有符合条件的工单，请调整搜索或状态。" : records.length ? "当前没有有效工单，可勾选查看历史工单。" : "尚未同步工单。同步完成后会显示在这里。"}
          </Table.Cell></Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  </div>
</section>
