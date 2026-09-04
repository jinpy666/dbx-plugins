// Ported from tiny-rdm ldapFilter.test.js (core cases) + new
// validateLDAPFilter / buildTreeKeywordFilter coverage.
import { describe, expect, it } from "vitest";
import {
  buildBuilderClauseFilter,
  buildClauseFilter,
  buildEqualityFilter,
  buildGroupFilter,
  buildNegatedFilter,
  buildNodeFilter,
  buildPresenceFilter,
  buildQueryBuilderFilter,
  buildSubstringFilter,
  buildTreeKeywordFilter,
  collectBuilderErrors,
  combineFilters,
  createBuilderClause,
  createBuilderGroup,
  escapeLdapFilterValue,
  isValidLDAPAttributeDescription,
  parseClauseItem,
  parseFilterStructure,
  reviveBuilderNode,
  toBuilderRoot,
  unescapeLdapFilterValue,
  validateQueryBuilder,
  validateQueryClause,
  validateLDAPFilter,
  type BuilderNode,
} from "./ldapFilter";

describe("escapeLdapFilterValue", () => {
  it("escapes RFC 4515 special characters", () => {
    expect(escapeLdapFilterValue("abc")).toBe("abc");
    expect(escapeLdapFilterValue("a*b")).toBe("a\\2ab");
    expect(escapeLdapFilterValue("a(b")).toBe("a\\28b");
    expect(escapeLdapFilterValue("a)b")).toBe("a\\29b");
    expect(escapeLdapFilterValue("a\\b")).toBe("a\\5cb");
    expect(escapeLdapFilterValue("a\u0000b")).toBe("a\\00b");
  });

  it("leaves benign characters (including spaces) untouched", () => {
    expect(escapeLdapFilterValue("hello world")).toBe("hello world");
    expect(escapeLdapFilterValue("Jürgen-中文")).toBe("Jürgen-中文");
  });

  it("escapes backslash first to avoid double-escaping", () => {
    expect(escapeLdapFilterValue("\\*")).toBe("\\5c\\2a");
  });

  it("handles null/undefined", () => {
    expect(escapeLdapFilterValue(null)).toBe("");
    expect(escapeLdapFilterValue(undefined)).toBe("");
  });
});

describe("filter builders", () => {
  it("presence", () => {
    expect(buildPresenceFilter("objectClass")).toBe("(objectClass=*)");
    expect(buildPresenceFilter("  ")).toBe("");
  });

  it("equality escapes the value", () => {
    expect(buildEqualityFilter("cn", "John Doe")).toBe("(cn=John Doe)");
    expect(buildEqualityFilter("cn", "Pic*ard")).toBe("(cn=Pic\\2aard)");
    expect(buildEqualityFilter("cn", " ")).toBe("");
  });

  it("substring modes", () => {
    expect(buildSubstringFilter("cn", "jo", "contains")).toBe("(cn=*jo*)");
    expect(buildSubstringFilter("cn", "jo", "startsWith")).toBe("(cn=jo*)");
    expect(buildSubstringFilter("cn", "jo", "endsWith")).toBe("(cn=*jo)");
  });

  it("negation passes already-negated filters through unchanged", () => {
    expect(buildNegatedFilter("(cn=a)")).toBe("(!(cn=a))");
    expect(buildNegatedFilter("(!(cn=a))")).toBe("(!(cn=a))");
    expect(buildNegatedFilter("")).toBe("");
  });

  it("combine and/or", () => {
    expect(combineFilters(["(a=1)", "(b=2)"], "and")).toBe("(&(a=1)(b=2))");
    expect(combineFilters(["(a=1)", "(b=2)"], "or")).toBe("(|(a=1)(b=2))");
    expect(combineFilters(["(a=1)"], "and")).toBe("(a=1)");
    expect(combineFilters([], "or")).toBe("");
  });

  it("clause with notEq becomes negated equality", () => {
    expect(buildClauseFilter({ field: "cn", op: "notEq", value: "x" })).toBe("(!(cn=x))");
    expect(buildClauseFilter({ field: "cn", op: "eq", value: "x", negate: true })).toBe("(!(cn=x))");
    expect(buildClauseFilter({ field: "objectClass", op: "present" })).toBe("(objectClass=*)");
  });

  it("custom field resolution", () => {
    expect(buildClauseFilter({ field: "__custom__", customField: "uid", op: "eq", value: "j" })).toBe("(uid=j)");
    expect(buildClauseFilter({ useCustomField: true, customField: "uid", op: "eq", value: "j" })).toBe("(uid=j)");
  });

  it("group and builder composition", () => {
    expect(
      buildGroupFilter({ join: "or", clauses: [{ field: "cn", op: "eq", value: "a" }, { field: "uid", op: "eq", value: "b" }] }),
    ).toBe("(|(cn=a)(uid=b))");
    expect(
      buildQueryBuilderFilter({ join: "and", groups: [{ clauses: [{ field: "cn", op: "eq", value: "a" }] }, { clauses: [{ field: "uid", op: "eq", value: "b" }] }] }),
    ).toBe("(&(cn=a)(uid=b))");
  });
});

