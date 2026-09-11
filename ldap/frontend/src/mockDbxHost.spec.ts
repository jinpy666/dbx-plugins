// @vitest-environment happy-dom
// Visual fixture host bridge (mockDbxHost) unit tests for the
// ldap/entry/delete recursive path: the fixture mirrors the backend N1
// behaviour in simplified form (whole-subtree removal + one aggregated
// "subtree_delete" audit record with deletedCount, incl. the target itself).
import { describe, expect, it } from "vitest";
import "./mockDbxHost";

const searchCount = async (baseDn: string) =>
  ((await window.dbxPlugin.invoke<{ entries: unknown[] }>("ldap/search", { baseDn, filter: "(objectClass=*)", scope: "sub" })).entries).length;

const deleteEntry = (params: Record<string, unknown>) => window.dbxPlugin.invoke("ldap/entry/delete", params);

describe("mockDbxHost ldap/entry/delete", () => {
  it("recursive delete removes the whole subtree and emits one aggregated subtree_delete audit", async () => {
    const events: Array<Record<string, unknown>> = [];
    const off = window.dbxPlugin.onEvent((event) => events.push(event.params));
    try {
      const target = "ou=services,dc=demo,dc=dbx";
      expect(await searchCount(target)).toBe(3); // ou=services + cn=ldap + cn=web
      await deleteEntry({ dn: target, recursive: true });
      expect(await searchCount(target)).toBe(0);
      const audits = events.filter((event) => event.action === "subtree_delete");
      expect(audits).toHaveLength(1);
      expect(audits[0]).toMatchObject({ target, result: "ok", deletedCount: 3 });
    } finally {
      off();
    }
  });

  it("non-recursive delete removes only the target entry and keeps its siblings", async () => {
    await deleteEntry({ dn: "cn=admins,ou=groups,dc=demo,dc=dbx" });
    expect(await searchCount("cn=admins,ou=groups,dc=demo,dc=dbx")).toBe(0);
    // ou=groups 自身 + devs + team-a 仍在。
    expect(await searchCount("ou=groups,dc=demo,dc=dbx")).toBe(3);
  });

  it("recursive delete is not capped in the fixture (1001-entry people tree goes in one call)", async () => {
    const people = "ou=people,dc=demo,dc=dbx";
    expect(await searchCount(people)).toBe(1001); // ou=people + 1000 users
    await deleteEntry({ dn: people, recursive: true });
    expect(await searchCount(people)).toBe(0);
    // 其他树不受影响。
    expect(await searchCount("ou=groups,dc=demo,dc=dbx")).toBe(3);
  });

  it("delete rejects a missing dn (recursive or not)", async () => {
    await expect(deleteEntry({ dn: "cn=ghost,dc=demo,dc=dbx" })).rejects.toThrow("entry not found");
    await expect(deleteEntry({ dn: "cn=ghost,dc=demo,dc=dbx", recursive: true })).rejects.toThrow("entry not found");
  });
});
