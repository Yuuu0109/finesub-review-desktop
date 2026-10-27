import { request } from "./transport";
export type User = {
  id: string;
  username: string;
  name: string;
  role: "admin" | "member";
  active: boolean;
  passcode_member?: boolean;
};
export type Entry = {
  id: string;
  entry_id: string;
  title: string;
  category: string;
  kind: string;
  aliases: string;
  summary: string;
  source: string;
  number: number;
  status: string;
  created_at: string;
  path: string;
  operation: string;
  review_count: number;
  my_review?: boolean;
  assignee_id: string | null;
  published: boolean;
  content?: string;
  current_revision?: string;
  batch_id?: string;
  group_id?: string;
};
export type RecordItem = {
  id: string;
  revision_id: string;
  author: string;
  author_id: string;
  parent_id: string | null;
  verdict: string;
  body: string;
  quote: string;
  suggestion: string;
  evidence: string;
  resolution: string;
  resolution_note: string;
  created_at: string;
  stale: boolean;
};
export type Detail = {
  entry: Entry & { content: string; current_revision: string };
  versions: Entry[];
  diff: string[];
  warnings: string[];
  errors: string[];
  reviews: RecordItem[];
  decisions: {
    revision_id: string;
    status: string;
    reason: string;
    author: string;
    created_at: string;
  }[];
  comparison: { kind: "previous" | "published"; number: number | null; id: string | null };
  related: Entry[];
};
export type ReleaseCandidate = Entry & { blockers: string[]; related: Entry[] };
export type Batch = {
  id: string;
  name: string;
  status: string;
  source: string;
  created_at: string;
  warnings?: string[];
  items?: ImportItem[];
  job?: { id: string; status: string; error: string };
  assets?: { id: string; filename: string; digest: string }[];
};
export type ImportItem = {
  id: string;
  path: string;
  classification: string;
  warnings: string[];
  error: string;
  grouped: boolean;
  operation: string;
};
export type Release = {
  id: string;
  name: string;
  notes: string;
  status: string;
  created_at: string;
  count: number;
  report: { warnings?: string[]; errors?: string[]; files?: number; native_import_requires_compat_tool?: boolean };
  rollback_of: string | null;
  current: boolean;
  job_id: string | null;
};
export type Notice = {
  id: string;
  entry_id: string | null;
  text: string;
  read: boolean;
  created_at: string;
};
export let csrf = "";
export const setCsrf = (token: string) => {
  csrf = token;
};
export async function apiResponse(
  path: string,
  options: RequestInit = {},
): Promise<Response> {
  const headers: Record<string, string> = {
    ...((options.headers as Record<string, string>) ?? {}),
  };
  if (options.body && !(options.body instanceof FormData))
    headers["Content-Type"] = "application/json";
  if (csrf) headers["X-CSRF-Token"] = csrf;
  const result = await request(path, {
    ...options,
    headers,
  });
  if (!result.ok) {
    const body = await result.json().catch(() => ({}));
    const detail =
      typeof body.detail === "string"
        ? body.detail
        : Array.isArray(body.detail)
          ? body.detail
              .map((x: any) => `${x.loc?.slice(1).join(".")}: ${x.msg}`)
              .join("；")
          : "请求失败，请稍后重试";
    if (
      (result.status === 401 &&
        !["/auth/login", "/auth/enter"].includes(path)) ||
      (result.status === 403 &&
        typeof detail === "string" &&
        detail.startsWith("会话校验失败"))
    )
      window.dispatchEvent(new Event("session-expired"));
    const error = new Error(detail) as Error & { status?: number };
    error.status = result.status;
    throw error;
  }
  return result;
}
export async function api<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  return (await apiResponse(path, options)).json();
}
export const post = <T = any>(path: string, data: unknown = {}) =>
  api<T>(path, { method: "POST", body: JSON.stringify(data) });
export const labels: Record<string, string> = {
  pending: "待裁决",
  approved: "已通过",
  rejected: "已驳回",
  needs_changes: "待补充",
  queued: "排队中",
  processing: "处理中",
  preview: "待确认",
  confirmed: "已导入",
  failed: "失败",
  ready: "已发布",
  approve: "建议通过",
  changes: "建议修改",
  reject: "建议驳回",
  evidence: "证据不足",
  reply: "回复",
  open: "未处理",
  accepted: "已采纳",
  partial: "部分采纳",
  declined: "未采纳",
  more_evidence: "待补证",
  new: "新增",
  changed: "修改",
  duplicate: "重复",
  invalid: "无法导入",
  renamed: "重命名",
  deleted: "删除",
  common: "通用知识",
  streamer: "主播档案",
  style: "翻译风格",
  translation: "翻译纠错",
};
export function date(value?: string) {
  return value
    ? new Date(value).toLocaleString("zh-CN", {
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
    : "—";
}
