/**
 * Visual fixture host bridge for the LDAP workbench (mock.html).
 *
 * Mirrors the ssh-sftp mockDbxHost pattern: an in-memory directory + a
 * `window.dbxPlugin` implementation so the workbench runs in a plain browser
 * (pnpm dev → /mock.html). URL params:
 *   ?theme=light|dark   appearance scheme (default dark)
 *   ?locale=zh-CN|en|…  workbench locale (default zh-CN)
 *   ?err=1              ldap/search always rejects (error-state fixture)
 *   ?noconn=1           host context has no connectionId (init error fixture)
 *   ?ro=1               readOnly connection (write actions disabled; write
 *                       attempts emit a denied `ldap/audit` event first, then
 *                       reject — mirrors the backend write-policy branch)
 */
import "./style.css";

const eventListeners = new Set<(event: DbxPluginEvent) => void>();
const appearanceListeners = new Set<(appearance: DbxPluginAppearance) => void>();
const contextListeners = new Set<(context: Record<string, unknown>) => void>();

const params = new URLSearchParams(location.search);
const readOnly = params.get("ro") === "1";

const context: Record<string, unknown> = {
  connectionId: params.get("noconn") === "1" ? "" : "visual-connection",
  workbenchId: "visual-workbench",
  restored: false,
  connection: {
    name: "Demo Directory",
    host: "ldap.demo.internal",
    port: 389,
    username: "cn=admin,dc=demo,dc=dbx",
    color: "#8b5cf6",
    readOnly,
    baseDn: "dc=demo,dc=dbx",
  },
};

const light = params.get("theme") === "light";
// 与 DBX globals.css 的 :root（pearl 浅色）和 .dark 规范块保持一致。
const appearance: DbxPluginAppearance = {
  colorScheme: light ? "light" : "dark",
  colors: light
    ? { background: "rgb(255 255 255)", foreground: "rgb(10 10 10)", muted: "rgb(245 245 245)", mutedForeground: "rgb(115 115 115)", accent: "rgb(245 245 245)", accentForeground: "rgb(23 23 23)", border: "rgb(229 229 229)", destructive: "rgb(231 0 11)" }
    : { background: "rgb(19 20 22)", foreground: "rgb(215 215 219)", muted: "rgb(42 42 45)", mutedForeground: "rgb(151 152 157)", accent: "rgb(46 47 51)", accentForeground: "rgb(221 221 226)", border: "rgb(110 110 114 / 0.28)", destructive: "rgb(243 98 95)" },
  terminal: { fontFamily: "Cascadia Mono, Consolas, monospace", fontSize: 13 },
};

// -- in-memory directory ------------------------------------------------------

interface MockEntry {
  dn: string;
  attributes: Record<string, string[]>;
}

const BASE_DN = "dc=demo,dc=dbx";
const directory = new Map<string, MockEntry>();

function put(dn: string, attributes: Record<string, string[]>) {
  directory.set(dn.toLowerCase(), { dn, attributes });
}

function get(dn: string): MockEntry | undefined {
  return directory.get(dn.toLowerCase());
}

function rdnOf(dn: string): string {
  const index = dn.indexOf(",");
  return index > 0 ? dn.slice(0, index) : dn;
}

function parentOf(dn: string): string {
  const index = dn.indexOf(",");
  return index > 0 ? dn.slice(index + 1) : dn;
}

put(BASE_DN, { dc: ["demo"], o: ["Demo Organization"], objectClass: ["dcObject", "organization"] });
put(`ou=people,${BASE_DN}`, { ou: ["people"], objectClass: ["organizationalUnit"] });
put(`ou=groups,${BASE_DN}`, { ou: ["groups"], objectClass: ["organizationalUnit"] });
put(`ou=services,${BASE_DN}`, { ou: ["services"], objectClass: ["organizationalUnit"] });
put(`cn=ldap,ou=services,${BASE_DN}`, { cn: ["ldap"], objectClass: ["applicationProcess"] });
put(`cn=web,ou=services,${BASE_DN}`, { cn: ["web"], objectClass: ["applicationProcess"] });
put(`cn=admins,ou=groups,${BASE_DN}`, { cn: ["admins"], memberUid: ["user1", "user2", "user3"], objectClass: ["groupOfNames"] });
put(`cn=devs,ou=groups,${BASE_DN}`, { cn: ["devs"], memberUid: ["user4"], objectClass: ["groupOfNames"] });

