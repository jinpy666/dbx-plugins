// 审计事件流数据面：工作台对 sidecar `kafka/audit` 事件（ok/denied/error，
// 契约同 ldap/audit 形状）的最近操作记录。纯函数实现便于单测，
// 组件（AuditFeedPanel.vue）只持有列表并调用 push。

export type AuditResult = "ok" | "denied" | "error";

export interface AuditFeedItem {
  id: number;
  action: string;
  target: string;
  result: AuditResult;
  at: number; // unix ms（本地事件到达时间，非服务器时间）
  detail?: string;
}

export const AUDIT_FEED_MAX = 100;

export function normalizeAuditResult(value: unknown): AuditResult {
  return value === "ok" || value === "denied" ? value : "error";
}

// 事件 params（与后端 AuditRecord JSON 同面）→ 展示条目；缺省字段兜底，
// 未知 result 折算为 error（保守告警，不静默吞掉未知取值）。
export function parseAuditEvent(params: unknown, id: number, nowMs: number): AuditFeedItem {
  const source = params && typeof params === "object" ? (params as Record<string, unknown>) : {};
  const detail = typeof source.detail === "string" && source.detail ? source.detail : undefined;
  return {
    id,
    action: typeof source.action === "string" && source.action ? source.action : "kafka",
    target: typeof source.target === "string" ? source.target : "",
    result: normalizeAuditResult(source.result),
    at: Number.isFinite(nowMs) ? nowMs : Date.now(),
    ...(detail ? { detail } : {}),
  };
}

// 最新事件插到头部；超出上限从尾部裁剪（最近操作语义，旧事件丢弃）。
export function pushAuditItem(items: AuditFeedItem[], item: AuditFeedItem, max = AUDIT_FEED_MAX): AuditFeedItem[] {
  const next = [item, ...items];
  return next.length > max ? next.slice(0, max) : next;
}

// 本地时间 HH:MM:SS；跨天事件加 MM-DD 前缀，避免陈旧条目误导。
export function formatAuditTime(at: number, nowMs = Date.now()): string {
  const date = new Date(at);
  const pad = (value: number) => String(value).padStart(2, "0");
  const clock = `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
  const now = new Date(Number.isFinite(nowMs) ? nowMs : Date.now());
  return date.toDateString() === now.toDateString() ? clock : `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${clock}`;
}
