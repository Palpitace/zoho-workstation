// input: 同源知识 API、内存中的 CSRF 令牌和用户选择的文件或草稿。
// output: 知识来源、独立建议任务状态及受保护的导入/发布请求。
// pos: 知识界面与 Go 的契约边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import type { AnalysisRun, TaskState } from "./api";

export type KnowledgeBlock = { id: string; text: string };
export type KnowledgeEntry = {
  id: number; replaces_id?: number; kind: "case" | "sop"; status: "draft" | "published" | "archived";
  title: string; product: string; source_name: string; source_hash: string; source_ticket_id?: string;
  message_ids: string[]; blocks: KnowledgeBlock[]; created_at: string; published_at?: string;
};
export type KnowledgeSource = {
  id: string; entry_id: number; block_id: string; title: string; kind: "case" | "sop";
  product: string; text: string; ticket_id?: string; source_hash: string; message_ids: string[];
};
export type AdviceData = {
  steps: { action: string; reason: string; source_ids: string[]; current_message_ids: string[] }[];
  missing_information: string[]; conflicts: string[];
};
type KnowledgeRunView<Result> = {
  task_state?: TaskState; attempts?: number;
  current: AnalysisRun<Result> | null; success: AnalysisRun<Result> | null; stale: boolean;
  waiting_reason?: string; next_retry_at?: string; last_failed_at?: string;
};
export type TicketKnowledge = {
  case: KnowledgeRunView<unknown>;
  advice: KnowledgeRunView<{ advice: AdviceData; sources: KnowledgeSource[] }>;
  enabled: boolean; busy: boolean; automatic_eligible?: boolean;
};

export async function knowledgeJSON<T>(path: string, signal: AbortSignal, token?: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const form = body instanceof FormData;
  if (token !== undefined) headers["X-CSRF-Token"] = token;
  if (body !== undefined && !form) headers["Content-Type"] = "application/json";
  const response = await fetch(path, { signal, method: token === undefined ? "GET" : "POST", headers,
    body: form ? body : body === undefined ? undefined : JSON.stringify(body) });
  if (!response.headers.get("content-type")?.includes("application/json")) throw new Error("无法读取服务数据，请确认本机服务正在运行。");
  const result = await response.json();
  if (!response.ok) throw new Error(typeof result?.error === "string" ? result.error : `请求失败（HTTP ${response.status}），请刷新后重试。`);
  return result as T;
}

export function knowledgeStatus(entry: KnowledgeEntry): string {
  if (entry.status === "archived") return "已停用";
  if (entry.status === "draft") return "待确认";
  return entry.kind === "case" ? "参考案例" : "已发布";
}
export function knowledgeLink(entryID: number, blockID?: string): string {
  return `#/knowledge/${entryID}${blockID ? `?block=${encodeURIComponent(blockID)}` : ""}`;
}
