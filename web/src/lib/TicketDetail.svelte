<!-- input: Go 工单快照、同步元数据、独立 Jev/Bub 结果及加载错误。 -->
<!-- output: 工单字段、顺序会话、评估总结及消息引用定位。 -->
<!-- pos: 工单详情页面。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { ArrowLeft, Paperclip } from "@lucide/svelte";
  import { Button } from "$lib/components/ui/button/index.js";
  import { Badge } from "$lib/components/ui/badge/index.js";
  import * as Card from "$lib/components/ui/card/index.js";
  import { dateTime, syncLabel, type TicketRecord, type JevView, type BubView } from "./api";
  import JevAssessment from "./JevAssessment.svelte";
  import BubSummary from "./BubSummary.svelte";

  let { ticket, loading, error, jev, bub, analysisLoading, analysisError }: {
    ticket: TicketRecord | null; loading: boolean; error: string;
    jev: JevView | null; bub: BubView | null; analysisLoading: boolean; analysisError: string;
  } = $props();
  const roles: Record<string, string> = { customer: "客户", agent: "客服", unknown: "作者未知" };
  const visibility: Record<string, string> = { public: "公开", private: "内部", unknown: "可见性未知" };
  const messageStates: Record<string, string> = { DRAFT: "草稿，未发送", FAILED: "发送失败", PENDING: "等待发送" };
</script>

