import { describe, expect, it } from "vitest";
import { missingRequiredAttributes } from "./entryValidation";

const person = { objectClass: ["top", "person"], cn: ["Alice"], sn: ["A"] };

describe("editor MUST preflight", () => {
  it("requires objectClass and known MUST attributes for new entries", () => {
    expect(missingRequiredAttributes({})).toEqual(["objectClass"]);
    expect(missingRequiredAttributes({ objectClass: ["person"] })).toEqual(["cn", "sn"]);
  });

  it("detects removal or blanking of a previously visible required value", () => {
    expect(missingRequiredAttributes({ ...person, sn: [] }, person)).toEqual(["sn"]);
    expect(missingRequiredAttributes({ ...person, objectClass: [" "] }, person)).toEqual(["objectClass"]);
  });

  it("does not demand existing MUST attributes that were hidden or unreadable", () => {
    const partial = { objectClass: ["person", "simpleSecurityObject"], cn: ["Alice"] };
    expect(missingRequiredAttributes({ ...partial, description: ["Updated"] }, partial)).toEqual([]);
  });

  it("tracks newly introduced requirements and releases them when a class is removed", () => {
    expect(missingRequiredAttributes({ ...person, objectClass: ["person", "groupOfNames"] }, person)).toEqual(["member"]);
    expect(missingRequiredAttributes(person, person)).toEqual([]);
    expect(missingRequiredAttributes({ ...person, objectClass: ["person", "simpleSecurityObject"] }, person)).toEqual([]);
  });

  it("matches case and attribute options without changing their values", () => {
    expect(missingRequiredAttributes({ OBJECTCLASS: ["PERSON"], "CN;lang-en": ["Alice"], SN: ["A"] })).toEqual([]);
  });

  it("uses custom schema MUST and SUP inheritance", () => {
    const schema = {
      objectClassAttributes: { child: { must: [], may: [] }, parent: { must: ["employeeNumber"], may: [] } },
      rawObjectClasses: ["( 1.2.3 NAME 'child' SUP parent STRUCTURAL )"],
    };
    expect(missingRequiredAttributes({ objectClass: ["child"] }, undefined, schema)).toEqual(["employeeNumber"]);
  });
});