describe("attribute description validation", () => {
  it("accepts descriptors, OIDs and options", () => {
    expect(isValidLDAPAttributeDescription("cn")).toBe(true);
    expect(isValidLDAPAttributeDescription("sAMAccountName")).toBe(true);
    expect(isValidLDAPAttributeDescription("1.2.840.113556.1.4.221")).toBe(true);
    expect(isValidLDAPAttributeDescription("cn;lang-en")).toBe(true);
    expect(isValidLDAPAttributeDescription("1.2.3;binary")).toBe(true);
  });

  it("rejects invalid descriptions", () => {
    expect(isValidLDAPAttributeDescription("cn x")).toBe(false);
    expect(isValidLDAPAttributeDescription("")).toBe(false);
    expect(isValidLDAPAttributeDescription("-bad")).toBe(false);
  });
});

describe("validateQueryClause / validateQueryBuilder", () => {
  it("reports missing attribute", () => {
    expect(validateQueryClause({ op: "eq", value: "x" }, 1, 2)).toMatchObject({ field: "field", code: "attribute_required", groupIndex: 1, clauseIndex: 2 });
  });

  it("reports invalid attribute", () => {
    expect(validateQueryClause({ field: "bad attr", op: "eq", value: "x" })).toMatchObject({ code: "attribute_invalid" });
  });

  it("reports missing value for non-present ops", () => {
    expect(validateQueryClause({ field: "cn", op: "eq", value: "" })).toMatchObject({ field: "value", code: "value_required" });
    expect(validateQueryClause({ field: "cn", op: "present" })).toBeNull();
  });

  it("collects errors across groups", () => {
    const errors = validateQueryBuilder({ groups: [{ clauses: [{ op: "eq", value: "x" }] }, { clauses: [{ field: "cn", op: "eq", value: "" }] }] });
    expect(errors).toHaveLength(2);
  });
});

describe("validateLDAPFilter", () => {
  it("accepts valid RFC 4515 filters", () => {
    expect(validateLDAPFilter("(objectClass=*)")).toBe(true);
    expect(validateLDAPFilter("(cn=John Doe)")).toBe(true);
    expect(validateLDAPFilter("(|(cn=a)(uid=b))")).toBe(true);
    expect(validateLDAPFilter("(&(objectClass=person)(!(cn=disabled)))")).toBe(true);
    expect(validateLDAPFilter("(cn>=a)")).toBe(true);
    expect(validateLDAPFilter("(cn~=a)")).toBe(true);
    expect(validateLDAPFilter("(cn:dn:=a)")).toBe(true);
  });

  it("rejects invalid filters", () => {
    expect(validateLDAPFilter("")).toBe(false);
    expect(validateLDAPFilter("(cn=a")).toBe(false);
    expect(validateLDAPFilter("cn=a)")).toBe(false);
    expect(validateLDAPFilter("(&)")).toBe(false);
    expect(validateLDAPFilter("(!)")).toBe(false);
    expect(validateLDAPFilter("objectClass=*")).toBe(false);
    expect(validateLDAPFilter("(()")).toBe(false);
    expect(validateLDAPFilter("(cn=a)(uid=b)")).toBe(false);
  });
});

describe("buildTreeKeywordFilter", () => {
  it("ORs substring matches over naming attributes", () => {
    const filter = buildTreeKeywordFilter("jdoe");
    expect(filter.startsWith("(|")).toBe(true);
    expect(filter).toContain("(cn=*jdoe*)");
    expect(filter).toContain("(uid=*jdoe*)");
    expect(filter).toContain("(sAMAccountName=*jdoe*)");
  });

  it("escapes the keyword", () => {
    expect(buildTreeKeywordFilter("a*b")).toContain("(cn=*a\\2ab*)");
  });

  it("falls back to presence when empty", () => {
    expect(buildTreeKeywordFilter("")).toBe("(objectClass=*)");
  });
});

