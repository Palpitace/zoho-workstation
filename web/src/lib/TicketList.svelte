<!-- input: Go 工单记录及附带 AI 状态、查询条件和查询回调。 -->
<!-- output: 可搜索筛选的列表、Jev 建议及独立同步和分析状态。 -->
<!-- pos: 工单列表页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Search } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Input } from "$lib/components/ui/input/index.js";
  import * as Table from "$lib/components/ui/table/index.js";
  import { aiLabel, dateTime, jevChoice, syncLabel, type TicketRecord, type AIKind } from "./api";
  const kinds: AIKind[] = ["jev", "bub"];

  let { records, loading, error, query = $bindable(""), statusFilter = $bindable(""), statusOptions, onSearch }: {
    records: TicketRecord[]; loading: boolean; error: string; query?: string;
    statusFilter?: string; statusOptions: string[]; onSearch: () => void;
  } = $props();
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
      <select id="status-filter" bind:value={statusFilter} class="h-10 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <option value="">全部状态</option>
        {#each statusOptions as option}<option value={option}>{option}</option>{/each}
      </select>
    </div>
    <Button type="submit" disabled={loading} class="h-10"><Search size={16} aria-hidden="true" />查询</Button>
  </form>

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
          <Table.Head>状态</Table.Head>
          <Table.Head class="min-w-32">产品</Table.Head>
          <Table.Head class="min-w-40">更新时间</Table.Head>
          <Table.Head>同步</Table.Head>
          <Table.Head class="min-w-36">Jev 建议</Table.Head>
          <Table.Head class="min-w-44 pr-5">AI 处理状态</Table.Head>
        </Table.Row>
      </Table.Header>
      <Table.Body>
        {#each records as record (record.id)}
          <Table.Row>
            <Table.Cell class="max-w-xl py-4 pl-5 whitespace-normal">
              <a href={`#/tickets/${encodeURIComponent(record.id)}`} class="inline-block rounded-sm font-medium leading-relaxed break-words underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {record.subject || (record.pending ? "等待获取工单详情" : "无标题工单")}
              </a>
              <div class="mt-1 text-xs text-muted-foreground">#{record.id}</div>
            </Table.Cell>
            <Table.Cell><Badge variant="outline">{record.status || "未知"}</Badge></Table.Cell>
            <Table.Cell class="max-w-52 whitespace-normal break-words text-muted-foreground">{record.product || "未提供"}</Table.Cell>
            <Table.Cell class="text-sm text-muted-foreground tabular-nums">{dateTime(record.modified_time)}</Table.Cell>
            <Table.Cell><Badge variant={record.sync_error ? "destructive" : "secondary"}>{syncLabel(record)}</Badge></Table.Cell>
            <Table.Cell class="space-y-1 text-xs"><div>优先级：{jevChoice("priority", record.ai?.jev.priority)}</div><div>情绪：{jevChoice("emotion", record.ai?.jev.emotion)}</div></Table.Cell>
            <Table.Cell class="space-y-2 pr-5">
              {#each kinds as kind}
                <div class="flex flex-wrap items-center gap-2 text-xs"><span>{kind === "jev" ? "Jev" : "Bub"}</span><Badge variant={record.ai?.[kind].status === "failed" ? "destructive" : "secondary"}>{aiLabel(record.ai?.[kind])}</Badge>{#if record.ai?.[kind].stale}<Badge variant="outline">过期</Badge>{/if}</div>
              {/each}
            </Table.Cell>
          </Table.Row>
        {:else}
          <Table.Row><Table.Cell colspan={7} class="h-36 text-center text-muted-foreground">
            {loading ? "正在加载…" : error ? "暂无可显示数据。" : query || statusFilter ? "没有符合条件的工单，请调整搜索或状态。" : "尚未同步工单。同步完成后会显示在这里。"}
          </Table.Cell></Table.Row>
        {/each}
      </Table.Body>
    </Table.Root>
  </div>
</section>
