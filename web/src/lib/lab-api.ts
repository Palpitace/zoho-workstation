// input: 离线批次 API 返回的固定输入、模型结果和人工反馈。
// output: 实验页面使用的类型、状态标签与同源请求。
// pos: 离线实验前端契约。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
import type { KnowledgeEntry, KnowledgeSource } from "./knowledge-api";
export { knowledgeJSON as labJSON } from "./knowledge-api";
export type LabMessage = { id: string; body: string; role: string; created_time: string; visibility: string };
export type LabSnapshot = { ticket_id?: string; messages: LabMessage[] };
export type LabNode = { id: string; cutoff_id: string; cutoff_time: string; label: string; ticket: LabSnapshot };
export type LabSample = { ticket_id: string; ticket_number: string; subject: string; group: string; split: string; snapshot: LabSnapshot; nodes: LabNode[] };
export type LabReplyData = { text: string; language: string; rationale: string; missing_information: string[]; source_ids: string[]; message_ids: string[] };
export type LabEvaluation = { verdict: string; score: number; findings: { code: string; severity: string; reason: string; message_ids: string[]; source_ids: string[] }[]; suggestions: string[] };
export type LabFeedback = { id: number; call_id: number; node_id: string; score: number; tags: string[]; note: string; edited_reply: string; created_at: string };
export type LabCall = { id: number; key: string; task: string; node_id: string; variant: string; revision: number; status: string; started_at: string; completed_at?: string; input_hash: string; input: { ticket: LabSnapshot; sources: KnowledgeSource[] }; result?: LabReplyData | LabEvaluation; error?: string; returned_model?: string };
export type LabBatch = { id: number; revision: number; created_at: string; spec: { schema: string; model: string; start_time: string; end_time: string; parent_id?: number; samples: LabSample[]; knowledge: KnowledgeEntry[] }; state: { status: string; phase: string; error?: string; calls: LabCall[]; feedback: LabFeedback[]; drafts: Record<string, number> } };
export type LabCandidate = { active: boolean; key: string; ticket_id: string; entry: KnowledgeEntry; proposal: { title: string; product: string; question: string; answer: string; conditions: string; reason: string; replaces_id: number; message_ids: string[]; source_ids: string[] } };
export type LabSelection = { id: string; ticket_number: string; subject: string; created_time: string; nodes: number; reason?: string };
export const labStatus = (status: string) => ({ draft: "待启动", running: "处理中", paused: "已暂停", completed: "待审核", failed: "失败", success: "完成", interrupted: "已中断" }[status] || status);
export const splitName = (split: string) => ({ learn: "学习", development: "开发", holdout: "留出验证" }[split] || split);
export const phaseName = (phase: string) => ({ learning: "提炼技术知识", development: "模拟与改写", holdout: "固定留出对比", review: "人工审核" }[phase] || phase);