// == A-LDAP: builder tree + source parsing ===================================

describe("buildNodeFilter per operator (table-driven)", () => {
  const specialValues = [
    ["plain", "plain"],
    ["a*b", "a\\2ab"],
    ["a(b", "a\\28b"],
    ["a)b", "a\\29b"],
    ["a\\b", "a\\5cb"],
    ["a\u0000b", "a\\00b"],
    ["Jürgen 中文", "Jürgen 中文"],
  ] as const;

  it.each([
    ["equals", (attr: string, value: string) => `(${attr}=${escapeLdapFilterValue(value)})`],
    ["contains", (attr: string, value: string) => `(${attr}=*${escapeLdapFilterValue(value)}*)`],
    ["startsWith", (attr: string, value: string) => `(${attr}=${escapeLdapFilterValue(value)}*)`],
    ["endsWith", (attr: string, value: string) => `(${attr}=*${escapeLdapFilterValue(value)})`],
    ["gte", (attr: string, value: string) => `(${attr}>=${escapeLdapFilterValue(value)})`],
    ["lte", (attr: string, value: string) => `(${attr}<=${escapeLdapFilterValue(value)})`],
    ["approx", (attr: string, value: string) => `(${attr}~=${escapeLdapFilterValue(value)})`],
  ])("operator %s escapes every special value", (op, render) => {
    for (const [raw, escaped] of specialValues) {
      expect(buildNodeFilter(createBuilderClause({ attribute: "cn", op: op as never, value: raw }))).toBe(render("cn", raw));
      expect(escapeLdapFilterValue(raw)).toBe(escaped);
    }
  });

  it("present ignores the value and renders attr=*", () => {
    expect(buildNodeFilter(createBuilderClause({ attribute: "objectClass", op: "present", value: "" }))).toBe("(objectClass=*)");
  });

  it("drops clauses without attribute or (for non-present ops) value", () => {
    expect(buildNodeFilter(createBuilderClause({ op: "equals", value: "x" }))).toBe("");
    expect(buildNodeFilter(createBuilderClause({ attribute: "cn", op: "equals", value: " " }))).toBe("");
    expect(buildNodeFilter(createBuilderClause({ attribute: "cn", op: "equals", value: "x" }))).toBe("(cn=x)");
  });

  it("negates clauses and avoids double negation", () => {
    expect(buildNodeFilter({ ...createBuilderClause({ attribute: "cn", op: "equals", value: "x" }), negate: true })).toBe("(!(cn=x))");
  });

  it("legacy clause builders stay aligned", () => {
    expect(buildBuilderClauseFilter(createBuilderClause({ attribute: "mail", op: "endsWith", value: "@x" }))).toBe("(mail=*@x)");
    expect(buildPresenceFilter("cn")).toBe("(cn=*)");
    expect(buildEqualityFilter("cn", "a")).toBe("(cn=a)");
    expect(buildSubstringFilter("cn", "a", "contains")).toBe("(cn=*a*)");
  });

});

