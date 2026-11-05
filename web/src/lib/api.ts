// input: 同源 Go HTTP API 的 JSON 响应及内存中的会话防护令牌。
// output: 工单/筛选契约、自动退避与提前重试候选、带防护的同源请求能力。
// pos: 浏览器与 Go 的数据边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
export type Attachment = {
  id: string;
  name: string;
  content_type?: string;
  size: number;
};

export type Message = {
  id: string;
  source: string;
  created_time: string;
  modified_time?: string;
  role: string;
  visibility: string;
  status?: string;
  direction?: string;
  is_description_thread: boolean;
  mixed_authors?: boolean;
  body: string;
  attachments?: Attachment[];
};

export type Ticket = {
  id: string;
  subject: string;
  description: string;
  department_id: string;
  status: string;
  priority: string;
  product: string;
  created_time: string;
  modified_time: string;
  messages: Message[];
  complete: boolean;
  warnings?: string[];
};

export type MessageSegment = { text?: string; quote?: boolean; children?: MessageSegment[] };

export type TicketRecord = Ticket & {
  message_segments?: Record<string, MessageSegment[]>;
  ticket_number?: string;
  web_url?: string;
  first_incoming_time?: string;
  sync_status?: string;
  pending: boolean;
  sync_stopped: boolean;
  sync_error?: string;
  checked_at?: string;
  ai?: Record<AIKind, AIState>;
};

export type AIKind = "jev" | "bub";
export type Operation = "sync" | AIKind;
export type AIState = {
  status: "waiting" | "pending" | "running" | "failed" | "success" | "disabled";
  stale: boolean;
  automatic_eligible?: boolean;
  next_retry_at?: string;
  last_failed_at?: string;
  failure_before_start?: boolean;
  waiting_reason?: string;
  priority?: string;
  emotion?: string;
  category?: string;
  error?: string;
};
type AICounts = { pending: number; running: number; failed: number; success: number; stale: number; retry_waiting?: number };

export type RetryCandidate = { ticket_id: string; run_id: number };

export type SystemStatus = {
  retry_candidates?: Record<AIKind, RetryCandidate[]>;
  sync: {
    department_id: string;
    discovery_offset?: number;
    discovery_done?: boolean;
    selected: number;
    ready: number;
    failed: number;
    watermark: string;
    batch_upper?: string;
    last_attempt: string;
    last_error?: string;
  };
  jev_enabled: boolean;
  bub_enabled: boolean;
  wecom_enabled: boolean;
  csrf_token: string;
  runtime: {
    sync_running: boolean;
    sync_start_time?: string;
    next_sync_at?: string;
    stopped_count?: number;
    last_sync_batch?: { fetched: number; failed: number; stopped: number; remaining: number };
    jev_running: boolean;
    bub_running: boolean;
    automatic: boolean;
    started_at?: string;
    recovery_pending?: Record<AIKind, number>;
    ai_scope: string;
    ai_scope_count?: number;
    sync_batch_size?: number;
    sync_concurrency?: number;
    last_error: Partial<Record<Operation, string>>;
  };
  budget: { total: number; round: number };
  ai: Record<AIKind, AICounts>;
};

export type AnalysisRun<Result> = {
  id: number;
  ticket_id: string;
  kind: string;
  input_hash: string;
  schema_version: string;
  requested_model: string;
  returned_model?: string;
  status: string;
  started_at: string;
  completed_at?: string;
  error?: string;
  result?: Result;
};

type AnalysisView<Result> = { current: AnalysisRun<Result> | null; success: AnalysisRun<Result> | null; stale: boolean; waiting_for?: "bub"; waiting_reason?: string; next_retry_at?: string; last_failed_at?: string; failure_before_start?: boolean };
export type JevView = AnalysisView<{ model: string; answers: Record<string, { type: string; choice: string }> }>;

type SummaryFact = { text: string; message_ids: string[] };
export type BubSummaryData = {
  device_info: SummaryFact[];
  customer_feedback: SummaryFact[];
  support_responses: SummaryFact[];
};
// Old summaries stay in storage; their mixed categories must not be relabelled.
type LegacyBubSummary = { current_situation: SummaryFact };
export type BubView = AnalysisView<{ summary: BubSummaryData | LegacyBubSummary; request_id?: string }>;

