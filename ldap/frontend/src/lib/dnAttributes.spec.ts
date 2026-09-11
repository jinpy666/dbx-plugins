// lib/dnAttributes 纯函数单测：RFC 4512 attributeTypes 定义 → DN 值属性推导
// （SYNTAX 命中 / SUP 单层启发 / NAME 双形态 / 非 DN 跳过 / 去重），以及
// "被引用"反查 OR 过滤器构建（RFC 4515 转义、单属性退化、空表）。
// 无 Vue / DOM 依赖，node 环境直接跑。
import { describe, expect, it } from "vitest";
import {
  DN_REFERENCE_CORE,
  DN_SYNTAX_OID,
  NAME_AND_OPTIONAL_UID_SYNTAX_OID,
  buildReferencedByFilter,
  deriveDnValuedAttributes,
} from "./dnAttributes";

describe("deriveDnValuedAttributes", () => {
  it("hits DN syntax via SYNTAX clause (with or without {len} decoration)", () => {
    const names = deriveDnValuedAttributes([
      // AD managedBy：SYNTAX DN + SINGLE-VALUE 修饰
      "( 1.2.840.113556.1.4.218 NAME 'managedBy' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 SINGLE-VALUE )",
      // 语法 OID 带 {len} 长度修饰也应命中（此处借用 DN OID 验证解析容错）
      "( 1.1.1.1 NAME 'weirdLen' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12{1024} )",
    ]);
    expect(names).toEqual(["managedBy", "weirdLen"]);
  });

  it("hits nameAndOptionalUID syntax (uniqueMember shape)", () => {
    const names = deriveDnValuedAttributes([
      "( 2.5.4.50 NAME 'uniqueMember' EQUALITY nameAndOptionalUID SYNTAX 1.3.6.1.4.1.1466.115.121.1.34 )",
    ]);
    expect(names).toEqual(["uniqueMember"]);
  });

  it("hits single-level SUP distinguishedName / nameAndOptionalUID", () => {
    const names = deriveDnValuedAttributes([
      "( 2.5.4.31 NAME 'member' SUP distinguishedName )",
      "( 2.5.4.18 NAME 'seeAlso' SUP distinguishedName )",
      "( 1.1.2.1 NAME 'uidRef' SUP nameAndOptionalUID )",
    ]);
    expect(names).toEqual(["member", "seeAlso", "uidRef"]);
  });

  it("does not mistake a matching-rule name for a SUP hit", () => {
    // EQUALITY distinguishedNameMatch 不带 SUP 前缀，不应命中
    const names = deriveDnValuedAttributes([
      "( 2.5.4.49 NAME 'plainDnString' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
    ]);
    expect(names).toEqual([]);
  });

  it("takes the first name for both NAME forms and keeps original casing", () => {
    const names = deriveDnValuedAttributes([
      // 单名形态：NAME 'altRecipient'
      "( 2.16.840.1.113556.1.4.218 NAME 'altRecipient' SUP distinguishedName )",
      // 多名括号形态：NAME ( 'Owner' 'owner' ) —— 取首名并保留原始大小写
      "( 2.5.4.999 NAME ( 'Owner' 'ownerOf' ) SUP distinguishedName )",
    ]);
    expect(names).toEqual(["altRecipient", "Owner"]);
  });

  it("skips non-DN attribute types and unparseable definitions", () => {
    const names = deriveDnValuedAttributes([
      // Directory String 语法 + SUP name —— 非 DN
      "( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )",
      "( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )",
      "( 0.9.2342.19200300.100.1.3 NAME 'mail' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26{256} )",
      // 命中语法但解析不出 NAME → 跳过该行
      "( 1.1.3.1 EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
      // 非 DN 语法却写 SUP distinguishedNameMatch（matching rule 名不触发 SUP 启发）
      "( 1.1.3.2 NAME 'notDn' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
      "",
    ]);
    expect(names).toEqual([]);
  });

  it("dedupes case-insensitively and keeps definition order", () => {
    const names = deriveDnValuedAttributes([
      "( 2.5.4.31 NAME 'member' SUP distinguishedName )",
      "( 2.5.4.18 NAME 'seeAlso' SUP distinguishedName )",
      // 同名（大小写不同）重复定义：只保留首次出现的原始大小写
      "( 1.1.4.1 NAME ( 'MEMBER' ) SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
    ]);
    expect(names).toEqual(["member", "seeAlso"]);
  });

  it("tolerates non-array input and returns an empty list", () => {
    expect(deriveDnValuedAttributes([])).toEqual([]);
    // 集成方可能直接透传 undefined 形态（schema 未加载）
    expect(deriveDnValuedAttributes(undefined as unknown as string[])).toEqual([]);
  });

  it("exposes the built-in core table used for fallback and reverse lookup", () => {
    expect(DN_REFERENCE_CORE).toEqual(["managedBy", "owner", "secretary", "assistant", "manager", "seeAlso", "altRecipient"]);
    expect(DN_SYNTAX_OID).toBe("1.3.6.1.4.1.1466.115.121.1.12");
    expect(NAME_AND_OPTIONAL_UID_SYNTAX_OID).toBe("1.3.6.1.4.1.1466.115.121.1.34");
  });
});

describe("buildReferencedByFilter", () => {
  it("builds a single OR filter over all attributes with the escaped DN", () => {
    const dn = "uid=user0000,ou=people,dc=demo,dc=dbx";
    expect(buildReferencedByFilter(dn, ["managedBy", "owner", "seeAlso"])).toBe(
      `(|(managedBy=${dn})(owner=${dn})(seeAlso=${dn}))`,
    );
  });

  it("degenerates to a plain equality filter for a single attribute", () => {
    expect(buildReferencedByFilter("cn=a,dc=x", ["managedBy"])).toBe("(managedBy=cn=a,dc=x)");
  });

  it("escapes RFC 4515 special characters in the DN value", () => {
    expect(buildReferencedByFilter("cn=a(b)*c\\d,dc=x", ["managedBy", "owner"])).toBe(
      "(|(managedBy=cn=a\\28b\\29\\2ac\\5cd,dc=x)(owner=cn=a\\28b\\29\\2ac\\5cd,dc=x))",
    );
  });

  it("returns an empty string for an empty attribute list", () => {
    expect(buildReferencedByFilter("uid=u,dc=x", [])).toBe("");
  });

  it("returns an empty string for an empty DN", () => {
    expect(buildReferencedByFilter("", ["managedBy", "owner"])).toBe("");
  });
});
