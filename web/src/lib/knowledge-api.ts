// input: 同源知识 API、内存中的 CSRF 令牌和用户选择的文件或草稿。
// output: 具名知识库、Markdown 版本、导入预览、检索与有限正文对照、独立建议状态及受保护的管理请求。
// pos: 知识界面与 Go 的契约边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import type { AnalysisRun } from "./api";

export type KnowledgeScope = "technical" | "policy" | "reference";
export type KnowledgeBlock = { id: string; text: string };
export type KnowledgeEntry = {
  id: number; library_id?: number; category_id?: number; markdown?: string; applicable_products?: string[]; excluded_products?: string[]; review_flags?: string[]; replaces_id?: number; scope: KnowledgeScope; source_tickets: string[]; lab_batch_id?: number; kind: "case" | "sop"; status: "draft" | "published" | "archived";
  title: string; product: string; source_name: string; source_hash: string; source_ticket_id?: string;
  message_ids: string[]; blocks: KnowledgeBlock[]; created_at: string; published_at?: string;
};
export type KnowledgeSource = {
  id: string; entry_id: number; block_id: string; title: string; scope: KnowledgeScope; kind: "case" | "sop";
  product: string; text: string; ticket_id?: string; source_hash: string; message_ids: string[];
};
export type AdviceData = {
  steps: { action: string; reason: string; source_ids: string[]; current_message_ids: string[] }[];
  missing_information: string[]; conflicts: string[];
};
type KnowledgeRunView<Result> = {
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


export function knowledgeScope(scope: KnowledgeScope): string {
  return { technical: "技术知识", policy: "售后政策", reference: "参考资料" }[scope] || "参考资料";
}

// Bounded line comparison; larger documents use complete side-by-side text.
export function knowledgeDiff(previous: string, current: string): { kind: "same" | "removed" | "added"; text: string }[] | null {
  const a = previous.split("\n"), b = current.split("\n");
  if (a.length > 400 || b.length > 400 || previous.length + current.length > 64000) return null;
  const width = b.length + 1, table = new Uint16Array((a.length + 1) * width);
  for (let i = a.length - 1; i >= 0; i--) for (let j = b.length - 1; j >= 0; j--)
    table[i * width + j] = a[i] === b[j] ? table[(i + 1) * width + j + 1] + 1 : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
  const result: { kind: "same" | "removed" | "added"; text: string }[] = [];
  let i = 0, j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) { result.push({ kind: "same", text: a[i++] }); j++; }
    else if (i < a.length && (j === b.length || table[(i + 1) * width + j] >= table[i * width + j + 1])) result.push({ kind: "removed", text: a[i++] });
    else result.push({ kind: "added", text: b[j++] });
  }
  return result;
}

export type KnowledgeLibrary = { id: number; name: string; department_ids: string[] };
export type KnowledgeCategory = { id: number; library_id: number; name: string };
export type ImportItem = { id: string; title: string; category: string; scope: KnowledgeScope; markdown: string; product?: string; applicable_products?: string[]; excluded_products?: string[]; status: "ready" | "duplicate" | "failed"; warnings: string[]; requires_confirmation: boolean; error?: string };
export type ImportBatch = { id: string; items: ImportItem[] };
export type EmbeddingStatus = { state: string; progress?: number; error?: string; indexed?: number; total?: number; degraded?: boolean };
export function knowledgeMarkdown(entry: KnowledgeEntry): string { return entry.markdown || entry.blocks.map((block) => block.text).join("\n\n"); }

export function knowledgeReviewLabel(flag: string): string {
  return ({ policy_confirmation_required: "政策内容需确认", internal_reference_only: "仅供内部参考", date_or_release_channel_review: "核对生效日期或软件版本", regional_condition_conflict: "地区条件存在差异", comment_requires_review: "核对图片识别文字", external_images_pending: "引用图片待下载", internal_or_policy_review: "核对内部升级要求或政策", model_applicability_review: "核对机型适用条件", cross_document_model_conflict: "不同文档的机型步骤有差异", model_list_non_exhaustive: "机型列表可能不完整", platform_macos: "适用于 macOS", platform_windows: "适用于 Windows", embedded_content_not_extracted: "内嵌内容未完整提取", empty_pdf_pages: "PDF 包含未提取文字的页面", additional_sections_not_extracted: "附加章节未完整提取", conversion_empty: "未提取到正文" } as Record<string, string>)[flag] || flag;
}