export async function getJSON<T>(path: string, signal: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal, headers: { Accept: "application/json" } });
  if (!response.ok) {
    throw new Error(response.status === 404 ? "工单尚未保存或不存在。请返回列表查看同步状态。" : `读取失败（HTTP ${response.status}），请稍后重试。`);
  }
  if (!response.headers.get("content-type")?.includes("application/json")) {
    throw new Error("无法读取服务数据，请确认本机 Go 服务正在运行。");
  }
  return response.json() as Promise<T>;
}

export async function postAction(path: string, token: string, signal: AbortSignal, body?: unknown): Promise<void> {
  const response = await fetch(path, { method: "POST", signal, body: body === undefined ? undefined : JSON.stringify(body), headers: { "X-CSRF-Token": token, Accept: "application/json", "Content-Type": "application/json" } });
  const result: { status?: string; error?: string } = response.headers.get("content-type")?.includes("application/json") ? await response.json() : {};
  if (response.status === 202 && result.status === "accepted") return;
  if (response.status === 409) throw new Error(result.error && result.error !== "operation already running" ? result.error : "操作已在运行，请等待完成。");
  throw new Error(typeof result.error === "string" ? result.error : `操作未受理（HTTP ${response.status}），请刷新后重试。`);
}

export function aiLabel(value?: AIState): string {
  if (!value) return "未读取";
  if (value.status === "disabled") return "已关闭";
  if (value.status === "waiting") return "等待总结";
  if (value.status === "running") return value.stale ? "更新中" : "处理中";
  if (value.status === "failed" && value.next_retry_at) return "等待重试";
  if (value.status === "failed") return value.stale ? "更新失败" : "失败";
  if (value.stale) return "待更新";
  if (value.status === "pending") return value.automatic_eligible === false ? "等待同步" : "排队中";
  return value.status === "success" ? "完成" : "未知";
}

export function ticketURL(value?: string): string | null {
  try {
    const url = new URL(value || "");
    return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? url.href : null;
  } catch { return null; }
}

export type ActionFeedback = { blocked: string; error: string; notice: string };

export function jevChoice(field: "priority" | "emotion" | "category", value?: string): string {
  const labels: Record<string, Record<string, string>> = {
    category: { hardware: "硬件", software: "软件 / 固件", connectivity: "连接", usage: "使用方法", order: "订单", service: "售后服务", other: "其他" },
    priority: { low: "低", normal: "常规", high: "高", urgent: "紧急" },
    emotion: { positive: "积极", neutral: "中性", frustrated: "沮丧 / 焦虑", angry: "愤怒" },
  };
  return labels[field][value || ""] || "未知";
}

export function dateTime(value?: string): string {
  if (!value) return "尚无记录";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "时间未知";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export type FilterKey = "status" | "product" | "sync_status" | "emotion" | "priority";
export type FilterChoice = { value: string; count: number };
export type FilterOptions = Partial<Record<FilterKey, FilterChoice[]>>;
export type FilterSelection = Record<FilterKey, string>;
export type SearchFilters = { q: string; field: string; dateField: string; startDate: string; endDate: string };
export const defaultSearch = (): SearchFilters => ({ q: "", field: "all", dateField: "first_incoming", startDate: "", endDate: "" });
export const emptyFilters = (): FilterSelection => ({ status: "", product: "", sync_status: "", emotion: "", priority: "" });

export function filterLabel(key: FilterKey, value: string): string {
  if (key === "emotion" || key === "priority") return jevChoice(key, value);
  if (key === "sync_status") return ({ synced: "已同步", pending: "待同步", failed: "同步失败，等待重试", incomplete: "会话不完整", merged: "已合并", deleted: "Zoho 已删除", stopped: "已停止同步" } as Record<string, string>)[value] || "未知";
  return value === "__missing__" ? (key === "product" ? "未提供" : "未知") : value;
}

export function syncLabel(record: TicketRecord): string {
  return filterLabel("sync_status", record.sync_status || "");
}

export function failureHint(value: { next_retry_at?: string; last_failed_at?: string; failure_before_start?: boolean }): string {
  const retry = value.next_retry_at ? `自动重试时间：${dateTime(value.next_retry_at)}。` : "";
  const time = value.last_failed_at ? `${value.failure_before_start ? "本次启动前的历史失败" : "最近失败"}：${dateTime(value.last_failed_at)}。` : "";
  return retry + time;
}
