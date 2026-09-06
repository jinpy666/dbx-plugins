import { describe, expect, it } from "vitest";
import { attrRowsToAttributes, diffChanges } from "./ldapDiff";

describe("attrRowsToAttributes", () => {
  it("splits edited rows on newline, dropping empties and duplicates", () => {
    expect(
      attrRowsToAttributes([
        { name: "cn", valuesText: "a\n\nb\na" },
        { name: "  ", valuesText: "ignored" },
        { name: "empty", valuesText: "" },
      ]),
    ).toEqual({ cn: ["a", "b"] });
  });

  it("keeps untouched multi-line values verbatim via sourceValues", () => {
    // A stored value may itself contain "\n"; a naive join/split would corrupt
    // it into two values and generate spurious modify changes on save.
    const original = ["line1\nline2", "plain"];
    expect(
      attrRowsToAttributes([{ name: "description", valuesText: original.join("\n"), sourceValues: original }]),
    ).toEqual({ description: original });
  });

  it("splits when the user edits an untouched row's text", () => {
    const original = ["line1\nline2"];
    expect(
      attrRowsToAttributes([{ name: "description", valuesText: `${original[0]}\nextra`, sourceValues: original }]),
    ).toEqual({ description: ["line1", "line2", "extra"] });
  });
});

describe("diffChanges", () => {
  it("emits add / delete / replace for attribute differences", () => {
    const before = { a: ["1"], b: ["2"], keep: ["k"] };
    const after = { a: ["1"], b: ["2b"], c: ["3"], keep: ["k"] };
    expect(diffChanges(before, after)).toEqual([
      { operation: "replace", attribute: "b", values: ["2b"] },
      { operation: "add", attribute: "c", values: ["3"] },
    ]);
  });

  it("emits delete for removed attributes", () => {
    expect(diffChanges({ b: ["2"] }, {})).toEqual([{ operation: "delete", attribute: "b", values: [] }]);
  });

  it("generates no changes for untouched multi-line values", () => {
    const before = { description: ["line1\nline2", "plain"] };
    expect(diffChanges(before, structuredClone(before))).toEqual([]);
  });
});

describe("attrRowsToAttributes duplicate-name merge (P2-14)", () => {
  it("merges repeated attribute rows into one multi-valued attribute instead of overwriting", () => {
    const rows = [
      { name: "mailx", valuesText: "dup-a@x" },
      { name: "mailx", valuesText: "dup-b@x" },
    ];
    expect(attrRowsToAttributes(rows)).toEqual({ mailx: ["dup-a@x", "dup-b@x"] });
  });

  it("drops duplicates across merged rows and keeps row order", () => {
    const rows = [
      { name: "mail", valuesText: "a@x\nb@x" },
      { name: "mail", valuesText: "b@x\nc@x" },
      { name: "mail", valuesText: "a@x" },
    ];
    expect(attrRowsToAttributes(rows)).toEqual({ mail: ["a@x", "b@x", "c@x"] });
  });

  it("merges an untouched sourceValues row with an edited duplicate", () => {
    const rows = [
      { name: "mail", valuesText: "keep@x", sourceValues: ["keep@x"] },
      { name: "mail", valuesText: "added@x" },
    ];
    expect(attrRowsToAttributes(rows)).toEqual({ mail: ["keep@x", "added@x"] });
  });

  it("ignores empty duplicate rows when merging", () => {
    const rows = [
      { name: "mail", valuesText: "a@x" },
      { name: "mail", valuesText: "" },
      { name: "  mail  ", valuesText: "b@x" },
    ];
    expect(attrRowsToAttributes(rows)).toEqual({ mail: ["a@x", "b@x"] });
  });

  it("emits one replace with the merged multi-value list in the diff", () => {
    const rows = [
      { name: "mail", valuesText: "a@x" },
      { name: "mail", valuesText: "b@x" },
    ];
    const after = attrRowsToAttributes(rows);
    expect(diffChanges({ mail: ["a@x"] }, after)).toEqual([
      { operation: "replace", attribute: "mail", values: ["a@x", "b@x"] },
    ]);
  });
});
