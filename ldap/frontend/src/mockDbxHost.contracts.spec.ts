// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ldapApi, setLdapConnectionId, type LdapEntry, type LdapSearchResult } from "./lib/api";

const baseDn = "dc=demo,dc=dbx";
const people = `ou=people,${baseDn}`;
const userDn = `uid=user0000,${people}`;
const invoke = <T = unknown>(method: string, params?: unknown) => window.dbxPlugin.invoke<T>(method, params);
const read = async (dn = userDn, attributes?: string[]) =>
  (await invoke<{ entry: LdapEntry }>("ldap/entry/get", { dn, attributes })).entry;
const add = (dn: string, attributes: LdapEntry["attributes"]) => invoke("ldap/entry/add", { dn, attributes });

beforeEach(async () => {
  vi.resetModules();
  await import("./mockDbxHost");
  setLdapConnectionId("visual-connection");
});

describe("mock search/read contract", () => {
  it.each([
    { attributes: ["dn"], keys: [] },
    { attributes: ["1.1"], keys: [] },
    { attributes: ["dn", " CN "], keys: ["cn"] },
    { attributes: ["1.1", "UID"], keys: ["uid"] },
    { attributes: ["+"], keys: [] },
  ])("projects $attributes without treating dn as a wildcard", async ({ attributes, keys }) => {
    const result = await invoke<LdapSearchResult>("ldap/search", { baseDn: userDn, scope: "base", attributes });
    expect(result.entries).toHaveLength(1);
    expect(result.entries[0].dn).toBe(userDn);
    expect(Object.keys(result.entries[0].attributes)).toEqual(keys);
    expect(await read(userDn, attributes)).toEqual(result.entries[0]);
  });

  it("uses empty/* selections for user attributes and typesOnly for names without values", async () => {
    const all = await read();
    expect(await read(userDn, [])).toEqual(all);
    expect(await read(userDn, ["*"])).toEqual(all);
    const result = await invoke<LdapSearchResult>("ldap/search", {
      baseDn: userDn, scope: "base", attributes: ["CN", "mail"], typesOnly: true,
    });
    expect(result.entries[0].attributes).toEqual({ cn: [], mail: [] });
    all.attributes.cn.push("client-side mutation");
    expect((await read()).attributes.cn).toEqual(["User 0"]);
  });

  it("honors explicit RootDSE attribute selection", async () => {
    expect(await invoke("ldap/rootDse", { attributes: ["NAMINGCONTEXTS"] })).toEqual({ attributes: { namingContexts: [baseDn] } });
    expect(await invoke("ldap/rootDse", { attributes: ["1.1"] })).toEqual({ attributes: {} });
    expect(await invoke("ldap/rootDse")).toEqual({ attributes: { objectClass: ["top"] } });
    const operational = await invoke<{ attributes: LdapEntry["attributes"] }>("ldap/rootDse", { attributes: ["+"] });
    expect(operational.attributes.namingContexts).toEqual([baseDn]);
    expect(operational.attributes).not.toHaveProperty("objectClass");
    const spy = vi.spyOn(window.dbxPlugin, "invoke");
    expect((await ldapApi.rootDse()).attributes).toMatchObject({ objectClass: ["top"], namingContexts: [baseDn] });
    expect(spy).toHaveBeenCalledWith("ldap/rootDse", { connectionId: "visual-connection", attributes: ["*", "+"] }, undefined);
  });

  it("aggregates transport pages and mirrors the Go exact-limit truncated flag", async () => {
    const query = { baseDn: people, scope: "one", attributes: ["1.1"] };
    for (const [pageSize, sizeLimit, count, truncated] of [[2, 4, 4, true], [2, 1000, 1000, true], [2, 1100, 1000, false], [200, 0, 500, true], [0, 0, 1000, false]] as const) {
      const result = await invoke<LdapSearchResult>("ldap/search", { ...query, pageSize, sizeLimit });
      expect(result).toMatchObject({ count, truncated, baseDn: people, filter: "(objectClass=*)" });
      expect(result.entries).toHaveLength(count);
    }
    await expect(invoke("ldap/search", { ...query, pageSize: 0, sizeLimit: 5 })).rejects.toThrow("Size Limit Exceeded");
  });

  it("excludes the search base from scope=one and handles an escaped comma in a direct child", async () => {
    const dn = String.raw`cn=comma\,name,${people}`;
    await add(dn, { cn: ["comma,name"], objectClass: ["person"] });
    const result = await invoke<LdapSearchResult>("ldap/search", { baseDn: people, scope: "one", filter: "(cn=comma,name)" });
    expect(result.entries.map((entry) => entry.dn)).toEqual([dn]);
    expect(await invoke("ldap/count", { baseDn: people, filter: "(cn=comma,name)" })).toEqual({ count: 1, truncated: false });
  });

  it("rejects malformed filters and effective unsupported alias dereferencing", async () => {
    await expect(invoke("ldap/search", { filter: "(uid" })).rejects.toThrow("invalid LDAP filter");
    await expect(invoke("ldap/search", { baseDn: userDn, scope: "base", derefAliases: "always" })).resolves.toMatchObject({ count: 1 });
    const alias = `cn=alias,${baseDn}`;
    await add(alias, { cn: ["alias"], objectClass: ["alias"], aliasedObjectName: [userDn] });
    await expect(invoke("ldap/search", { baseDn: alias, derefAliases: "always" })).rejects.toThrow("not implemented in the fixture");
    await expect(invoke("ldap/search", { baseDn: alias, derefAliases: "never" })).resolves.toMatchObject({ count: 1 });
  });

  it.each(["ldap/audit", "connection/test", "ldap/unknown"])("rejects %s instead of pretending success", async (method) => {
    await expect(invoke(method)).rejects.toMatchObject({ code: -32601 });
  });
});

