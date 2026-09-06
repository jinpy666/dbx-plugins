// newEntryTemplates 纯函数测试（M6 N4）：模板清单/解析、must/may 聚合
// （schema 驱动 + SUP 父类归并 + 内置兜底）、buildDn 转义与 dn.ts 往返对齐。
import { describe, expect, it } from "vitest";
import {
  BUILTIN_TEMPLATES,
  buildDn,
  listTemplates,
  mayAttributesFor,
  mustAttributesFor,
  resolveTemplate,
  type LdapSchema,
} from "./newEntryTemplates";
import { isLikelyRdn, rdnConfirmationToken, splitFirstDnRdn } from "./dn";

// 迷你 schema fixture：objectClassAttributes 走 schemaCache 聚合形状，
// rawObjectClasses 带 SUP 定义以覆盖「schema 驱动父类归并」路径。
const SCHEMA_OBJECT_CLASSES = {
  top: { must: ["objectClass"], may: [] },
  person: { must: ["cn", "sn"], may: ["telephoneNumber", "description"] },
  organizationalPerson: { must: [], may: ["title", "l", "o", "ou", "street"] },
  inetOrgPerson: { must: [], may: ["mail", "uid", "displayName", "telephoneNumber"] },
  groupOfNames: { must: ["cn", "member"], may: ["description", "owner", "seeAlso"] },
  organizationalUnit: { must: ["ou"], may: ["description", "l", "telephoneNumber"] },
  simpleSecurityObject: { must: ["userPassword"], may: [] },
};

const SCHEMA: LdapSchema = {
  objectClassAttributes: SCHEMA_OBJECT_CLASSES,
  rawObjectClasses: [
    "( NAME 'top' STRUCTURAL MUST objectClass )",
    "( NAME 'person' SUP top STRUCTURAL MUST ( cn $ sn ) MAY ( telephoneNumber $ description ) )",
    "( NAME 'organizationalPerson' SUP person STRUCTURAL MAY ( title $ l $ o $ ou $ street ) )",
    "( NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL MAY ( mail $ uid $ displayName $ telephoneNumber ) )",
    "( NAME 'groupOfNames' SUP top STRUCTURAL MUST ( cn $ member ) MAY ( owner $ seeAlso $ description ) )",
    "( NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou MAY ( l $ description $ telephoneNumber ) )",
    "( NAME 'simpleSecurityObject' SUP top AUXILIARY MUST userPassword )",
  ],
};

describe("listTemplates / resolveTemplate", () => {
  it("ships the four built-in templates plus blank, in a stable order", () => {
    expect(listTemplates().map((template) => template.id)).toEqual(["user", "group", "ou", "simpleObject", "blank"]);
    expect(BUILTIN_TEMPLATES).toHaveLength(5);
  });

  it("preseeds the user template with the inetOrgPerson chain and common may set", () => {
    const user = resolveTemplate("user")!;
    expect(user.rdnAttr).toBe("cn");
    expect(user.objectClasses).toEqual(["top", "person", "organizationalPerson", "inetOrgPerson"]);
    expect(user.must).toEqual(["cn", "sn"]);
    expect(user.may).toEqual(["mail", "uid", "telephoneNumber", "displayName"]);
  });

  it("resolves group/ou/simpleObject/blank and returns null for unknown ids", () => {
    expect(resolveTemplate("group")!.must).toContain("member");
    expect(resolveTemplate("ou")!.rdnAttr).toBe("ou");
    expect(resolveTemplate("simpleObject")!.objectClasses).toEqual(["top", "simpleSecurityObject"]);
    expect(resolveTemplate("blank")!.objectClasses).toEqual([]);
    expect(resolveTemplate("nope")).toBeNull();
  });
});

