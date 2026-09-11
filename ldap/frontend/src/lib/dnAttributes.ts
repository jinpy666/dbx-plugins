/**
 * DN 引用属性识别与"被引用"反查过滤器（条目关联视图的通用 DN 引用能力）。
 *
 * LDAP 中大量属性的值是 DN（指向另一个条目）：managedBy、owner、secretary、
 * seeAlso、manager、assistant、altRecipient 等。本模块把 member/memberOf 专属
 * 逻辑泛化为通用 DN 引用的两半：
 * - 正查（deriveDnValuedAttributes）：从 RFC 4512 attributeTypes 原始定义识别
 *   DN 值属性（SYNTAX 为 DN 语法 OID，或 SUP 直接指向 distinguishedName /
 *   nameAndOptionalUID 的单层启发）；schema 不可用时由集成方回退
 *   DN_REFERENCE_CORE 兜底表。
 * - 反查（buildReferencedByFilter）：单次 OR 过滤器搜索哪些条目通过核心引用
 *   属性指向本条目——schema 全量 DN 属性可能数百个，反查固定用内置核心表，
 *   不做全量 OR。
 * 纯函数，无 Vue / DOM 依赖。
 */
import { escapeLdapFilterValue } from "./ldapFilter";

/** RFC 4512 DN 语法 OID 与 nameAndOptionalUID（uniqueMember 用）。 */
export const DN_SYNTAX_OID = "1.3.6.1.4.1.1466.115.121.1.12";
export const NAME_AND_OPTIONAL_UID_SYNTAX_OID = "1.3.6.1.4.1.1466.115.121.1.34";

/** 内置兜底/反查核心表（小写比较；顺序即展示顺序）。 */
export const DN_REFERENCE_CORE = ["managedBy", "owner", "secretary", "assistant", "manager", "seeAlso", "altRecipient"];

// RFC 4512 SYNTAX 子句：`SYNTAX <oid>`，OID 后允许 {len} 长度修饰
// （如 Directory String 的 `.15{32768}`）。捕获组取纯 OID 便于精确比较。
const SYNTAX_RE = /\bSYNTAX\s+(\d+(?:\.\d+)+)(?:\{\d+\})?/iu;
// 单层 SUP 启发：SUP 直接指向两个 DN 上层语法名。`\b` 保证不会误命中
// matching rule 名（如 distinguishedNameMatch）。不做深层继承链解析。
const DN_SUP_RE = /\bSUP\s+\(?\s*(?:distinguishedName|nameAndOptionalUID)\b/iu;
// NAME 子句：`NAME 'x'` 单名或 `NAME ( 'a' 'b' )` 多名，取首名。
const NAME_RE = /\bNAME\s+(?:\(\s*([^)]*)\)|'([^']*)'|([^\s)]+))/iu;

/** 解析 NAME 子句的首名（支持带引号单名与带引号多名两种形态）。 */
function parseFirstName(definition: string): string {
  const match = NAME_RE.exec(definition);
  if (!match) return "";
  if (match[1] != null) {
    // NAME ( 'a' 'b' )：取括号内首个带引号 token；裸 token 序列取第一段兜底。
    const quoted = /'([^']*)'/.exec(match[1]);
    return (quoted ? quoted[1] : (match[1].trim().split(/\s+/)[0] ?? "")).trim();
  }
  return (match[2] ?? match[3] ?? "").trim();
}

/**
 * 从 RFC 4512 attributeTypes 定义推导 DN 值属性名（返回原始大小写、去重）。
 * 命中规则：SYNTAX 为两个 DN 语法 OID 之一；或定义含 "SUP distinguishedName"
 * / "SUP nameAndOptionalUID"（单层启发，不做深层继承链解析）。
 * 解析 NAME 时取首名（支持 NAME 'x' 与 NAME ('a' 'b') 两种形态）；无法解析的行跳过。
 */
export function deriveDnValuedAttributes(attributeTypes: string[]): string[] {
  const names: string[] = [];
  const seen = new Set<string>();
  for (const definition of Array.isArray(attributeTypes) ? attributeTypes : []) {
    const text = String(definition ?? "");
    const syntaxMatch = SYNTAX_RE.exec(text);
    const isDnValued =
      (syntaxMatch !== null && (syntaxMatch[1] === DN_SYNTAX_OID || syntaxMatch[1] === NAME_AND_OPTIONAL_UID_SYNTAX_OID)) ||
      DN_SUP_RE.test(text);
    if (!isDnValued) continue;
    const name = parseFirstName(text);
    const key = name.toLowerCase();
    // 同名重复定义（多 OID 别名行）只保留首次出现的原始大小写。
    if (!key || seen.has(key)) continue;
    seen.add(key);
    names.push(name);
  }
  return names;
}

/**
 * 反查 OR 过滤器：`(|(attr=esc)(attr2=esc2)...)`；attrs 空则返回 ""。单属性时退化为普通 `(attr=esc)`。
 * 值统一做 RFC 4515 转义（调用方无需再转义）；dn 为空同样返回 ""（空 DN 无反查意义）。
 */
export function buildReferencedByFilter(dn: string, attrs: string[]): string {
  const escaped = escapeLdapFilterValue(String(dn ?? "").trim());
  const attributes = (Array.isArray(attrs) ? attrs : [])
    .map((attribute) => String(attribute ?? "").trim())
    .filter(Boolean);
  if (!escaped || attributes.length === 0) return "";
  const clauses = attributes.map((attribute) => `(${attribute}=${escaped})`);
  return clauses.length === 1 ? clauses[0] : `(|${clauses.join("")})`;
}
