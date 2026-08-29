// Ported from tiny-rdm dn.test.js + baseDn.test.js (core cases) and
// dnWithinBase coverage for the DN whitelist semantics.
import { describe, expect, it } from "vitest";
import {
  dnWithinBase,
  isLikelyDn,
  isLikelyRdn,
  joinRdnAndParent,
  rdnConfirmationToken,
  splitFirstDnRdn,
} from "./dn";
import {
  extractLdapProfileHost,
  inferBaseDnCandidatesFromProfile,
  inferBaseDnFromLdapHost,
} from "./baseDn";

describe("splitFirstDnRdn", () => {
  it("splits the first RDN from the parent DN", () => {
    expect(splitFirstDnRdn("cn=jdoe,ou=people,dc=example,dc=com")).toEqual({
      rdn: "cn=jdoe",
      parentDn: "ou=people,dc=example,dc=com",
    });
  });

  it("respects escaped commas", () => {
    expect(splitFirstDnRdn("cn=Doe\\, John,ou=people")).toEqual({
      rdn: "cn=Doe\\, John",
      parentDn: "ou=people",
    });
  });

  it("respects quoted commas", () => {
    expect(splitFirstDnRdn('cn="Doe, John",ou=people')).toEqual({
      rdn: 'cn="Doe, John"',
      parentDn: "ou=people",
    });
  });

  it("handles bare RDN and empty input", () => {
    expect(splitFirstDnRdn("dc=com")).toEqual({ rdn: "dc=com", parentDn: "" });
    expect(splitFirstDnRdn("")).toEqual({ rdn: "", parentDn: "" });
  });
});

describe("rdnConfirmationToken", () => {
  it("decodes the first RDN value", () => {
    expect(rdnConfirmationToken("cn=John Doe,ou=people")).toBe("John Doe");
    expect(rdnConfirmationToken("cn=Doe\\2C John,ou=people")).toBe("Doe, John");
  });

  it("falls back to the whole RDN for multi-valued or malformed RDNs", () => {
    expect(rdnConfirmationToken("cn=a+uid=b")).toBe("cn=a+uid=b");
    expect(rdnConfirmationToken("")).toBe("");
  });
});

describe("isLikelyRdn / isLikelyDn", () => {
  it("accepts simple and multi-valued RDNs", () => {
    expect(isLikelyRdn("cn=jdoe")).toBe(true);
    expect(isLikelyRdn("cn=a+uid=b")).toBe(true);
    expect(isLikelyRdn("noequals")).toBe(false);
    expect(isLikelyRdn("cn=")).toBe(false);
  });

  it("accepts full DNs only when every part is an RDN", () => {
    expect(isLikelyDn("cn=jdoe,ou=people,dc=example,dc=com")).toBe(true);
    expect(isLikelyDn("not,a=dn")).toBe(false);
    expect(isLikelyDn("")).toBe(false);
  });
});

describe("joinRdnAndParent", () => {
  it("joins and normalises", () => {
    expect(joinRdnAndParent("cn=a", "dc=example,dc=com")).toBe("cn=a,dc=example,dc=com");
    expect(joinRdnAndParent("cn=a", "")).toBe("cn=a");
    expect(joinRdnAndParent("", "dc=com")).toBe("");
  });
});

describe("dnWithinBase", () => {
  it("matches the base itself and descendants", () => {
    const base = "ou=people,dc=example,dc=com";
    expect(dnWithinBase(base, base)).toBe(true);
    expect(dnWithinBase("cn=jdoe,ou=people,dc=example,dc=com", base)).toBe(true);
    expect(dnWithinBase("CN=jdoe,OU=people,DC=example,DC=com", base)).toBe(true);
  });

  it("rejects siblings and unrelated suffix collisions", () => {
    const base = "ou=people,dc=example,dc=com";
    expect(dnWithinBase("ou=groups,dc=example,dc=com", base)).toBe(false);
    expect(dnWithinBase("cn=jdoe,ou=peoplex,dc=example,dc=com", base)).toBe(false);
    expect(dnWithinBase("dc=com", base)).toBe(false);
  });

  it("empty whitelist base means unrestricted", () => {
    expect(dnWithinBase("cn=x,dc=com", "")).toBe(true);
    expect(dnWithinBase("", "dc=com")).toBe(false);
  });
});

describe("baseDn inference", () => {
  it("extracts host candidates from URLs", () => {
    expect(extractLdapProfileHost({ url: "ldaps://ldap.example.com:636" })).toBe("ldap.example.com");
    expect(extractLdapProfileHost({ host: "ad.corp.local" })).toBe("ad.corp.local");
  });

  it("skips leading service labels when inferring base DN", () => {
    expect(inferBaseDnFromLdapHost("ldap.example.com")).toBe("dc=example,dc=com");
    expect(inferBaseDnFromLdapHost("ad01.corp.local")).toBe("dc=corp,dc=local");
    expect(inferBaseDnFromLdapHost("example.com")).toBe("dc=example,dc=com");
  });

  it("rejects IPs and single labels", () => {
    expect(inferBaseDnFromLdapHost("127.0.0.1")).toBe("");
    expect(inferBaseDnFromLdapHost("localhost")).toBe("");
  });

  it("dedupes candidates per profile", () => {
    const candidates = inferBaseDnCandidatesFromProfile({ url: "ldap://ldap.corp.io", saslHost: "ldap.corp.io" });
    expect(candidates).toEqual(["dc=corp,dc=io"]);
  });
});
