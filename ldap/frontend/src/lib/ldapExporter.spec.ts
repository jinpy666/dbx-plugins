// Ported from tiny-rdm ldapExporter.test.js (core cases).
import { describe, expect, it } from "vitest";
import {
  extractEntryAttributeNames,
  getEntryAttributeValues,
  serializeEntriesToCsv,
  serializeEntriesToJson,
} from "./ldapExporter";

const ENTRIES = [
  { dn: "cn=a,dc=com", attributes: { cn: ["a"], objectClass: ["top", "person"], mail: ["a@x", "a@y"] } },
  { dn: "cn=b,dc=com", attributes: { cn: ["b"], uid: "b" } },
];

describe("getEntryAttributeValues", () => {
  it("returns arrays for multivalued and scalars", () => {
    expect(getEntryAttributeValues(ENTRIES[0], "mail")).toEqual(["a@x", "a@y"]);
    expect(getEntryAttributeValues(ENTRIES[1], "uid")).toEqual(["b"]);
  });

  it("is case-insensitive", () => {
    expect(getEntryAttributeValues(ENTRIES[0], "CN")).toEqual(["a"]);
    expect(getEntryAttributeValues(ENTRIES[0], "MAIL")).toEqual(["a@x", "a@y"]);
  });

  it("returns empty for unknown attributes", () => {
    expect(getEntryAttributeValues(ENTRIES[0], "nope")).toEqual([]);
    expect(getEntryAttributeValues(ENTRIES[0], "")).toEqual([]);
  });
});

describe("extractEntryAttributeNames", () => {
  it("collects the union in first-seen order", () => {
    expect(extractEntryAttributeNames(ENTRIES)).toEqual(["cn", "objectClass", "mail", "uid"]);
  });
});

describe("serializeEntriesToCsv", () => {
  it("headers dn + attribute union, CRLF rows", () => {
    const csv = serializeEntriesToCsv(ENTRIES);
    const [header, rowA, rowB] = csv.split("\r\n");
    expect(header).toBe("dn,cn,objectClass,mail,uid");
    // dn contains a comma → RFC 4180 quoting
    expect(rowA).toBe('"cn=a,dc=com",a,top|person,a@x|a@y,');
    expect(rowB).toBe('"cn=b,dc=com",b,,,b');
  });

  it("quotes cells containing separators or quotes", () => {
    const csv = serializeEntriesToCsv([{ dn: "cn=x", attributes: { note: ['has "quote"'] } }]);
    expect(csv.split("\r\n")[1]).toContain('"has ""quote"""');
  });

  it("supports pinned columns and no header", () => {
    const csv = serializeEntriesToCsv(ENTRIES, { columns: ["cn"], includeHeader: false });
    expect(csv.split("\r\n")[0]).toBe('"cn=a,dc=com",a');
  });

  it("resolves pinned columns case-insensitively", () => {
    const csv = serializeEntriesToCsv(ENTRIES, { columns: ["CN", "Mail"], includeHeader: false });
    expect(csv.split("\r\n")[0]).toBe('"cn=a,dc=com",a,a@x|a@y');
  });
});

describe("serializeEntriesToJson", () => {
  it("normalises to dn + attributes shape", () => {
    const json = serializeEntriesToJson([{ DN: "cn=x", attrs: { cn: "x" } }]);
    expect(JSON.parse(json)).toEqual([{ dn: "cn=x", attributes: { cn: "x" } }]);
  });

  it("pretty-prints by default", () => {
    expect(serializeEntriesToJson([{ dn: "d", attributes: {} }], { pretty: false })).toBe('[{"dn":"d","attributes":{}}]');
    expect(serializeEntriesToJson([{ dn: "d", attributes: {} }])).toContain('\n');
  });
});
