// input: 同源 Go HTTP API 的 JSON 响应及内存中的会话防护令牌。
// output: 工单号/安全链接、分类与含等待总结的紧凑分析状态及调用计数类型、可取消查询和受保护操作。
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

export type TicketRecord = Ticket & {
  ticket_number?: string;
  web_url?: string;
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
  priority?: string;
  emotion?: string;
  category?: string;
  error?: string;
};
type AICounts = { pending: number; running: number; failed: number; success: number; stale: number };

export type SystemStatus = {
  sync: {
    department_id: string;
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
    jev_running: boolean;
    bub_running: boolean;
    automatic: boolean;
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

type AnalysisView<Result> = { current: AnalysisRun<Result> | null; success: AnalysisRun<Result> | null; stale: boolean; waiting_for?: "bub" };
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

export async function postAction(path: string, token: string, signal: AbortSignal): Promise<void> {
  const response = await fetch(path, { method: "POST", signal, headers: { "X-CSRF-Token": token, Accept: "application/json" } });
  const body: { status?: string; error?: string } = response.headers.get("content-type")?.includes("application/json") ? await response.json() : {};
  if (response.status === 202 && body.status === "accepted") return;
  if (response.status === 409) throw new Error("操作已在运行，请等待完成。");
  throw new Error(typeof body.error === "string" ? body.error : `操作未受理（HTTP ${response.status}），请刷新后重试。`);
}

export function aiLabel(value?: AIState): string {
  if (!value) return "未读取";
  if (value.status === "disabled") return "已关闭";
  if (value.status === "waiting") return "等待总结";
  if (value.status === "running") return value.stale ? "更新中" : "处理中";
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

export function syncLabel(record: TicketRecord): string {
  if (record.sync_stopped && record.sync_error === "ticket confirmed merged in Zoho") return "已合并";
  if (record.sync_stopped) return record.sync_error === "ticket confirmed deleted in Zoho" ? "Zoho 已删除" : "已停止同步";
  if (record.sync_error) return "同步失败，等待重试";
  if (record.pending) return "待同步";
  if (!record.complete) return "会话不完整";
  return "已同步";
}