describe("nested groups", () => {
  it("wraps child groups with AND/OR", () => {
    const tree = createBuilderGroup({
      join: "and",
      children: [
        createBuilderClause({ attribute: "objectClass", op: "equals", value: "person" }),
        createBuilderGroup({
          join: "or",
          children: [
            createBuilderClause({ attribute: "cn", op: "contains", value: "jo" }),
            createBuilderClause({ attribute: "uid", op: "startsWith", value: "jd" }),
          ],
        }),
      ],
    });
    expect(buildNodeFilter(tree)).toBe("(&(objectClass=person)(|(cn=*jo*)(uid=jd*)))");
  });

  it("drops empty groups but keeps siblings", () => {
    const tree = createBuilderGroup({
      join: "and",
      children: [
        createBuilderGroup({ join: "or", children: [] }),
        createBuilderClause({ attribute: "cn", op: "equals", value: "a" }),
      ],
    });
    expect(buildNodeFilter(tree)).toBe("(cn=a)");
  });

  it("negates whole groups once", () => {
    const tree = createBuilderGroup({
      join: "or",
      negate: true,
      children: [
        createBuilderClause({ attribute: "cn", op: "equals", value: "a" }),
        createBuilderClause({ attribute: "uid", op: "equals", value: "b" }),
      ],
    });
    expect(buildNodeFilter(tree)).toBe("(!(|(cn=a)(uid=b)))");
  });

  it("collects per-clause validation errors with node ids", () => {
    const clause = createBuilderClause({ attribute: "cn", op: "equals", value: "" });
    const tree = createBuilderGroup({ children: [createBuilderClause({ op: "equals", value: "x" }), clause] });
    expect(collectBuilderErrors(tree)).toEqual([
      { id: tree.children[0].id, code: "attribute_required" },
      { id: clause.id, code: "value_required" },
    ]);
  });

  it("treats fully empty clauses as match-all (no error), half-filled ones still error", () => {
    // 空条件 = 匹配全部（v0.1.22）：属性与值皆空不算错。
    const tree = createBuilderGroup({ children: [createBuilderClause()] });
    expect(collectBuilderErrors(tree)).toEqual([]);
    expect(buildNodeFilter(tree)).toBe("");
    // 半填（有属性无值）仍需报错。
    const half = createBuilderGroup({ children: [createBuilderClause({ attribute: "cn", op: "equals" })] });
    expect(collectBuilderErrors(half)).toEqual([{ id: half.children[0].id, code: "value_required" }]);
  });
});

describe("parseFilterStructure (table-driven)", () => {
  const stripIds = (node: BuilderNode | null): unknown => {
    if (!node) return null;
    const { id: _id, ...rest } = node as unknown as Record<string, unknown> & { id: string };
    if (rest.kind === "group") rest.children = (rest.children as BuilderNode[]).map((child) => stripIds(child));
    return rest;
  };

  it.each([
    ["(cn=x)", { kind: "clause", attribute: "cn", op: "equals", value: "x" }],
    ["(cn=*)", { kind: "clause", attribute: "cn", op: "present", value: "" }],
    ["(cn=*jo*)", { kind: "clause", attribute: "cn", op: "contains", value: "jo" }],
    ["(cn=jo*)", { kind: "clause", attribute: "cn", op: "startsWith", value: "jo" }],
    ["(cn=*jo)", { kind: "clause", attribute: "cn", op: "endsWith", value: "jo" }],
    ["(cn>=a)", { kind: "clause", attribute: "cn", op: "gte", value: "a" }],
    ["(cn<=a)", { kind: "clause", attribute: "cn", op: "lte", value: "a" }],
    ["(cn~=a)", { kind: "clause", attribute: "cn", op: "approx", value: "a" }],
    ["(cn=Pic\\2aard)", { kind: "clause", attribute: "cn", op: "equals", value: "Pic*ard" }],
    ["(cn=Pic\\5card)", { kind: "clause", attribute: "cn", op: "equals", value: "Pic\\ard" }],
    ["(cn=\\28x\\29)", { kind: "clause", attribute: "cn", op: "equals", value: "(x)" }],
    ["(!(cn=x))", { kind: "clause", attribute: "cn", op: "notEquals", value: "x" }],
    [
      "(&(objectClass=person)(|(cn=*jo*)(!(uid=jd*))))",
      {
        kind: "group",
        join: "and",
        children: [
          { kind: "clause", attribute: "objectClass", op: "equals", value: "person" },
          {
            kind: "group",
            join: "or",
            children: [
              { kind: "clause", attribute: "cn", op: "contains", value: "jo" },
              { kind: "clause", attribute: "uid", op: "startsWith", value: "jd", negate: true },
            ],
          },
        ],
      },
    ],
  ])("parses %s", (input, expected) => {
    expect(stripIds(parseFilterStructure(input))).toEqual(expected);
  });

  it("rejects shapes the builder cannot represent (stay in source mode)", () => {
    for (const invalid of [
      "",
      "cn=a",
      "(cn=a",
      "cn=a)",
      "(&)",
      "(!)",
      "(()",
      "(cn=a)(uid=b)",
      "(cn=a*b)", // multi-star substring pattern
      "(cn=a**b)", // empty middle segment
      "(cn:dn:=x)", // extensible match
      "(cn=)", // empty value
      "(cn>=)", // empty comparison value
      "(bad attr=x)", // invalid attribute description
      "(a=b)c", // trailing content
    ]) {
      expect(parseFilterStructure(invalid), `input ${JSON.stringify(invalid)}`).toBeNull();
    }
  });

  it("keeps trailing wildcard segments only when exactly one core segment exists", () => {
    expect(parseClauseItem("cn=*a*b*")).toBeNull();
    expect(parseClauseItem("cn=*a*")).not.toBeNull();
  });
});

