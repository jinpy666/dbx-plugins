/**
 * 新建条目向导的轻模板 + objectClass must/may 聚合（PLA_GAP_ANALYSIS §7 N4）。
 *
 * 模板 = 前端常量（对齐 PLA templates 精简版，不做完整 JSON 模板引擎/自定义
 * 模板目录/uid 自动编号）；must/may 优先从 schemaCache 的 objectClassAttributes
 * 聚合（含父类归并 best-effort：schema rawObjectClasses 的 SUP 优先，缺省用
 * RFC 4519/1274 常识链兜底），schema 不可用时回退模板内置 must/may 表。
 * 零 sidecar 改动；DN 组装复用 lib/dn.ts（joinRdnAndParent），值侧按 RFC 4514
 * 最小转义，输出可通过 dn.ts 的 isLikelyRdn / splitFirstDnRdn 校验。
 */

import { joinRdnAndParent } from "./dn";
import type { ObjectClassAttributes } from "./schemaCache";

export type TemplateId = "user" | "group" | "ou" | "simpleObject" | "blank";

export interface EntryTemplate {
  id: TemplateId;
  /** 建议的 objectClass 链（空白模板为空 = 无建议，由用户自行补加）。 */
  objectClasses: string[];
  /** 建议 RDN 属性（空白模板为空串 = 不建议）。 */
  rdnAttr: string;
  /**
   * 内置 must 兜底表（不含 objectClass——objectClass 由所选类链自动提供），
   * 仅在 schema 不可用/缺该类定义时参与聚合。
   */
  must: string[];
  /** 内置常用 may 建议集，同上兜底语义。 */
  may: string[];
}

/**
 * schemaCache 聚合结果的最小结构切片（SchemaMetadata / useLdapSchemaCache 的
 * objectClassAttributes 即满足；rawObjectClasses 可选，供 SUP 父类归并）。
 */
export interface LdapSchema {
  objectClassAttributes?: Record<string, ObjectClassAttributes>;
  rawObjectClasses?: string[];
}

export interface NewEntryPayload {
  dn: string;
  attributes: Record<string, string[]>;
}

// -- 内置轻模板 ---------------------------------------------------------------

export const BUILTIN_TEMPLATES: readonly EntryTemplate[] = [
  {
    id: "user",
    objectClasses: ["top", "person", "organizationalPerson", "inetOrgPerson"],
    rdnAttr: "cn",
    must: ["cn", "sn"],
    may: ["mail", "uid", "telephoneNumber", "displayName"],
  },
  {
    id: "group",
    objectClasses: ["top", "groupOfNames"],
    rdnAttr: "cn",
    must: ["cn", "member"],
    may: ["description", "owner", "seeAlso"],
  },
  {
    id: "ou",
    objectClasses: ["top", "organizationalUnit"],
    rdnAttr: "ou",
    must: ["ou"],
    may: ["description", "l", "telephoneNumber"],
  },
  {
    // RFC 4519 无 simpleObject 类：取 simpleSecurityObject（MUST userPassword）
    // 组成最小可用对象（账号 + 密码），RDN 仍用 cn。
    id: "simpleObject",
    objectClasses: ["top", "simpleSecurityObject"],
    rdnAttr: "cn",
    must: ["userPassword"],
    may: [],
  },
  {
    // 「空白」：不预置 objectClass 建议，步骤 ② 自行补加（补加引导入口）。
    id: "blank",
    objectClasses: [],
    rdnAttr: "",
    must: [],
    may: [],
  },
];

const templatesById = new Map<string, EntryTemplate>(BUILTIN_TEMPLATES.map((template) => [template.id, template]));

export function listTemplates(): EntryTemplate[] {
  return [...BUILTIN_TEMPLATES];
}

export function resolveTemplate(id: string): EntryTemplate | null {
  return templatesById.get(id) ?? null;
}

// -- 内置 per-class 兜底表（schema 不可用/缺定义时的 best-effort） -------------

