// Ported from tiny-rdm ldif.test.js (round-trip, folding, base64, errors).
import { describe, expect, it } from "vitest";
import { parseLdif, serializeEntriesToLdif, type LdifEntry } from "./ldif";

const SAMPLE_ENTRIES: LdifEntry[] = [
  {
    dn: "cn=Alice,ou=people,dc=example,dc=com",
    attributes: {
      objectClass: ["top", "person"],
      cn: ["Alice"],
      sn: ["Doe"],
      mail: ["alice@example.com"],
    },
  },
  {
    dn: "cn=Bob,ou=people,dc=example,dc=com",
    attributes: {
      cn: ["Bob"],
      description: ["multi\nline"],
    },
  },
];

describe("serializeEntriesToLdif", () => {
  it("emits a version header by default", () => {
    const text = serializeEntriesToLdif([{ dn: "dc=com", attributes: { dc: ["com"] } }]);
    expect(text.startsWith("version: 1\n")).toBe(true);
    expect(text).toContain("dn: dc=com");
    expect(text).toContain("dc: com");
  });

  it("omits the version header when asked", () => {
    const text = serializeEntriesToLdif([{ dn: "dc=com", attributes: {} }], { includeVersion: false });
    expect(text).toBe("dn: dc=com");
  });

  it("base64-encodes unsafe values", () => {
    const text = serializeEntriesToLdif([{ dn: "cn=x", attributes: { description: [" leading space"] } }], { includeVersion: false });
    expect(text).toContain("description::");
    // decoded value round-trips
    const parsed = parseLdif(text);
    expect(parsed.entries[0].attributes.description[0]).toBe(" leading space");
  });

  it("base64-encodes non-ASCII values and decodes UTF-8 correctly", () => {
    const text = serializeEntriesToLdif([{ dn: "cn=x", attributes: { cn: ["Jürgen-中文"] } }], { includeVersion: false });
    expect(text).toContain("cn::");
    const parsed = parseLdif(text);
    expect(parsed.entries[0].attributes.cn[0]).toBe("Jürgen-中文");
  });

  it("folds long lines at 76 chars with a single leading space", () => {
    const longValue = "x".repeat(200);
    const text = serializeEntriesToLdif([{ dn: "cn=x", attributes: { description: [longValue] } }], { includeVersion: false });
    for (const line of text.split("\n")) {
      expect(line.length).toBeLessThanOrEqual(76);
      if (line.startsWith(" ")) {
        expect(line.length).toBeLessThanOrEqual(76);
      }
    }
    const parsed = parseLdif(text);
    expect(parsed.entries[0].attributes.description[0]).toBe(longValue);
  });

  it("round-trips a multi-entry document", () => {
    const text = serializeEntriesToLdif(SAMPLE_ENTRIES);
    const parsed = parseLdif(text);
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries).toHaveLength(2);
    expect(parsed.entries[0]).toEqual(SAMPLE_ENTRIES[0]);
    expect(parsed.entries[1]).toEqual(SAMPLE_ENTRIES[1]);
  });

  // -- adversarial round-trip (RFC 2849 SAFE-STRING boundaries) ----------------

  const ADVERSARIAL_VALUES: Record<string, string[]> = {
    "leading-colon": [":colon-start"],
    "leading-less": ["<less-than"],
    "leading-space": [" leading"],
    "trailing-space": ["trailing "],
    "embedded-newline": ["line1\nline2"],
    "embedded-crlf": ["line1\r\nline2"],
    tab: ["a\tb"],
    chinese: ["中文值 Jürgen"],
    emoji: ["🎉emoji"],
    "long-500": ["x".repeat(500)],
    "hash-start": ["#not-comment"],
    "only-spaces": ["   "],
    empty: [""],
    equals: ["a=b;c"],
    asterisk: ["wil*d"],
  };

  it("round-trips adversarial values and a non-ASCII dn losslessly", () => {
    const entry: LdifEntry = { dn: "cn=tëst 中文,dc=example,dc=com", attributes: ADVERSARIAL_VALUES };
    const parsed = parseLdif(serializeEntriesToLdif([entry]));
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0]).toEqual(entry);
  });

  it("round-trips a folded base64 dn and multivalue attributes", () => {
    const entry: LdifEntry = {
      dn: `cn=${"长".repeat(60)},ou=people,dc=example,dc=com`,
      attributes: { cn: ["长".repeat(60)], mail: ["a@b.com", " b2 ", "c\nd"] },
    };
    const parsed = parseLdif(serializeEntriesToLdif([entry]));
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0]).toEqual(entry);
  });

  it("keeps folded comment continuation lines out of the entry stream", () => {
    const parsed = parseLdif("# a folded comment\n  continued\ndn: dc=com\ndc: com\n");
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries).toHaveLength(1);
    expect(parsed.entries[0].attributes.dc).toEqual(["com"]);
  });

  it("treats a version attribute inside an entry as data, not as a header", () => {
    const parsed = parseLdif("dn: dc=com\ndc: com\nversion: 9\n");
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0].attributes.version).toEqual(["9"]);
    expect(parsed.entries[0].attributes.dc).toEqual(["com"]);
  });
});

describe("parseLdif", () => {
  it("parses simple entries", () => {
    const parsed = parseLdif("dn: dc=com\ndc: com\nobjectClass: top\n");
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0].dn).toBe("dc=com");
    expect(parsed.entries[0].attributes.objectClass).toEqual(["top"]);
  });

  it("parses base64 values with ::", () => {
    // 'Jürgen' encoded
    const text = "dn: cn=x\ncn:: SsO8cmdlbg==\n";
    const parsed = parseLdif(text);
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0].attributes.cn[0]).toBe("Jürgen");
  });

  it("unfolds continuation lines", () => {
    const parsed = parseLdif("dn: cn=x\ndescription: first\n  second\n");
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries[0].attributes.description[0]).toBe("first second");
  });

  it("skips comments and version headers", () => {
    const parsed = parseLdif("# a comment\nversion: 1\n\ndn: dc=com\ndc: com\n");
    expect(parsed.errors).toEqual([]);
    expect(parsed.entries).toHaveLength(1);
  });

  it("reports changetype records as errors", () => {
    const parsed = parseLdif("dn: cn=x\nchangetype: modify\n");
    expect(parsed.entries).toHaveLength(0);
    expect(parsed.errors.some((error) => error.message.includes("changetype"))).toBe(true);
  });

  it("reports attributes before dn", () => {
    const parsed = parseLdif("cn: orphan\n");
    expect(parsed.entries).toHaveLength(0);
    expect(parsed.errors[0].message).toContain("before dn");
  });

  it("reports malformed lines with line numbers", () => {
    const parsed = parseLdif("dn: dc=com\n:novalue\n");
    expect(parsed.errors).toHaveLength(1);
    expect(parsed.errors[0].line).toBe(2);
  });

  it("handles CRLF input", () => {
    const parsed = parseLdif("dn: dc=com\r\ndc: com\r\n");
    expect(parsed.entries).toHaveLength(1);
  });

  it("treats empty input as no entries", () => {
    expect(parseLdif("")).toEqual({ entries: [], errors: [] });
  });
});
