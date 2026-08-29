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