for (let index = 0; index < 1000; index += 1) {
  const uid = `user${String(index).padStart(4, "0")}`;
  put(`uid=${uid},ou=people,${BASE_DN}`, {
    uid: [uid],
    cn: [`User ${index}`],
    sn: [`Surname${index}`],
    mail: [`${uid}@demo.internal`],
    telephoneNumber: [`+1 555 0${String(index).padStart(4, "0")}`],
    objectClass: ["inetOrgPerson", "organizationalPerson", "person", "top"],
  });
}

// -- RFC 4515 filter evaluation (fixture-grade) -------------------------------

function decodeFilterValue(value: string): string {
  return value.replace(/\\([0-9a-f]{2})/giu, (_, hex) => String.fromCharCode(Number.parseInt(hex, 16)));
}

function matchItem(entry: MockEntry, attribute: string, rawValue: string): boolean {
  const values = entry.attributes[attribute] ?? [];
  if (rawValue === "*") return values.length > 0;
  if (!rawValue.includes("*")) {
    return values.some((value) => value.toLowerCase() === decodeFilterValue(rawValue).toLowerCase());
  }
  const parts = rawValue.split("*").map(decodeFilterValue).map((part) => part.toLowerCase());
  return values.some((value) => {
    const text = value.toLowerCase();
    let cursor = 0;
    for (let index = 0; index < parts.length; index += 1) {
      const part = parts[index];
      if (part === "") continue;
      const found = text.indexOf(part, cursor);
      if (found < 0) return false;
      if (index === 0 && found !== 0) return false;
      cursor = found + part.length;
    }
    if (parts[parts.length - 1] !== "" && !text.endsWith(parts[parts.length - 1])) return false;
    return true;
  });
}

function matchFilter(entry: MockEntry, filter: string): boolean {
  const text = filter.trim();
  if (!text.startsWith("(") || !text.endsWith(")")) return false;
  const operator = text[1];
  if (operator === "&" || operator === "|") {
    let position = 2;
    const children: string[] = [];
    while (text[position] === "(") {
      let depth = 0;
      const start = position;
      for (; position < text.length; position += 1) {
        if (text[position] === "(") depth += 1;
        else if (text[position] === ")") {
          depth -= 1;
          if (depth === 0) break;
        }
      }
      children.push(text.slice(start, position + 1));
      position += 1;
    }
    return operator === "&" ? children.every((child) => matchFilter(entry, child)) : children.some((child) => matchFilter(entry, child));
  }
  if (operator === "!") return !matchFilter(entry, text.slice(2, -1));
  const item = text.slice(1, -1);
  const equals = item.indexOf("=");
  if (equals < 0) return false;
  const attribute = item.slice(0, equals).replace(/~$|>=$|<=$/, "");
  const value = item.slice(equals + 1);
  // fixture 属性名大小写不敏感（cn/uid 等真实目录同理）。
  return [attribute, attribute.toLowerCase()].some((candidate) => matchItem(entry, candidate, value));
}

function dnWithinBase(dn: string, baseDn: string): boolean {
  const a = dn.toLowerCase();
  const b = baseDn.toLowerCase();
  return a === b || a.endsWith(`,${b}`);
}

