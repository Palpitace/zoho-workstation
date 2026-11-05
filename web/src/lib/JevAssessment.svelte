<!-- input: Jev 结果、独立操作反馈和重新评估回调。 -->
<!-- output: 区分等待总结/未知/失败/过期的评估及重新评估入口，无结果来源面板或说明注脚。 -->
<!-- pos: 工单详情中的 Jev 展示边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。 -->
<script lang="ts">
  import { Badge } from "$lib/components/ui/badge/index.js";
  import { Button } from "$lib/components/ui/button/index.js";
  import { type JevView, type ActionFeedback } from "./api";
  let { view, loading, error, control, onRerun }: { view: JevView | null; loading: boolean; error: string; control: ActionFeedback; onRerun: () => void } = $props();
  const fields = [
    ["priority", "建议优先级"], ["severity", "问题严重程度"],
    ["emotion", "近期客户情绪"], ["emotion_intensity", "情绪强度"],
    ["category", "问题分类"], ["safety", "安全风险线索"],
    ["information", "信息是否足够"], ["impact_scope", "影响范围线索"],
  ];
  const requests = [["refund", "退款"], ["replacement", "换货"], ["repair", "维修 / 保修"], ["how_to", "技术帮助"], ["order_status", "订单进度"], ["other", "其他诉求"]];
  const choices: Record<string, string> = {
    unknown: "未知 / 信息不足", low: "低", normal: "常规", high: "高", urgent: "紧急", medium: "中",
    minor: "轻微", moderate: "部分功能受影响", major: "核心使用受阻", critical: "报告严重安全风险",
    positive: "积极", neutral: "中性", frustrated: "沮丧 / 焦虑", angry: "愤怒",
    hardware: "硬件", software: "软件 / 固件", connectivity: "连接", usage: "使用方法", order: "订单", service: "售后服务", other: "其他",
    reported_risk: "客户报告了具体风险", no_explicit_clue: "未见明确风险线索",
    sufficient: "可进行初步评估", insufficient: "关键事实缺失",
    single_report: "单一设备 / 情况报告", multiple_reported: "客户报告多个设备 / 情况，待核实",
    yes: "明确提出", no: "未见明确表达",
  };
  const answers = $derived(view?.success?.result?.answers);
  const failed = $derived(view?.current?.status === "failed" || view?.current?.status === "invalid");
  function answer(key: string) {
    if (key === "emotion_intensity" && (!answers?.emotion?.choice || answers.emotion.choice === "unknown")) return "待确认";
    return choices[answers?.[key]?.choice || "unknown"] || choices.unknown;
  }
</script>

<section aria-labelledby="jev-title" class="space-y-4 border-t pt-6" aria-busy={loading}>
  <header class="flex flex-wrap items-center gap-3">
    <h2 id="jev-title" class="text-lg font-semibold">Jev 结构化评估</h2>
    {#if view?.waiting_for === "bub"}<Badge variant="outline">等待总结</Badge>
    {:else if failed}<Badge variant="destructive">{view?.current?.status === "invalid" ? "无法评估" : "最近评估失败"}</Badge>
    {:else if view?.current?.status === "running"}<Badge variant="secondary">评估中</Badge>
    {:else if view?.success}<Badge variant="secondary">已有评估</Badge>
    {:else if !loading && !error}<Badge variant="outline">待评估</Badge>{/if}
    {#if view?.stale}<Badge variant="outline">结果已过期</Badge>{/if}
    <Button variant="outline" class="h-10 sm:ml-auto" onclick={onRerun} disabled={!!control.blocked}>重新评估</Button>
  </header>
  {#if control.blocked}<p class="text-xs text-muted-foreground">重新评估暂不可用：{control.blocked}</p>{/if}
  {#if control.error}<p role="alert" class="text-sm text-destructive">重新评估：{control.error}</p>{/if}
  {#if control.notice}<p role="status" class="text-sm text-muted-foreground">{control.notice}</p>{/if}
  {#if loading}<p role="status" class="text-sm text-muted-foreground">正在读取评估…</p>{/if}
  {#if error}<p role="alert" class="text-sm text-destructive">评估暂不可读：{error}</p>{/if}
  {#if view?.current?.error}<p role="alert" class="text-sm break-words text-destructive">{view.current.error}</p>{/if}
  {#if view?.stale}<p class="text-sm text-muted-foreground">以下是旧输入或旧配置对应的成功结果，不代表当前会话。</p>
  {:else if view?.success && failed}<p class="text-sm text-muted-foreground">保留上次成功评估，最近失败未覆盖已有结果。</p>{/if}
  {#if view?.success && answers}
    <dl class="grid gap-x-8 gap-y-5 rounded-lg border p-5 text-sm sm:grid-cols-2 xl:grid-cols-4">
      {#each fields as [key, label]}<div><dt class="text-muted-foreground">{label}</dt><dd class="mt-1 font-medium leading-relaxed">{answer(key)}</dd></div>{/each}
    </dl>
    <div><h3 class="mb-3 text-sm font-medium">客户明确诉求</h3><dl class="grid gap-3 text-sm sm:grid-cols-2 xl:grid-cols-3">
      {#each requests as [key, label]}<div class="flex flex-wrap gap-x-2"><dt class="text-muted-foreground">{label}</dt><dd>{answer(`request_${key}`)}</dd></div>{/each}
    </dl></div>
  {:else if !loading && !error}<p class="text-sm text-muted-foreground">尚无成功评估。未知、失败或信息不足不会显示为低风险。</p>{/if}
</section>