describe("mustAttributesFor", () => {
  it("aggregates must across the schema-derived SUP chain and drops objectClass", () => {
    // inetOrgPerson 链（SUP organizationalPerson → person → top）：自身无 must，
    // 归并 person 的 cn/sn；top 的 objectClass 被剔除（由所选类链自动提供）。
    expect(mustAttributesFor(["inetOrgPerson"], SCHEMA)).toEqual(["cn", "sn"]);
    expect(mustAttributesFor(["groupOfNames"], SCHEMA)).toEqual(["cn", "member"]);
    expect(mustAttributesFor(["organizationalUnit"], SCHEMA)).toEqual(["ou"]);
    expect(mustAttributesFor(["simpleSecurityObject"], SCHEMA)).toEqual(["userPassword"]);
  });

  it("falls back to the known RFC hierarchy when rawObjectClasses are absent", () => {
    const withoutRaw: LdapSchema = { objectClassAttributes: SCHEMA_OBJECT_CLASSES };
    expect(mustAttributesFor(["inetOrgPerson"], withoutRaw)).toEqual(["cn", "sn"]);
    expect(mustAttributesFor(["groupOfnames"], withoutRaw)).toEqual(["cn", "member"]);
  });

  it("falls back to the built-in must table when schema is missing entirely", () => {
    expect(mustAttributesFor(["inetOrgPerson"], null)).toEqual(["cn", "sn"]);
    expect(mustAttributesFor(["groupOfNames"], undefined)).toEqual(["cn", "member"]);
    expect(mustAttributesFor(["organizationalUnit"], {})).toEqual(["ou"]);
    expect(mustAttributesFor(["simpleSecurityObject"], null)).toEqual(["userPassword"]);
  });

  it("mixes schema and built-in fallback per class and tolerates unknown classes", () => {
    // schema 在场但缺 inetOrgPerson 定义：该类走内置兜底，链上 person 仍走 schema。
    const partial: LdapSchema = {
      objectClassAttributes: { person: { must: ["cn", "sn"], may: [] } },
    };
    expect(mustAttributesFor(["inetOrgPerson"], partial)).toEqual(["cn", "sn"]);
    expect(mustAttributesFor(["myCustomClass"], SCHEMA)).toEqual([]);
    expect(mustAttributesFor([], SCHEMA)).toEqual([]);
  });
});

describe("mayAttributesFor", () => {
  it("aggregates may along the chain, dedupes and excludes must attributes", () => {
    const may = mayAttributesFor(["inetOrgPerson"], SCHEMA);
    expect(may).toContain("mail");
    expect(may).toContain("uid");
    expect(may).toContain("displayName");
    expect(may).toContain("telephoneNumber");
    expect(may).not.toContain("cn");
    expect(may).not.toContain("sn");
    expect(may).not.toContain("objectClass");
    // 去重：telephoneNumber 同时出现在 person 与 inetOrgPerson 的 may 里。
    expect(may.filter((name) => name.toLowerCase() === "telephonenumber")).toHaveLength(1);
  });

  it("falls back to the built-in may table when schema is missing", () => {
    const may = mayAttributesFor(["groupOfNames"], null);
    expect(may).toEqual(expect.arrayContaining(["description", "owner", "seeAlso"]));
    expect(may).not.toContain("cn");
    expect(may).not.toContain("member");
    expect(mayAttributesFor(["simpleSecurityObject"], undefined)).toEqual([]);
    const userMay = mayAttributesFor(["inetOrgPerson"], {});
    expect(userMay).toEqual(expect.arrayContaining(["mail", "uid", "displayName"]));
  });
});

describe("buildDn", () => {
  const PARENT = "ou=people,dc=demo,dc=dbx";

  it("joins a plain RDN with the parent DN", () => {
    expect(buildDn("cn", "Alice", PARENT)).toBe(`cn=Alice,${PARENT}`);
    expect(buildDn("cn", "Alice", "")).toBe("cn=Alice");
  });

  it("trims inputs and rejects empty attribute or value", () => {
    expect(buildDn("  cn  ", "  Alice  ", PARENT)).toBe(`cn=Alice,${PARENT}`);
    expect(buildDn("", "Alice", PARENT)).toBe("");
    expect(buildDn("cn", "  ", PARENT)).toBe("");
  });

  it("escapes RFC 4514 specials in the value only", () => {
    expect(buildDn("cn", "Doe, John", PARENT)).toBe(`cn=Doe\\, John,${PARENT}`);
    expect(buildDn("cn", "a+b", PARENT)).toBe(`cn=a\\+b,${PARENT}`);
    expect(buildDn("cn", "#lead", PARENT)).toBe(`cn=\\#lead,${PARENT}`);
    expect(buildDn("cn", "a\\b", PARENT)).toBe(`cn=a\\\\b,${PARENT}`);
    expect(buildDn("cn", '<v>";', PARENT)).toBe(`cn=\\<v\\>\\"\\;,${PARENT}`);
    // 值中段的 # 与空格按 RFC 4514 无需转义。
    expect(buildDn("cn", "E#F G", PARENT)).toBe(`cn=E#F G,${PARENT}`);
  });

  it("produces DNs that round-trip through the dn.ts escape-aware parser", () => {
    const dn = buildDn("cn", 'Doe, "John" + Q <2>;', PARENT);
    const { rdn, parentDn } = splitFirstDnRdn(dn);
    expect(parentDn).toBe(PARENT);
    expect(isLikelyRdn(rdn)).toBe(true);
    expect(rdnConfirmationToken(dn)).toBe('Doe, "John" + Q <2>;');
  });
});
