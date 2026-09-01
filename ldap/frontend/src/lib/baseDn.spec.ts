import { describe, expect, it } from "vitest";
import { inferBaseDnFromLdapHost, pickBaseDnFromRootDse } from "./baseDn";

describe("pickBaseDnFromRootDse", () => {
    it("prefers AD defaultNamingContext", () => {
        expect(
            pickBaseDnFromRootDse({
                namingContexts: ["CN=Configuration,DC=corp,DC=int,DC=kn", "DC=corp,DC=int,DC=kn"],
                defaultNamingContext: ["DC=corp,DC=int,DC=kn"],
            }),
        ).toBe("DC=corp,DC=int,DC=kn");
    });

    it("prefers the dc= context when defaultNamingContext is absent (OpenLDAP)", () => {
        expect(
            pickBaseDnFromRootDse({
                namingContexts: ["cn=Monitor", "dc=example,dc=org"],
            }),
        ).toBe("dc=example,dc=org");
    });

    it("falls back to the first context as-is", () => {
        expect(pickBaseDnFromRootDse({ namingContexts: ["o=corp"] })).toBe("o=corp");
    });

    it("trims and skips blank entries", () => {
        expect(pickBaseDnFromRootDse({ namingContexts: ["  ", "dc=a,dc=b"] })).toBe("dc=a,dc=b");
    });

    it("returns empty for missing attributes", () => {
        expect(pickBaseDnFromRootDse({})).toBe("");
    });
});

describe("inferBaseDnFromLdapHost", () => {
    it("maps a bare AD host to its domain DN", () => {
        expect(inferBaseDnFromLdapHost("corp.int.kn")).toBe("dc=corp,dc=int,dc=kn");
    });

    it("strips a machine-name prefix on multi-label hosts", () => {
        expect(inferBaseDnFromLdapHost("dc01.corp.example.com")).toBe("dc=corp,dc=example,dc=com");
    });

    it("rejects single-label and IP hosts", () => {
        expect(inferBaseDnFromLdapHost("localhost")).toBe("");
        expect(inferBaseDnFromLdapHost("127.0.0.1")).toBe("");
    });
});