function search(params_: Record<string, unknown>): { entries: MockEntry[]; count: number; truncated: boolean } {
  if (params.get("err") === "1") throw new Error("connection lost (fixture error injection)");
  const baseDn = String(params_.baseDn ?? BASE_DN);
  const filter = String(params_.filter ?? "(objectClass=*)");
  const scope = String(params_.scope ?? "sub");
  const sizeLimit = Number(params_.sizeLimit ?? 0) || 0;
  const wanted = Array.isArray(params_.attributes) ? (params_.attributes as string[]).map(String) : null;
  const matched: MockEntry[] = [];
  for (const entry of directory.values()) {
    if (!dnWithinBase(entry.dn, baseDn)) continue;
    if (scope === "base" && entry.dn.toLowerCase() !== baseDn.toLowerCase()) continue;
    if (scope === "one") {
      const relative = entry.dn.slice(0, entry.dn.length - baseDn.length - 1);
      if (relative.includes(",")) continue;
    }
    if (!matchFilter(entry, filter)) continue;
    matched.push(entry);
    if (sizeLimit > 0 && matched.length >= sizeLimit) break;
  }
  const entries = matched.map((entry) => ({
    dn: entry.dn,
    attributes: wanted
      ? Object.fromEntries(Object.entries(entry.attributes).filter(([name]) => wanted.some((w) => w.toLowerCase() === name.toLowerCase() || w === "dn")))
      : { ...entry.attributes },
  }));
  return { entries, count: entries.length, truncated: false };
}

// ldap/count（A-LDAP 契约：scope=one，上限 5000）——直接子条目精确计数，
// 不受 search sizeLimit 截断影响；超上限折算为 truncated:true。
function countChildren(params_: Record<string, unknown>): { count: number; truncated: boolean } {
  if (params.get("err") === "1") throw new Error("connection lost (fixture error injection)");
  const baseDn = String(params_.baseDn ?? BASE_DN);
  const filter = String(params_.filter ?? "(objectClass=*)");
  const limit = 5000;
  let count = 0;
  let truncated = false;
  for (const entry of directory.values()) {
    if (!dnWithinBase(entry.dn, baseDn)) continue;
    if (entry.dn.toLowerCase() === baseDn.toLowerCase()) continue;
    const relative = entry.dn.slice(0, entry.dn.length - baseDn.length - 1);
    if (relative.includes(",")) continue;
    if (!matchFilter(entry, filter)) continue;
    count += 1;
    if (count >= limit) {
      truncated = true;
      break;
    }
  }
  return { count, truncated };
}

// -- schema fixture (RFC 4512 descriptions) -----------------------------------

const attributeTypeDefinitions = [
  "( 2.5.4.3 NAME ( 'cn' 'commonName' ) SUP name )",
  "( 2.5.4.4 NAME ( 'sn' 'surname' ) SUP name )",
  "( 2.5.4.12 NAME 'title' SUP name )",
  "( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{32768} )",
  "( 2.5.4.42 NAME ( 'givenName' 'gn' ) SUP name )",
  "( 0.9.2342.19200300.100.1.1 NAME ( 'uid' 'userid' ) EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{256} )",
  "( 0.9.2342.19200300.100.1.3 NAME ( 'mail' 'rfc822Mailbox' ) EQUALITY caseIgnoreIA5Match SUBSTR caseIgnoreIA5SubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.26{256} )",
  "( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
  "( 2.5.4.10 NAME ( 'o' 'organizationName' ) EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{64} )",
  "( 2.5.4.11 NAME ( 'ou' 'organizationalUnitName' ) EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{64} )",
  "( 2.5.4.7 NAME ( 'l' 'localityName' ) SUP name )",
  "( 2.5.4.20 NAME 'telephoneNumber' EQUALITY telephoneNumberMatch SUBSTR telephoneNumberSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.50{32} )",
  "( 2.5.4.31 NAME 'member' SUP distinguishedName )",
  "( 2.5.4.49 NAME 'distinguishedName' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
  "( 2.5.4.50 NAME 'uniqueMember' EQUALITY nameAndOptionalUID SYNTAX 1.3.6.1.4.1.1466.115.121.1.34 )",
  "( 2.16.840.1.113730.3.1.241 NAME 'displayName' SUP name )",
  "( 2.5.4.18 NAME 'seeAlso' SUP distinguishedName )",
  "( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SUBSTR caseIgnoreSubstringsMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15{1024} )",
];

