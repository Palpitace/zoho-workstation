// input: 同源 Go HTTP API 的 JSON 响应。
// output: 工单、消息、独立分析结果及运行状态类型和可取消的只读请求。
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
  pending: boolean;
  sync_error?: string;
  checked_at?: string;
};

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

type AnalysisView<Result> = { current: AnalysisRun<Result> | null; success: AnalysisRun<Result> | null; stale: boolean };
export type JevView = AnalysisView<{ model: string; answers: Record<string, { type: string; choice: string }> }>;

type SummaryFact = { text: string; message_ids: string[] };
export type BubSummaryData = {
  current_situation: SummaryFact;
  key_progress: SummaryFact[];
  attempted_measures: {
    action: string;
    execution_status: "suggested" | "customer_confirmed_executed" | "unknown";
    result: string;
    result_status: "customer_reported" | "unknown";
    message_ids: string[];
  }[];
  blockers: SummaryFact[];
  next_steps: (SummaryFact & { basis: "recommendation" | "hypothesis" })[];
};
export type BubView = AnalysisView<{ summary: BubSummaryData; request_id?: string }>;

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

export function dateTime(value?: string): string {
  if (!value) return "尚无记录";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "时间未知";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

export function syncLabel(record: TicketRecord): string {
  if (record.sync_error) return "同步失败";
  if (record.pending) return "待同步";
  if (!record.complete) return "会话不完整";
  return "已同步";
}
