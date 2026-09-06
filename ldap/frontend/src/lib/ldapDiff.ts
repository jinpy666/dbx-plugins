/** Diff two LDAP attribute maps into an RFC 4511 modify change list. */
export interface LdapModifyChange {
  operation: "add" | "replace" | "delete";
  attribute: string;
  values: string[];
}

export function diffChanges(
  before: Record<string, string[]>,
  after: Record<string, string[]>,
): LdapModifyChange[] {
  const changes: LdapModifyChange[] = [];
  const keys = new Set([...Object.keys(before || {}), ...Object.keys(after || {})]);
  for (const key of keys) {
    const beforeValues = before?.[key];
    const afterValues = after?.[key];
    if (!beforeValues && afterValues) changes.push({ operation: "add", attribute: key, values: afterValues });
    else if (beforeValues && !afterValues) changes.push({ operation: "delete", attribute: key, values: [] });
    else if (beforeValues && afterValues && beforeValues.join("\n") !== afterValues.join("\n")) {
      changes.push({ operation: "replace", attribute: key, values: afterValues });
    }
  }
  return changes;
}

/**
 * Editor row draft → attribute map. A single attribute value may itself contain
 * "\n", which is indistinguishable from the row textarea's value separator
 * after a join/split round-trip. Rows whose text is untouched therefore keep
 * their original values verbatim (`sourceValues`); only edited rows are split
 * on "\n" (empty segments dropped, duplicates collapsed).
 *
 * Repeated rows with the same attribute name merge into one multi-valued
 * attribute (values concatenated in row order, duplicates dropped) instead of
 * the later row silently overwriting the earlier one (UI 扫描 P2-14).
 */
export interface AttrRowDraftSource {
  name: string;
  valuesText: string;
  sourceValues?: string[];
}

export function attrRowsToAttributes(rows: readonly AttrRowDraftSource[]): Record<string, string[]> {
  const result: Record<string, string[]> = {};
  for (const row of rows || []) {
    const name = row.name.trim();
    if (!name) continue;
    let values: string[];
    if (row.sourceValues && row.valuesText === row.sourceValues.join("\n")) {
      values = [...row.sourceValues];
    } else {
      values = row.valuesText.split("\n").filter((value) => value !== "");
      values = [...new Set(values)];
    }
    if (values.length === 0) continue;
    const existing = result[name];
    result[name] = existing ? [...existing, ...values.filter((value) => !existing.includes(value))] : values;
  }
  return result;
}