const objectClassDefinitions = [
  "( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
  "( 2.5.6.4 NAME 'organization' SUP top STRUCTURAL MUST o MAY ( userPassword $ searchGuide $ seeAlso $ businessCategory $ x500UniqueIdentifier $ preferredDeliveryMethod ) )",
  "( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou MAY ( userPassword $ searchGuide $ seeAlso $ businessCategory ) )",
  "( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ telephoneNumber $ seeAlso $ description ) )",
  "( 2.5.6.7 NAME 'organizationalPerson' SUP person STRUCTURAL MAY ( title $ x121Address $ registeredAddress $ destinationIndicator $ preferredDeliveryMethod $ telexNumber $ teletexTerminalIdentifier $ telephoneNumber $ internationaliSDNNumber $ facsimileTelephoneNumber $ street $ postOfficeBox $ postalCode $ postalAddress $ physicalDeliveryOfficeName $ ou $ st $ l ) )",
  "( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL MAY ( audio $ businessCategory $ carLicense $ departmentNumber $ displayName $ employeeNumber $ employeeType $ givenName $ homePhone $ homePostalAddress $ initials $ jpegPhoto $ labeledURI $ mail $ manager $ mobile $ o $ pager $ photo $ roomNumber $ secretary $ uid $ userCertificate $ x500uniqueIdentifier $ preferredLanguage $ userSMIMECertificate $ userPKCS12 ) )",
  "( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( cn $ member ) MAY ( businessCategory $ seeAlso $ owner $ ou $ o $ description ) )",
  "( 2.5.6.1 NAME 'alias' SUP top STRUCTURAL MUST aliasedObjectName )",
  "( 2.5.6.2 NAME 'country' SUP top STRUCTURAL MUST c MAY ( searchGuide $ description ) )",
  "( 2.5.6.3 NAME 'locality' SUP top STRUCTURAL MUST l MAY ( searchGuide $ description ) )",
  "( 1.3.6.1.4.1.1466.115.121.1.15 NAME 'applicationProcess' SUP top STRUCTURAL MUST cn MAY ( seeAlso $ ou $ l $ description ) )",
  "( 2.5.20.1 NAME 'subschema' AUXILIARY MAY ( dITContentRules $ dITStructureRules $ namingContexts $ subordinateSuffix $ objectClasses $ attributeTypes $ matchingRules $ matchingRuleUse ) )",
];

// -- request / invoke ----------------------------------------------------------

const request: DbxPluginApi["request"] = async <T = unknown>(method: string) =>
  (method === "host.getContext" ? context : null) as T;

// 事件注入（与 sidecar emitter.Event 同面）：denied audit fixture 用。
function emitEvent(method: string, eventParams: Record<string, unknown>) {
  for (const listener of eventListeners) listener({ method, params: eventParams } as DbxPluginEvent);
}

// readOnly 下写操作被策略拒绝：先发 denied audit 事件（App.vue showError 横幅
// 与 audit.jsonl 的 denied 语义对应），再抛业务错误（与后端 write-policy 分支
// 行为一致：AuditRecord{Action:"write-policy", Result:"denied"}）。
function denyWrite(target: string): never {
  emitEvent("ldap/audit", {
    connectionId: context.connectionId,
    action: "write-policy",
    target,
    result: "denied",
    detail: "connection is read-only (fixture)",
  });
  throw new Error("connection is read-only (fixture)");
}

