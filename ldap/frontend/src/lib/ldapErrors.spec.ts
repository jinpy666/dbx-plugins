import { describe, expect, it } from "vitest";
import { friendlyLdapError } from "./ldapErrors";

describe("friendlyLdapError", () => {
    it("maps the go-ldap network error (original runtime bug) to a friendly message", () => {
        const mapped = friendlyLdapError(
            'dial ldap: LDAP Result Code 200 "Network Error": parse "ldaps://[ldaps:%2F%2Fdc]:636": invalid URL escape "%2F"',
        );
        expect(mapped).not.toContain("Result Code");
        expect(mapped.length).toBeGreaterThan(0);
    });

    it("maps invalid credentials", () => {
        expect(friendlyLdapError('LDAP Result Code 49 "Invalid Credentials": password wrong')).not.toBe("");
    });

    it("maps no such object", () => {
        expect(friendlyLdapError('search: LDAP Result Code 32 "No Such Object": dn mismatch')).not.toBe("");
    });

    it("maps tls certificate failures ahead of network rules", () => {
        expect(friendlyLdapError("tls: failed to verify certificate: x509: certificate signed by unknown authority")).not.toBe("");
    });

    it("maps dial timeouts to the network message", () => {
        expect(friendlyLdapError("dial tcp 10.0.0.1:636: i/o timeout")).not.toBe("");
    });

    it("maps connection lost / closed transport failures to the network message (P2-1)", () => {
        const raw = "connection lost (fixture error injection)";
        expect(friendlyLdapError(raw)).not.toBe(raw);
        expect(friendlyLdapError(raw)).toBe(friendlyLdapError("connection reset"));
        expect(friendlyLdapError("ldap: connection closed unexpectedly")).not.toContain("connection closed");
    });

    it("passes unknown messages through unchanged", () => {
        const raw = "some completely unknown sidecar failure";
        expect(friendlyLdapError(raw)).toBe(raw);
    });

    it("handles empty input", () => {
        expect(friendlyLdapError("")).toBe("");
    });
});
