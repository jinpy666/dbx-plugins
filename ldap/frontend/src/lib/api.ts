/**
 * Sidecar call wrapper for the LDAP workbench.
 *
 * All `ldap/*` domain methods take only `connectionId` (the workbench never
 * receives credentials — the host drives `connection/test|connect|disconnect`
 * itself). The current connection id comes from the host context delivered
 * over `window.dbxPlugin`. Errors surface through `showError(cause, "ldap")`.
 */

export type LdapScope = "base" | "one" | "sub";
export type LdapDerefAliases = "never" | "searching" | "finding" | "always";

export interface LdapEntry {
  dn: string;
  attributes: Record<string, string[]>;
}

export interface LdapSearchRequest {
  baseDn?: string;
  filter: string;
  scope: LdapScope;
  attributes?: string[];
  sizeLimit?: number;
  pageSize?: number;
  typesOnly?: boolean;
  derefAliases?: LdapDerefAliases;
}

export interface LdapSearchResult {
  entries: LdapEntry[];
  count: number;
  truncated: boolean;
}

export interface LdapModifyChange {
  operation: "add" | "replace" | "delete";
  attribute: string;
  values: string[];
}

export interface LdapSearchPreset {
  id: string;
  name: string;
  baseDn: string;
  filter: string;
  scope: LdapScope;
  attributes?: string[];
  sizeLimit?: number;
  /** Structured query-builder conditions (persisted alongside the filter string). */
  conditions?: unknown;
}

export interface LdapConnectionStatus {
  connectionId: string;
  /** 后端 LDAPConnectionStatus.Status（契约三态：connected | idle | error）。 */
  status: "connected" | "idle" | "error";
  /** 策略层只读门禁（表单 read_only ∥ 宿主标准 read_only），旧 sidecar 可能缺省。 */
  readOnly?: boolean;
  lastError?: string;
  /** unix 毫秒时间戳（sidecar JSON number）。 */
  lastUsedAt?: number;
}

export interface SchemaResult {
  attributeTypes: string[];
  objectClasses: string[];
}

/** Current connection id, injected by App.vue once the host context resolves. */
let currentConnectionId = "";

export function setLdapConnectionId(connectionId: string) {
  currentConnectionId = String(connectionId || "");
}

export function getLdapConnectionId(): string {
  return currentConnectionId;
}

function requireConnectionId(): string {
  if (!currentConnectionId) {
    throw new Error("LDAP connection context is not ready (missing connectionId)");
  }
  return currentConnectionId;
}

async function callLdap<T>(method: string, params: Record<string, unknown> = {}, options?: { timeoutMs?: number }): Promise<T> {
  const api = window.dbxPlugin;
  if (!api) throw new Error("DBX Host API unavailable");
  const invoke = (api.invoke ?? api.request).bind(api);
  return invoke<T>(method, { connectionId: requireConnectionId(), ...params }, options);
}

// -- domain methods (§5.2 of IMPL_PLAN_DBX_LDAP) -----------------------------

export const ldapApi = {
  search(params: LdapSearchRequest, options?: { timeoutMs?: number }) {
    return callLdap<LdapSearchResult>("ldap/search", { ...params }, options);
  },

  count(baseDn: string, filter?: string) {
    return callLdap<{ count: number; truncated?: boolean }>(
      "ldap/count",
      { baseDn, ...(filter ? { filter } : {}) },
      { timeoutMs: 15000 },
    );
  },

  entryGet(dn: string, attributes?: string[]) {
    return callLdap<{ entry: LdapEntry }>("ldap/entry/get", { dn, ...(attributes ? { attributes } : {}) });
  },

  rootDse(attributes?: string[]) {
    return callLdap<{ attributes: Record<string, string[]> }>(
      "ldap/rootDse",
      attributes?.length ? { attributes } : {},
    );
  },

  schema(refresh = false) {
    return callLdap<SchemaResult>("ldap/schema", refresh ? { refresh: true } : {});
  },

  entryAdd(dn: string, attributes: Record<string, string[]>) {
    return callLdap<{ success: boolean }>("ldap/entry/add", { dn, attributes });
  },

  entryModify(dn: string, changes: LdapModifyChange[]) {
    return callLdap<{ success: boolean }>("ldap/entry/modify", { dn, changes });
  },

  entryDelete(dn: string, recursive = false) {
    return callLdap<{ success: boolean }>(
      "ldap/entry/delete",
      recursive ? { dn, recursive: true } : { dn },
    );
  },

  childrenCount(dn: string) {
    return callLdap<{ count: number; truncated?: boolean }>(
      "ldap/entry/childrenCount",
      { dn },
      { timeoutMs: 15000 },
    );
  },

  entryModifyDn(dn: string, newRdn: string, newParentDn: string | undefined, deleteOldRdn: boolean) {
    return callLdap<{ success: boolean }>(
      "ldap/entry/modifyDn",
      { dn, newRdn, ...(newParentDn ? { newParentDn } : {}), deleteOldRdn },
    );
  },

  connectionStatuses() {
    return callLdap<{ statuses: LdapConnectionStatus[] }>("ldap/connections/statuses");
  },

  presetsList() {
    return callLdap<{ presets: LdapSearchPreset[] }>("ldap/presets/list");
  },

  presetsSave(preset: LdapSearchPreset) {
    return callLdap<{ presets: LdapSearchPreset[] }>("ldap/presets/save", { preset });
  },

  presetsRemove(id: string) {
    return callLdap<{ presets: LdapSearchPreset[] }>("ldap/presets/remove", { id });
  },
};

// -- audit event -------------------------------------------------------------

export interface LdapAuditEvent {
  connectionId: string;
  action: string;
  target: string;
  result: "ok" | "denied" | "error";
}