describe("source-mode roundtrip (build → parse → build)", () => {
  const trees: BuilderNode[] = [
    createBuilderClause({ attribute: "cn", op: "equals", value: "John Doe" }),
    createBuilderClause({ attribute: "mail", op: "contains", value: "a*b" }),
    createBuilderClause({ attribute: "cn", op: "present" }),
    { ...createBuilderClause({ attribute: "uid", op: "startsWith", value: "jd" }), negate: true },
    createBuilderClause({ attribute: "createTimestamp", op: "gte", value: "20260101000000Z" }),
    createBuilderGroup({
      join: "and",
      children: [
        createBuilderClause({ attribute: "objectClass", op: "equals", value: "person" }),
        createBuilderGroup({
          join: "or",
          children: [
            createBuilderClause({ attribute: "cn", op: "contains", value: "jo" }),
            { ...createBuilderClause({ attribute: "uid", op: "endsWith", value: "oe" }), negate: true },
            createBuilderClause({ attribute: "mail", op: "approx", value: "a(b)" }),
          ],
        }),
        { ...createBuilderGroup({ join: "and", children: [createBuilderClause({ attribute: "sn", op: "equals", value: "Doe" })] }), negate: true },
      ],
    }),
  ];

  it.each(trees.map((tree, index) => [index, tree] as const))("tree #%i roundtrips stably", (_index, tree) => {
    const once = buildNodeFilter(tree);
    const parsed = parseFilterStructure(once);
    expect(parsed, `reparse of ${once}`).not.toBeNull();
    expect(buildNodeFilter(parsed)).toBe(once);
    // idempotent: parsing an already-generated string is stable
    const reparsed = parseFilterStructure(buildNodeFilter(parsed));
    expect(buildNodeFilter(reparsed)).toBe(once);
  });

  it("toBuilderRoot wraps a bare clause", () => {
    const root = toBuilderRoot(parseFilterStructure("(cn=x)"));
    expect(root?.kind).toBe("group");
    expect(root?.children).toHaveLength(1);
    expect(toBuilderRoot(null)).toBeNull();
  });

  it("reviveBuilderNode restores persisted presets with fresh ids", () => {
    const original = createBuilderGroup({ children: [createBuilderClause({ attribute: "cn", op: "equals", value: "x" })] });
    const json = JSON.parse(JSON.stringify(original));
    const revived = reviveBuilderNode(json);
    expect(revived).not.toBeNull();
    expect(buildNodeFilter(revived)).toBe(buildNodeFilter(original));
    expect(revived!.id).not.toBe(original.id);
    expect(revived!.kind === "group" && revived!.children[0].id).not.toBe(original.kind === "group" ? original.children[0].id : "");
  });

  it("reviveBuilderNode rejects malformed payloads", () => {
    expect(reviveBuilderNode(null)).toBeNull();
    expect(reviveBuilderNode({ kind: "clause", op: "nope" })).toBeNull();
    expect(reviveBuilderNode({ kind: "group", children: "nope" })).toBeNull();
    expect(reviveBuilderNode({ kind: "group", children: [{ kind: "clause", op: "bogus" }] })).toBeNull();
  });
});