describe("mock filter matching contract", () => {
  const root = `ou=filter-contract,${baseDn}`;
  const dns = [9, 10, 100].map((number) => `uid=n${number},${root}`);
  const searchDns = async (filter: string) => (await invoke<LdapSearchResult>("ldap/search", { baseDn: root, filter })).entries.map((entry) => entry.dn);

  beforeEach(async () => {
    await add(root, { objectClass: ["organizationalUnit"], ou: ["filter-contract"] });
    for (const [index, number] of [9, 10, 100].entries()) {
      await add(dns[index], { objectClass: ["inetOrgPerson", "posixAccount"], uid: [`n${number}`],
        cn: [number === 9 ? "研究*员" : number === 10 ? "研究员" : "User 100"], sn: ["Fixture"],
        uidNumber: [String(number)], gidNumber: ["1000"], homeDirectory: [`/home/n${number}`] });
    }
  });

  it("decodes escaped UTF-8 while keeping escaped stars literal in equality and substring filters", async () => {
    expect(await searchDns(String.raw`(cn=\e7\a0\94\e7\a9\b6\2a\e5\91\98)`)).toEqual([dns[0]]);
    expect(await searchDns(String.raw`(cn=*\e7\a0\94*)`)).toEqual(dns.slice(0, 2));
    expect(await searchDns("(cn=研究*员)")).toEqual(dns.slice(0, 2));
    expect(await searchDns(String.raw`(&(cn=*\e7\a0\94*)(!(uidNumber=10)))`)).toEqual([dns[0]]);
    expect(await invoke("ldap/count", { baseDn: root, filter: String.raw`(cn=*\e7\a0\94*)` })).toEqual({ count: 2, truncated: false });
  });

  it("uses integer ordering for RFC2307 uidNumber/gidNumber instead of lexical ordering", async () => {
    expect(await searchDns("(UIDNUMBER>=10)")).toEqual(dns.slice(1));
    expect(await searchDns("(uidNumber<=9)")).toEqual([dns[0]]);
    expect(await searchDns("(gidNumber>=1000)")).toEqual(dns);
    expect(await searchDns("(uidNumber=09)")).toEqual([]);
    expect(await invoke("ldap/count", { baseDn: root, filter: "(uidNumber>=10)" })).toEqual({ count: 2, truncated: false });
    const schema = await invoke<{ attributeTypes: string[] }>("ldap/schema");
    expect(schema.attributeTypes.filter((value) => /NAME '(uidNumber|gidNumber)'/.test(value))).toHaveLength(2);
  });

  it("does not round adjacent integers outside JavaScript's safe range", async () => {
    for (const value of ["9007199254740992", "9007199254740993"]) {
      await add(`uid=${value},${root}`, { objectClass: ["posixAccount"], uidNumber: [value] });
    }
    expect(await searchDns("(uidNumber>=9007199254740993)")).toEqual([`uid=9007199254740993,${root}`]);
  });

  it.each([
    "(cn~=User 100)", "(cn>=A)", "(cn:caseExactMatch:=User 100)",
    "(|(objectClass=*)(cn~=User 100))", "(&(uid=missing)(cn~=User 100))",
    "(!(cn:dn:=User 100))",
  ])("rejects unsupported matching rules before any boolean short-circuit: %s", async (filter) => {
    for (const method of ["ldap/search", "ldap/count"]) {
      await expect(invoke(method, { baseDn: root, filter })).rejects.toThrow("not implemented in the fixture");
    }
  });

  it.each(["(uid)", String.raw`(cn=bad\q)`])("uses the same assertion validation for search and count: %s", async (filter) => {
    for (const method of ["ldap/search", "ldap/count"]) {
      await expect(invoke(method, { baseDn: root, filter })).rejects.toThrow("invalid LDAP filter");
    }
  });
});