const invoke: DbxPluginApi["invoke"] = async <T = unknown>(method: string, rawParams?: unknown) => {
  const input = (rawParams ?? {}) as Record<string, unknown>;
  let result: unknown = { success: true };
  if (method === "ldap/search") result = search(input);
  else if (method === "ldap/entry/get") {
    const entry = get(String(input.dn ?? ""));
    if (!entry) throw new Error(`entry not found: ${input.dn}`);
    result = { entry };
  } else if (method === "ldap/rootDse") {
    result = {
      attributes: {
        namingContexts: [BASE_DN],
        supportedLDAPVersion: ["3"],
        supportedSASLMechanisms: ["SIMPLE", "EXTERNAL"],
        subschemaSubentry: ["cn=Subschema"],
        vendorName: ["FixtureLDAP"],
      },
    };
  } else if (method === "ldap/schema") result = { attributeTypes: attributeTypeDefinitions, objectClasses: objectClassDefinitions };
  else if (method === "ldap/count") result = countChildren(input);
  else if (method === "ldap/entry/add") {
    if (readOnly) denyWrite(String(input.dn ?? ""));
    const dn = String(input.dn ?? "");
    if (get(dn)) throw new Error(`entry already exists: ${dn}`);
    if (!dnWithinBase(dn, BASE_DN)) throw new Error(`base DN allowlist rejected: ${dn}`);
    put(dn, Object.fromEntries(Object.entries((input.attributes ?? {}) as Record<string, string[]>)));
  } else if (method === "ldap/entry/modify") {
    if (readOnly) denyWrite(String(input.dn ?? ""));
    const entry = get(String(input.dn ?? ""));
    if (!entry) throw new Error(`entry not found: ${input.dn}`);
    for (const change of (input.changes ?? []) as Array<Record<string, unknown>>) {
      const attribute = String(change.attribute);
      const values = Array.isArray(change.values) ? (change.values as string[]).map(String) : [];
      if (change.operation === "delete") delete entry.attributes[attribute];
      else entry.attributes[attribute] = values;
    }
  } else if (method === "ldap/entry/delete") {
    if (readOnly) denyWrite(String(input.dn ?? ""));
    const dn = String(input.dn ?? "");
    if (!get(dn)) throw new Error(`entry not found: ${dn}`);
    directory.delete(dn.toLowerCase());
  } else if (method === "ldap/entry/modifyDn") {
    if (readOnly) denyWrite(String(input.dn ?? ""));
    const dn = String(input.dn ?? "");
    const entry = get(dn);
    if (!entry) throw new Error(`entry not found: ${dn}`);
    const newDn = `${input.newRdn},${input.newParentDn ? String(input.newParentDn) : parentOf(dn)}`;
    directory.delete(dn.toLowerCase());
    put(newDn, entry.attributes);
  } else if (method === "ldap/connections/statuses") {
    // 字段名与后端契约一致：status（三态）+ unix 毫秒 lastUsedAt。
    result = { statuses: [{ connectionId: String(context.connectionId), status: "connected", readOnly, lastUsedAt: Date.now() }] };
  } else if (method === "ldap/presets/list") {
    result = { presets: JSON.parse(localStorage.getItem("ldap-mock-presets") ?? "[]") };
  } else if (method === "ldap/presets/save") {
    const presets = JSON.parse(localStorage.getItem("ldap-mock-presets") ?? "[]") as unknown[];
    const incoming = (input.preset ?? {}) as Record<string, unknown>;
    const index = presets.findIndex((preset) => (preset as Record<string, unknown>).id === incoming.id);
    if (index >= 0) presets[index] = incoming;
    else presets.push(incoming);
    localStorage.setItem("ldap-mock-presets", JSON.stringify(presets));
    result = { presets };
  } else if (method === "ldap/presets/remove") {
    const id = String(input.id ?? "");
    const presets = (JSON.parse(localStorage.getItem("ldap-mock-presets") ?? "[]") as Array<Record<string, unknown>>).filter((preset) => preset.id !== id);
    localStorage.setItem("ldap-mock-presets", JSON.stringify(presets));
    result = { presets };
  } else if (method === "ldap/audit") {
    // never emitted by the mock host itself
  }
  return result as T;
};

// window.dbxPlugin 组装（与宿主 Host API 1.0 面一致）。
window.dbxPlugin = {
  ready: Promise.resolve(context),
  context,
  appearance,
  locale: params.get("locale") || "zh-CN",
  request,
  invoke,
  notify: async () => undefined,
  sendBinary: async () => undefined,
  onEvent: (listener) => {
    eventListeners.add(listener);
    return () => eventListeners.delete(listener);
  },
  onBinary: () => () => undefined,
  onAppearanceChange: (listener) => {
    appearanceListeners.add(listener);
    listener(appearance);
    return () => appearanceListeners.delete(listener);
  },
  onLocaleChange: () => () => undefined,
  onContextChange: (listener) => {
    contextListeners.add(listener);
    listener(context);
    return () => contextListeners.delete(listener);
  },
  decodeBase64: (value) => Uint8Array.from(atob(value), (character) => character.charCodeAt(0)),
  encodeBase64: (value) => {
    const bytes = value instanceof Uint8Array ? value : new Uint8Array(value as ArrayBuffer);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary);
  },
  workbenchState: { set: async () => undefined },
  clipboard: { readText: async () => "", writeText: async () => undefined },
};

export { context, appearance };