describe("unescapeLdapFilterValue", () => {
  it("decodes hex escapes and leaves other text", () => {
    expect(unescapeLdapFilterValue("a\\2ab")).toBe("a*b");
    expect(unescapeLdapFilterValue("a\\5cb")).toBe("a\\b");
    expect(unescapeLdapFilterValue("plain")).toBe("plain");
    expect(unescapeLdapFilterValue("")).toBe("");
  });

  // -- adversarial boundary additions (round 3) --------------------------------

  it("decodes consecutive hex escapes as UTF-8 bytes, not per-char code points", () => {
    expect(unescapeLdapFilterValue("\\e4\\b8\\ad\\e6\\96\\87")).toBe("中文");
    expect(unescapeLdapFilterValue("u\\c3\\a9")).toBe("ué");
  });

  it("decodes the NUL escape and interleaved literal text", () => {
    expect(unescapeLdapFilterValue("a\\00b\\2ac")).toBe("a\u0000b*c");
  });

  it("round-trips an adversarial value through escape → parse → rebuild", () => {
    const value = "a(b)c\\d*e\u0000f中文";
    const filter = `(cn=${escapeLdapFilterValue(value)})`;
    const root = toBuilderRoot(parseFilterStructure(filter));
    expect(root).not.toBeNull();
    expect(buildNodeFilter(root)).toBe(filter);
    expect(root?.kind === "group" && (root.children[0] as { value?: string }).value).toBe(value);
  });

  it("keeps deeply nested AND/OR/NOT builder trees stable across string round-trip", () => {
    const filter = "(&(|(cn=a)(sn=b*))(!(mail=*c*))(|(uid=x)(!(ou=y))))";
    const root = toBuilderRoot(parseFilterStructure(filter));
    expect(buildNodeFilter(root)).toBe(filter);
    const revived = reviveBuilderNode(JSON.parse(JSON.stringify(root)));
    expect(buildNodeFilter(revived)).toBe(filter);
  });

  it("round-trips a long Chinese substring value", () => {
    const value = "长".repeat(200);
    const filter = `(cn=*${value}*)`;
    const root = toBuilderRoot(parseFilterStructure(filter));
    expect(buildNodeFilter(root)).toBe(filter);
  });

  it("falls back to source mode for unrepresentable substring shapes", () => {
    expect(parseFilterStructure("(cn=a*b*c)")).toBeNull();
    expect(parseFilterStructure("(cn=a**b)")).toBeNull();
  });

  it("normalizes a single-child AND group to its leaf (documented collapse)", () => {
    const root = toBuilderRoot(parseFilterStructure("(&(cn=a))"));
    expect(buildNodeFilter(root)).toBe("(cn=a)");
  });
});

describe("notEquals builder operator (ADS parity, M5-a)", () => {
  it("builds a negated equality leaf", () => {
    expect(buildBuilderClauseFilter(createBuilderClause({ attribute: "uid", op: "notEquals", value: "admin" }))).toBe("(!(uid=admin))");
  });

  it("escapes the value and drops empty clauses like other operators", () => {
    expect(buildBuilderClauseFilter(createBuilderClause({ attribute: "cn", op: "notEquals", value: "a(b" }))).toBe("(!(cn=a\\28b))");
    expect(buildBuilderClauseFilter(createBuilderClause({ attribute: "cn", op: "notEquals", value: "" }))).toBe("");
    expect(buildBuilderClauseFilter(createBuilderClause({ attribute: "", op: "notEquals", value: "x" }))).toBe("");
  });

  it("composes inside groups", () => {
    const root = createBuilderGroup({
      children: [
        createBuilderClause({ attribute: "cn", op: "equals", value: "a" }),
        createBuilderClause({ attribute: "uid", op: "notEquals", value: "admin" }),
      ],
    });
    expect(buildNodeFilter(root)).toBe("(&(cn=a)(!(uid=admin)))");
  });

  it("collectBuilderErrors requires a value for notEquals", () => {
    const errors = collectBuilderErrors(createBuilderClause({ attribute: "uid", op: "notEquals", value: "" }));
    expect(errors).toEqual([{ id: expect.any(String), code: "value_required" }]);
  });

  it("parses (!(attr=value)) into the ≠ operator and round-trips", () => {
    const parsed = parseFilterStructure("(!(uid=admin))");
    expect(parsed).not.toBeNull();
    expect(parsed?.kind === "clause" && parsed.op).toBe("notEquals");
    expect(parsed?.kind === "clause" && parsed.negate).toBeFalsy();
    expect(buildNodeFilter(parsed)).toBe("(!(uid=admin))");
  });

  it("keeps the negate flag for negated non-equality shapes", () => {
    const parsed = parseFilterStructure("(!(cn=a*))");
    expect(parsed?.kind === "clause" && parsed.op).toBe("startsWith");
    expect(parsed?.kind === "clause" && parsed.negate).toBe(true);
    expect(buildNodeFilter(parsed)).toBe("(!(cn=a*))");
  });

  it("reviveBuilderNode accepts persisted notEquals clauses", () => {
    const clause = createBuilderClause({ attribute: "uid", op: "notEquals", value: "admin" });
    const revived = reviveBuilderNode(JSON.parse(JSON.stringify(clause)));
    expect(revived).not.toBeNull();
    expect(buildNodeFilter(revived)).toBe("(!(uid=admin))");
    expect(reviveBuilderNode({ kind: "clause", op: "notEquals" })).not.toBeNull();
  });
});