const FALLBACK_MUST: Record<string, string[]> = {
  // top 的 MUST objectClass 在聚合结果里统一剔除（由所选类链自动生成）。
  top: ["objectClass"],
  person: ["cn", "sn"],
  organizationalperson: [],
  inetorgperson: [],
  groupofnames: ["cn", "member"],
  organizationalunit: ["ou"],
  simplesecurityobject: ["userPassword"],
};

const FALLBACK_MAY: Record<string, string[]> = {
  person: ["telephoneNumber", "description"],
  organizationalperson: ["title", "l", "o", "ou", "street"],
  inetorgperson: ["mail", "uid", "displayName", "mobile", "departmentNumber"],
  groupofnames: ["description", "owner", "seeAlso", "businessCategory", "o", "ou"],
  organizationalunit: ["description", "l", "o", "telephoneNumber", "street", "postalCode"],
  simplesecurityobject: [],
};

/** schema 缺定义时的常识父类链（RFC 4519/1274/2798）。 */
const KNOWN_SUPERS: Record<string, string[]> = {
  person: ["top"],
  organizationalperson: ["person"],
  inetorgperson: ["organizationalperson"],
  groupofnames: ["top"],
  organizationalunit: ["top"],
  simplesecurityobject: ["top"],
};

const OBJECT_CLASS_KEY = "objectclass";

// -- schema 查找 / 父类归并（best-effort，全部容错） ---------------------------

const lower = (value: string): string => String(value ?? "").trim().toLowerCase();

function lookupClassAttributes(schema: LdapSchema | null | undefined, className: string): ObjectClassAttributes | null {
  const map = schema?.objectClassAttributes;
  if (!map) return null;
  const direct = map[className];
  if (direct) return direct;
  const key = lower(className);
  for (const name of Object.keys(map)) {
    if (lower(name) === key) return map[name];
  }
  return null;
}

/** 从 rawObjectClasses 里按 NAME 找到定义并抽出 SUP 值（无则空数组）。 */
function schemaSupers(schema: LdapSchema | null | undefined, className: string): string[] {
  const raw = schema?.rawObjectClasses;
  if (!Array.isArray(raw) || raw.length === 0) return [];
  const key = lower(className);
  for (const definition of raw) {
    const text = String(definition ?? "");
    const nameMatch = /\bNAME\s+(?:\(\s*([^)]*)\s*\)|'([^']*)'|([^\s)]+))/iu.exec(text);
    if (!nameMatch) continue;
    const names = (nameMatch[1] ?? `${nameMatch[2] ?? ""} ${nameMatch[3] ?? ""}`)
      .match(/'([^']*)'|([^\s$()]+)/gu)
      ?.map((token) => token.replace(/^'|'$/gu, "").trim())
      .filter(Boolean) ?? [];
    if (!names.some((name) => lower(name) === key)) continue;
    const supMatch = /\bSUP\s+(?:\(\s*([^)]*)\s*\)|'([^']*)'|([^\s)]+))/iu.exec(text);
    if (!supMatch) return [];
    return (supMatch[1] ?? `${supMatch[2] ?? ""} ${supMatch[3] ?? ""}`)
      .split(/[\s$]+/u)
      .map((token) => token.trim())
      .filter(Boolean);
  }
  return [];
}

/** 父类闭包（自身在前，去环、限深）：schema SUP 优先，缺省用常识链。 */
function superChain(schema: LdapSchema | null | undefined, className: string): string[] {
  const chain: string[] = [];
  const seen = new Set<string>();
  const queue = [className];
  let depth = 0;
  while (queue.length > 0 && depth < 16) {
    const current = queue.shift()!;
    const key = lower(current);
    if (!key || seen.has(key)) continue;
    seen.add(key);
    chain.push(current);
    const supers = schemaSupers(schema, current);
    queue.push(...(supers.length > 0 ? supers : KNOWN_SUPERS[key] ?? []));
    depth += 1;
  }
  return chain;
}