<section class="space-y-6" aria-label="工单详情" aria-busy={loading}>
  <Button href="#/tickets" variant="ghost" class="-ml-3 h-10"><ArrowLeft size={16} aria-hidden="true" />返回工单列表</Button>
  {#if loading}
    <p role="status" class="py-12 text-muted-foreground">正在读取工单详情…</p>
  {:else if ticket?.pending}
    <h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">工单 #{ticket.id}</h1>
    <Badge variant={ticket.sync_error ? "destructive" : "secondary"}>{syncLabel(ticket)}</Badge>
    <p class="text-muted-foreground">这张工单已进入采样范围，详情尚未保存。</p>
    {#if ticket.sync_error}<p role="alert" class="text-sm text-destructive">{ticket.sync_error}</p>{/if}
  {:else if error}
    <h1 id="page-title" tabindex="-1" class="text-2xl font-semibold">无法读取工单</h1>
    <p role="alert" class="text-sm text-destructive">{error}</p>
  {:else if ticket}
    <header class="space-y-3">
      <div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>工单 #{ticket.id}</span>
        <Badge variant="outline">{ticket.status || "状态未知"}</Badge>
        <Badge variant={ticket.sync_error ? "destructive" : "secondary"}>{syncLabel(ticket)}</Badge>
      </div>
      <h1 id="page-title" tabindex="-1" class="max-w-4xl text-2xl leading-relaxed font-semibold break-words">{ticket.subject || "无标题工单"}</h1>
      <p class="text-sm text-muted-foreground">更新于 {dateTime(ticket.modified_time)} · 时间按当前设备时区显示</p>
    </header>

    {#if ticket.sync_error}
      <div role="alert" class="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-4 text-sm">
        <p class="font-medium text-destructive">最近同步失败，以下为已保存的内容。</p>
        <p class="break-words text-muted-foreground">{ticket.sync_error}</p>
      </div>
    {/if}
    {#if !ticket.complete}
      <div role="status" class="rounded-md border bg-muted/40 p-4 text-sm">会话尚不完整，可能缺少部分沟通记录。请勿把当前内容视为完整证据。</div>
    {/if}
    {#if ticket.warnings?.length}
      <details class="rounded-md border p-4 text-sm">
        <summary class="cursor-pointer font-medium">采集提示（{ticket.warnings.length}）</summary>
        <ul class="mt-3 list-inside list-disc space-y-1 break-words text-muted-foreground">{#each ticket.warnings as warning}<li>{warning}</li>{/each}</ul>
      </details>
    {/if}

    <Card.Root>
      <Card.Header><Card.Title>工单信息</Card.Title><Card.Description>来自 Zoho 的已保存字段</Card.Description></Card.Header>
      <Card.Content class="space-y-5">
        <dl class="grid gap-4 text-sm sm:grid-cols-2 xl:grid-cols-4">
          <div><dt class="text-muted-foreground">产品</dt><dd class="mt-1 break-words">{ticket.product || "未提供"}</dd></div>
          <div><dt class="text-muted-foreground">Zoho 优先级</dt><dd class="mt-1">{ticket.priority || "未知"}</dd></div>
          <div><dt class="text-muted-foreground">部门 ID</dt><dd class="mt-1 break-all">{ticket.department_id}</dd></div>
          <div><dt class="text-muted-foreground">创建时间</dt><dd class="mt-1 tabular-nums">{dateTime(ticket.created_time)}</dd></div>
        </dl>
        <div class="border-t pt-4"><h2 class="mb-2 text-sm font-medium">工单描述</h2><p class="text-sm leading-7 whitespace-pre-wrap break-words">{ticket.description || "未提供描述。"}</p></div>
      </Card.Content>
    </Card.Root>

    <section aria-labelledby="conversation-title">
      <div class="mb-3 flex flex-wrap items-baseline gap-3"><h2 id="conversation-title" class="text-lg font-semibold">沟通记录</h2><span class="text-sm text-muted-foreground">{ticket.messages?.length || 0} 条 · 按时间顺序</span></div>
      <div class="divide-y rounded-lg border">
        {#each ticket.messages || [] as message (`${message.source}:${message.id}`)}
          <article id={`message-${message.source}-${message.id}`} tabindex="-1" class="scroll-mt-5 p-5 focus:outline-2 focus:outline-ring focus:-outline-offset-2 sm:p-6">
            <header class="mb-4 flex flex-wrap items-center gap-2 text-xs">
              <span class="text-sm font-semibold">{roles[message.role] || "作者未知"}</span>
              {#if message.source === "comment"}<Badge variant="secondary">{message.visibility === "private" ? "内部备注" : "备注"}</Badge>{/if}
              <Badge variant="outline">{visibility[message.visibility] || message.visibility || "可见性未知"}</Badge>
              {#if message.status}<Badge variant={messageStates[message.status.toUpperCase()] ? "secondary" : "outline"}>{messageStates[message.status.toUpperCase()] || message.status}</Badge>{/if}
              {#if message.is_description_thread}<Badge variant="outline">工单描述线程</Badge>{/if}
              {#if message.mixed_authors}<Badge variant="outline">聊天转录 · 混合发言者</Badge>{/if}
              <time datetime={message.created_time} class="text-muted-foreground tabular-nums sm:ml-auto">{dateTime(message.created_time)}</time>
            </header>
            {#if message.mixed_authors}<p class="mb-3 text-xs leading-relaxed text-muted-foreground">这段聊天可能包含双方发言，不能整体视为单一客户的陈述。</p>{/if}
            <p class="max-w-4xl text-sm leading-7 whitespace-pre-wrap break-words">{message.body || "正文为空或未返回。"}</p>
            {#if message.attachments?.length}
              <ul aria-label="附件元数据" class="mt-4 space-y-2 text-xs text-muted-foreground">
                {#each message.attachments as attachment}
                  <li class="flex items-start gap-2"><Paperclip size={14} class="mt-0.5 shrink-0" aria-hidden="true" /><span class="break-all">{attachment.name || "未命名附件"} · {attachment.size > 0 ? `${new Intl.NumberFormat("zh-CN").format(attachment.size)} 字节` : "大小未知"}{attachment.content_type ? ` · ${attachment.content_type}` : ""}</span></li>
                {/each}
              </ul>
            {/if}
            <footer class="mt-4 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
              <span class="break-all">{message.source} / {message.id || "消息 ID 未知"}</span>
              {#if message.direction}<span>方向：{message.direction}</span>{/if}
              {#if message.modified_time}<span>编辑于 {dateTime(message.modified_time)}</span>{/if}
            </footer>
          </article>
        {:else}<p class="p-6 text-sm text-muted-foreground">尚无已保存的沟通记录。</p>{/each}
      </div>
    </section>
    <JevAssessment view={jev} loading={analysisLoading} error={analysisError} />
    <BubSummary view={bub} messages={ticket.messages || []} loading={analysisLoading} error={analysisError} />
  {/if}
</section>