describe("mock alias support boundary", () => {
  const parent = `ou=services,${baseDn}`;
  const alias = `cn=alias,${parent}`;

  beforeEach(async () => {
    await add(alias, { cn: ["alias"], objectClass: ["top", "alias"], aliasedObjectName: [people] });
  });

  it.each(["never", "searching", "finding", "always"])("does not reject %s searches because of an unrelated alias", async (derefAliases) => {
    await expect(invoke("ldap/search", { baseDn: userDn, scope: "base", derefAliases })).resolves.toMatchObject({ count: 1 });
  });

  it("keeps the base alias in searching mode and ignores aliases below one-level scope", async () => {
    for (const scope of ["base", "sub"]) {
      await expect(invoke("ldap/search", { baseDn: alias, scope, derefAliases: "searching" })).resolves.toMatchObject({ entries: [{ dn: alias }] });
    }
    await expect(invoke("ldap/search", { baseDn: alias, scope: "one", derefAliases: "searching" })).resolves.toMatchObject({ count: 0 });
    await expect(invoke("ldap/search", { baseDn, scope: "one", derefAliases: "searching" })).resolves.toMatchObject({ count: 3 });
    await expect(invoke("ldap/search", { baseDn: parent, scope: "one", filter: "(objectClass=alias)", derefAliases: "finding" })).resolves.toMatchObject({ entries: [{ dn: alias }] });
  });

  it("rejects only searches that need unimplemented base/ancestor or scoped alias resolution", async () => {
    for (const base of [alias, `cn=child,${alias}`]) {
      await expect(invoke("ldap/search", { baseDn: base, scope: "base", derefAliases: "finding" })).rejects.toThrow("not implemented in the fixture");
    }
    for (const scope of ["one", "sub"]) {
      await expect(invoke("ldap/search", { baseDn: parent, scope, derefAliases: "searching" })).rejects.toThrow("not implemented in the fixture");
    }
    await expect(invoke("ldap/search", { baseDn: alias, scope: "base", derefAliases: " ALWAYS " })).rejects.toThrow("not implemented in the fixture");
  });
});

describe("mock writes and the real move field", () => {
  it("adds values, deletes only requested values, and treats attribute names case-insensitively", async () => {
    await ldapApi.entryModify(userDn, [{ operation: "replace", attribute: "description", values: ["first", "second"] }]);
    await ldapApi.entryModify(userDn, [{ operation: "add", attribute: "DESCRIPTION", values: ["third"] }]);
    await ldapApi.entryModify(userDn, [{ operation: "delete", attribute: "Description", values: ["second"] }]);
    expect((await read()).attributes.description).toEqual(["first", "third"]);
    await ldapApi.entryModify(userDn, [{ operation: "delete", attribute: "description", values: [] }]);
    expect((await read()).attributes).not.toHaveProperty("description");
  });

  it.each([
    { operation: "add", attribute: "cn", values: ["User 0"] },
    { operation: "delete", attribute: "cn", values: ["missing"] },
    { operation: "replace", attribute: "cn", values: [] },
    { operation: "typo", attribute: "cn", values: ["replacement"] },
  ])("keeps all attributes unchanged when a later $operation fails", async (change) => {
    const before = await read();
    await expect(invoke("ldap/entry/modify", { dn: userDn, changes: [
      { operation: "replace", attribute: "sn", values: ["must roll back"] }, change,
    ] })).rejects.toThrow();
    expect(await read()).toEqual(before);
  });

  it("sends newSuperior through the frontend API and moves a subtree with naming attributes", async () => {
    const source = `ou=move,${baseDn}`;
    const superior = `ou=services,${baseDn}`;
    const destination = `ou=moved,${superior}`;
    await add(source, { ou: ["move"], objectClass: ["organizationalUnit"] });
    await add(`cn=child,${source}`, { cn: ["child"], objectClass: ["person"] });
    const spy = vi.spyOn(window.dbxPlugin, "invoke");
    await ldapApi.entryModifyDn(source, "ou=moved", superior, true);
    expect(spy).toHaveBeenCalledWith("ldap/entry/modifyDn", {
      connectionId: "visual-connection", dn: source, newRdn: "ou=moved", newSuperior: superior, deleteOldRdn: true,
    }, undefined);
    expect((await read(destination)).attributes.ou).toEqual(["moved"]);
    expect((await read(`cn=child,${destination}`)).attributes.cn).toEqual(["child"]);
    await expect(read(source)).rejects.toThrow("entry not found");
    await expect(read(`cn=child,${source}`)).rejects.toThrow("entry not found");
  });

  it("retains the old RDN value when requested and decodes multi-valued escaped RDNs", async () => {
    const newRdn = String.raw`cn=new\,name+uid=\e7\a0\94\e7\a9\b6`;
    await ldapApi.entryModifyDn(userDn, newRdn, undefined, false);
    const moved = await read(`${newRdn},${people}`);
    expect(moved.attributes.cn).toEqual(["User 0", "new,name"]);
    expect(moved.attributes.uid).toEqual(["user0000", "研究"]);
  });

  it("rejects a destination collision or a move beneath itself without changing the source", async () => {
    const before = await read();
    await expect(ldapApi.entryModifyDn(userDn, "uid=user0001", undefined, true)).rejects.toThrow("entry already exists");
    await expect(ldapApi.entryModifyDn(people, "ou=moved", userDn, true)).rejects.toThrow("below itself");
    expect(await read()).toEqual(before);
  });
});