/** schema 是否有任何可用数据（决定是否整体走内置兜底）。 */
function hasSchemaData(schema: LdapSchema | null | undefined): boolean {
  return Boolean(schema?.objectClassAttributes && Object.keys(schema.objectClassAttributes).length > 0);
}

function classMust(schema: LdapSchema | null | undefined, schemaLive: boolean, className: string): string[] {
  const fromSchema = lookupClassAttributes(schema, className)?.must ?? [];
  if (fromSchema.length > 0 || (schemaLive && lookupClassAttributes(schema, className))) return fromSchema;
  return FALLBACK_MUST[lower(className)] ?? [];
}

function classMay(schema: LdapSchema | null | undefined, schemaLive: boolean, className: string): string[] {
  const fromSchema = lookupClassAttributes(schema, className)?.may ?? [];
  if (fromSchema.length > 0 || (schemaLive && lookupClassAttributes(schema, className))) return fromSchema;
  return FALLBACK_MAY[lower(className)] ?? [];
}

function mergeNames(values: string[]): string[] {
  const seen = new Set<string>();
  const merged: string[] = [];
  for (const value of values) {
    const name = String(value ?? "").trim();
    if (!name) continue;
    const key = lower(name);
    if (key === OBJECT_CLASS_KEY || seen.has(key)) continue;
    seen.add(key);
    merged.push(name);
  }
  return merged;
}

// -- 聚合入口 -----------------------------------------------------------------

/**
 * 聚合 objectClass 链的 MUST 属性：逐类做父类闭包后归并（schema 驱动，缺定义
 * 的类回退内置表）；剔除 objectClass 自身并大小写不敏感去重。
 */
export function mustAttributesFor(objectClasses: string[], schema: LdapSchema | null | undefined): string[] {
  const schemaLive = hasSchemaData(schema);
  const raw: string[] = [];
  for (const className of Array.isArray(objectClasses) ? objectClasses : []) {
    for (const link of superChain(schema, className)) {
      raw.push(...classMust(schema, schemaLive, link));
    }
  }
  return mergeNames(raw);
}

/** 聚合 MAY 属性：同 must 语义，另排除 MUST 集合（去重、剔除 objectClass）。 */
export function mayAttributesFor(objectClasses: string[], schema: LdapSchema | null | undefined): string[] {
  const schemaLive = hasSchemaData(schema);
  const must = new Set(mustAttributesFor(objectClasses, schema).map(lower));
  const raw: string[] = [];
  for (const className of Array.isArray(objectClasses) ? objectClasses : []) {
    for (const link of superChain(schema, className)) {
      raw.push(...classMay(schema, schemaLive, link));
    }
  }
  return mergeNames(raw).filter((name) => !must.has(lower(name)));
}

// -- DN 组装 ------------------------------------------------------------------

/** RFC 4514 最小值转义：反斜杠、`,+"<>;`、前导 `#`、首尾空格。 */
function escapeDnValue(value: string): string {
  const text = String(value ?? "");
  let escaped = "";
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === "\\" || char === "," || char === "+" || char === '"' || char === "<" || char === ">" || char === ";") {
      escaped += `\\${char}`;
      continue;
    }
    if ((index === 0 || index === text.length - 1) && char === " ") {
      escaped += `\\${char}`;
      continue;
    }
    if (index === 0 && char === "#") {
      escaped += `\\${char}`;
      continue;
    }
    escaped += char;
  }
  return escaped;
}

/**
 * 组装新条目 DN：`attr=escaped(value)` 与父 DN 用 lib/dn.ts 的 joinRdnAndParent
 * 拼接（转义语义与 dn.ts 的 escape-aware 解析对齐，输出可通过 isLikelyRdn）。
 * 属性名或值为空时返回空串（由调用方拦截）。
 */
export function buildDn(rdnAttr: string, rdnValue: string, parentDn: string): string {
  const attr = String(rdnAttr ?? "").trim();
  const value = String(rdnValue ?? "").trim();
  if (!attr || !value) return "";
  return joinRdnAndParent(`${attr}=${escapeDnValue(value)}`, String(parentDn ?? "").trim());
}
