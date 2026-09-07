/**
 * LDAP search filter builders.
 *
 * Ported from tiny-rdm `frontend/src/modules/tools/ldap/utils/ldapFilter.js`
 * (RFC 4515 escaping applied at the value-leaf level so callers never need to
 * escape twice). Pure functions only — no Vue / DOM dependencies.
 */

// Backslash MUST appear first in the rotation order so that already-generated
// escape sequences (e.g. "\2a") never get double-escaped. The last rule
// escapes the NUL character (RFC 4515 \00); plain spaces stay untouched.
const ESCAPE_REPLACEMENTS: ReadonlyArray<readonly [RegExp, string]> = Object.freeze([
    [/\\/gu, '\\5c'],
    [/\*/gu, '\\2a'],
    [/\(/gu, '\\28'],
    [/\)/gu, '\\29'],
    [/\0/gu, '\\00'],
]);

const SUBSTRING_MODES = Object.freeze({
    contains: (attr: string, value: string) => `(${attr}=*${value}*)`,
    startsWith: (attr: string, value: string) => `(${attr}=${value}*)`,
    endsWith: (attr: string, value: string) => `(${attr}=*${value})`,
});

const COMPARISON_MODES = Object.freeze({
    gte: (attr: string, value: string) => `(${attr}>=${value})`,
    lte: (attr: string, value: string) => `(${attr}<=${value})`,
    approx: (attr: string, value: string) => `(${attr}~=${value})`,
});

const trimToString = (value: unknown): string => String(value ?? '').trim();

const LDAP_ATTRIBUTE_OPTION = '[A-Za-z0-9-]+';
const LDAP_ATTRIBUTE_DESCRIPTION_RE = new RegExp(
    `^(?:[A-Za-z][A-Za-z0-9-]*(;${LDAP_ATTRIBUTE_OPTION})*|[0-9]+(?:\\.[0-9]+)+(;${LDAP_ATTRIBUTE_OPTION})*)$`,
    'u',
);

export type SubstringMode = keyof typeof SUBSTRING_MODES;
export type ComparisonMode = keyof typeof COMPARISON_MODES;

/** Escape a single LDAP filter value per RFC 4515. */
export const escapeLdapFilterValue = (value: unknown): string => {
    let result = String(value ?? '');
    for (const [pattern, replacement] of ESCAPE_REPLACEMENTS) {
        result = result.replace(pattern, replacement);
    }
    return result;
};

export const buildPresenceFilter = (attribute: string): string => {
    const attr = trimToString(attribute);
    return attr ? `(${attr}=*)` : '';
};

export const buildEqualityFilter = (attribute: string, value: unknown): string => {
    const attr = trimToString(attribute);
    const raw = String(value ?? '').trim();
    if (!attr || !raw) return '';
    return `(${attr}=${escapeLdapFilterValue(raw)})`;
};

export const buildSubstringFilter = (attribute: string, value: unknown, mode: SubstringMode): string => {
    const attr = trimToString(attribute);
    const raw = String(value ?? '').trim();
    const handler = SUBSTRING_MODES[mode];
    if (!attr || !raw || !handler) return '';
    return handler(attr, escapeLdapFilterValue(raw));
};

export const buildComparisonFilter = (attribute: string, value: unknown, op: ComparisonMode): string => {
    const attr = trimToString(attribute);
    const raw = String(value ?? '').trim();
    const handler = COMPARISON_MODES[op];
    if (!attr || !raw || !handler) return '';
    return handler(attr, escapeLdapFilterValue(raw));
};

/**
 * Wrap a filter in a negation. Already-negated filters pass through unchanged
 * so we never produce `(!(!(...)))`.
 */
export const buildNegatedFilter = (filter: string): string => {
    const source = trimToString(filter);
    if (!source) return '';
    if (source.startsWith('(!') && source.endsWith(')')) return source;
    return `(!${source})`;
};

export const combineFilters = (filters: string[], join: 'and' | 'or'): string => {
    const cleaned = (filters || []).map((entry) => trimToString(entry)).filter(Boolean);
    if (cleaned.length === 0) return '';
    if (cleaned.length === 1) return cleaned[0];
    const operator = join === 'or' ? '|' : '&';
    return `(${operator}${cleaned.join('')})`;
};

export interface LdapQueryClause {
    field?: string;
    customField?: string;
    useCustomField?: boolean;
    op?: string;
    value?: unknown;
    negate?: boolean;
}

export interface LdapQueryGroup {
    join?: 'and' | 'or';
    clauses?: LdapQueryClause[];
}

export interface LdapQueryBuilder {
    join?: 'and' | 'or';
    groups?: LdapQueryGroup[];
}

export const resolveLdapQueryClauseAttribute = (clause?: LdapQueryClause): string =>
    trimToString(
        clause?.useCustomField === true || clause?.field === '__custom__' ? clause?.customField : clause?.field,
    );

/** Validate an LDAP attribute description accepted by the visual query builder. */
export const isValidLDAPAttributeDescription = (value: unknown): boolean =>
    LDAP_ATTRIBUTE_DESCRIPTION_RE.test(trimToString(value));

/** Translate a single clause descriptor into an LDAP filter. */
export const buildClauseFilter = (clause?: LdapQueryClause): string => {
    if (!clause) return '';
    const attribute = resolveLdapQueryClauseAttribute(clause);
    if (!attribute) return '';
    const op = trimToString(clause.op) || 'eq';
    const rawValue = String(clause.value ?? '').trim();

    let expression = '';
    if (op === 'present') {
        expression = buildPresenceFilter(attribute);
    } else if (op === 'eq' || op === 'notEq') {
        expression = buildEqualityFilter(attribute, rawValue);
    } else if (op in SUBSTRING_MODES) {
        expression = buildSubstringFilter(attribute, rawValue, op as SubstringMode);
    } else if (op in COMPARISON_MODES) {
        expression = buildComparisonFilter(attribute, rawValue, op as ComparisonMode);
    }

    if (!expression) return '';
    const shouldNegate = clause.negate === true || op === 'notEq';
    return shouldNegate ? buildNegatedFilter(expression) : expression;
};

export const buildGroupFilter = (group?: LdapQueryGroup): string => {
    const clauses = (group?.clauses || []).map(buildClauseFilter).filter(Boolean);
    return combineFilters(clauses, group?.join === 'or' ? 'or' : 'and');
};

export const buildQueryBuilderFilter = (builder?: LdapQueryBuilder): string => {
    const groups = (builder?.groups || []).map(buildGroupFilter).filter(Boolean);
    return combineFilters(groups, builder?.join === 'or' ? 'or' : 'and');
};

export interface QueryClauseError {
    field: 'field' | 'value';
    code: 'attribute_required' | 'attribute_invalid' | 'value_required';
    groupIndex: number;
    clauseIndex: number;
    attribute?: string;
    op?: string;
}

/** Validate a single visual query-builder clause without UI dependencies. */
export const validateQueryClause = (clause?: LdapQueryClause, groupIndex = 0, clauseIndex = 0): QueryClauseError | null => {
    const attribute = resolveLdapQueryClauseAttribute(clause);
    const normalizedGroupIndex = Number.isInteger(groupIndex) ? groupIndex : 0;
    const normalizedClauseIndex = Number.isInteger(clauseIndex) ? clauseIndex : 0;
    if (!attribute) {
        return { field: 'field', code: 'attribute_required', groupIndex: normalizedGroupIndex, clauseIndex: normalizedClauseIndex, attribute };
    }
    if (!isValidLDAPAttributeDescription(attribute)) {
        return { field: 'field', code: 'attribute_invalid', groupIndex: normalizedGroupIndex, clauseIndex: normalizedClauseIndex, attribute };
    }
    const op = trimToString(clause?.op) || 'eq';
    if (op !== 'present' && !trimToString(clause?.value)) {
        return { field: 'value', code: 'value_required', groupIndex: normalizedGroupIndex, clauseIndex: normalizedClauseIndex, attribute, op };
    }
    return null;
};

/** Validate all visual query-builder clauses. */
export const validateQueryBuilder = (builder?: LdapQueryBuilder): QueryClauseError[] => {
    const errors: QueryClauseError[] = [];
    for (const [groupIndex, group] of (builder?.groups || []).entries()) {
        for (const [clauseIndex, clause] of (group?.clauses || []).entries()) {
            const error = validateQueryClause(clause, groupIndex, clauseIndex);
            if (error) errors.push(error);
        }
    }
    return errors;
};

// -- visual builder tree (A-LDAP): clauses + nested AND/OR groups ------------

export type BuilderOp = "equals" | "notEquals" | "contains" | "startsWith" | "endsWith" | "present" | "gte" | "lte" | "approx";

export interface BuilderClause {
    kind: "clause";
    id: string;
    attribute: string;
    op: BuilderOp;
    value: string;
    negate?: boolean;
}

export interface BuilderGroup {
    kind: "group";
    id: string;
    join: "and" | "or";
    children: BuilderNode[];
    negate?: boolean;
}

export type BuilderNode = BuilderClause | BuilderGroup;

let builderNodeSequence = 0;
export const nextBuilderNodeId = (): string => `n${(builderNodeSequence += 1)}`;

export const createBuilderClause = (overrides: Partial<Omit<BuilderClause, "kind" | "id">> = {}): BuilderClause => ({
    kind: "clause",
    id: nextBuilderNodeId(),
    attribute: "",
    op: "equals",
    value: "",
    ...overrides,
});

export const createBuilderGroup = (overrides: Partial<Omit<BuilderGroup, "kind" | "id">> = {}): BuilderGroup => ({
    kind: "group",
    id: nextBuilderNodeId(),
    join: "and",
    children: [],
    ...overrides,
});

/** Translate a builder clause into its RFC 4515 filter leaf. */
export const buildBuilderClauseFilter = (clause: BuilderClause): string => {
    const attribute = trimToString(clause.attribute);
    const rawValue = String(clause.value ?? "").trim();
    switch (clause.op) {
        case "present":
            return buildPresenceFilter(attribute);
        case "equals":
            return buildEqualityFilter(attribute, rawValue);
        // RFC 4515 has no inequality operator: ≠ is the negation of equality
        // ((!(attr=value))), mirroring the tiny-rdm "notEq" clause semantics.
        case "notEquals": {
            const equality = buildEqualityFilter(attribute, rawValue);
            return equality ? buildNegatedFilter(equality) : "";
        }
        case "contains":
        case "startsWith":
        case "endsWith":
            return buildSubstringFilter(attribute, rawValue, clause.op);
        case "gte":
        case "lte":
        case "approx":
            return buildComparisonFilter(attribute, rawValue, clause.op);
        default:
            return "";
    }
};

/** Recursively build a filter from a builder node (empty children are dropped). */
export const buildNodeFilter = (node?: BuilderNode | null): string => {
    if (!node) return "";
    if (node.kind === "clause") {
        const expression = buildBuilderClauseFilter(node);
        if (!expression) return "";
        return node.negate ? buildNegatedFilter(expression) : expression;
    }
    const inner = combineFilters((node.children || []).map((child) => buildNodeFilter(child)), node.join === "or" ? "or" : "and");
    if (!inner) return "";
    return node.negate ? buildNegatedFilter(inner) : inner;
};

export interface BuilderNodeError {
    id: string;
    code: "attribute_required" | "attribute_invalid" | "value_required";
}

/** Validate every clause in a builder tree (UI-side pre-check before search).
 *  空条件（属性与值皆空）= 「无条件」匹配全部，合法不算错；半填（有属性无值
 *  等）仍报错。generatedFilter 为空串时由调用方回退 (objectClass=*)。 */
export const collectBuilderErrors = (node?: BuilderNode | null): BuilderNodeError[] => {
    if (!node) return [];
    if (node.kind === "clause") {
        const attribute = trimToString(node.attribute);
        const value = trimToString(node.value);
        if (!attribute && !value) return [];
        if (!attribute) return [{ id: node.id, code: "attribute_required" }];
        if (!isValidLDAPAttributeDescription(attribute)) return [{ id: node.id, code: "attribute_invalid" }];
        if (node.op !== "present" && !value) return [{ id: node.id, code: "value_required" }];
        return [];
    }
    return (node.children || []).flatMap((child) => collectBuilderErrors(child));
};

// -- RFC 4515 source-string parsing (best-effort filter → builder tree) ------

/**
 * Decode `\XX` hex escapes produced by escapeLdapFilterValue (and servers).
 * RFC 4515 escapes are byte-oriented: consecutive hex escapes form UTF-8 bytes
 * (e.g. `\e4\b8\ad` → 中), so escape runs are decoded as UTF-8 rather than
 * char-by-char (which would mojibake multi-byte characters).
 */
export const unescapeLdapFilterValue = (value: string): string => {
    const source = String(value ?? "");
    const decoder = new TextDecoder("utf-8");
    const HEX_ESCAPE = /^\\([0-9a-fA-F]{2})/;
    let result = "";
    let bytes: number[] = [];
    const flushBytes = () => {
        if (bytes.length > 0) {
            result += decoder.decode(new Uint8Array(bytes));
            bytes = [];
        }
    };
    let i = 0;
    while (i < source.length) {
        const match = HEX_ESCAPE.exec(source.slice(i));
        if (match) {
            bytes.push(Number.parseInt(match[1], 16));
            i += 3;
            continue;
        }
        flushBytes();
        result += source[i];
        i += 1;
    }
    flushBytes();
    return result;
};

/** Split a raw (still escaped) filter value on unescaped `*` wildcards. */
const splitFilterValueWildcards = (raw: string): string[] => {
    const segments: string[] = [];
    let current = "";
    let escaped = false;
    for (const char of raw) {
        if (escaped) {
            current += char;
            escaped = false;
            continue;
        }
        if (char === "\\") {
            current += char;
            escaped = true;
            continue;
        }
        if (char === "*") {
            segments.push(current);
            current = "";
            continue;
        }
        current += char;
    }
    segments.push(current);
    return segments;
};

const CLAUSE_ITEM_RE = /^([^=]+?)(>=|<=|~=|=)([\s\S]*)$/;

/**
 * Classify an item body `attr<op>value` into a builder clause. Returns null for
 * shapes the visual builder cannot represent (extensible matches, multi-star
 * substring patterns, empty values) so callers keep source mode.
 */
export const parseClauseItem = (body: string): BuilderClause | null => {
    const match = CLAUSE_ITEM_RE.exec(body);
    if (!match) return null;
    const attribute = trimToString(match[1]);
    const operator = match[2];
    const rawValue = match[3] ?? "";
    if (!isValidLDAPAttributeDescription(attribute)) return null;

    if (operator === "=") {
        if (rawValue === "*") return createBuilderClause({ attribute, op: "present" });
        const segments = splitFilterValueWildcards(rawValue);
        // Middle empty segments (e.g. `a**b`) or multi-star patterns are not
        // representable by a single operator — fail loudly (stay in source mode).
        if (segments.slice(1, -1).some((segment) => segment === "")) return null;
        const leading = segments[0] === "";
        const trailing = segments[segments.length - 1] === "";
        const core = segments.slice(leading ? 1 : 0, trailing ? segments.length - 1 : segments.length);
        if (core.length !== 1) return null;
        if (leading && trailing) return createBuilderClause({ attribute, op: "contains", value: unescapeLdapFilterValue(core[0]) });
        if (leading) return createBuilderClause({ attribute, op: "endsWith", value: unescapeLdapFilterValue(core[0]) });
        if (trailing) return createBuilderClause({ attribute, op: "startsWith", value: unescapeLdapFilterValue(core[0]) });
        return createBuilderClause({ attribute, op: "equals", value: unescapeLdapFilterValue(core[0]) });
    }

    const op = operator === ">=" ? "gte" : operator === "<=" ? "lte" : "approx";
    if (!rawValue) return null;
    return createBuilderClause({ attribute, op, value: unescapeLdapFilterValue(rawValue) });
};

interface ParseCursor {
    text: string;
    position: number;
}

const parseFilterItem = (cursor: ParseCursor): BuilderNode | null => {
    const text = cursor.text;
    if (text[cursor.position] !== "(") return null;
    cursor.position += 1;
    const operator = text[cursor.position];

    if (operator === "&" || operator === "|") {
        cursor.position += 1;
        const children: BuilderNode[] = [];
        while (text[cursor.position] === "(") {
            const child = parseFilterItem(cursor);
            if (!child) return null;
            children.push(child);
        }
        if (children.length === 0) return null;
        if (text[cursor.position] !== ")") return null;
        cursor.position += 1;
        const group = createBuilderGroup({ join: operator === "&" ? "and" : "or", children });
        return group;
    }

    if (operator === "!") {
        cursor.position += 1;
        const child = parseFilterItem(cursor);
        if (!child) return null;
        if (cursor.text[cursor.position] !== ")") return null;
        cursor.position += 1;
        // Negated equality folds into the first-class ≠ operator so the UI
        // shows it directly; other shapes keep the negate flag (round-trip
        // is preserved by buildNodeFilter either way).
        if (child.kind === "clause" && child.op === "equals" && !child.negate) {
            return { ...child, op: "notEquals", negate: undefined };
        }
        return { ...child, negate: true };
    }

    const close = text.indexOf(")", cursor.position);
    if (close < 0) return null;
    const body = text.slice(cursor.position, close);
    if (body.includes("(")) return null;
    cursor.position = close + 1;
    return parseClauseItem(body);
};

/**
 * Best-effort RFC 4515 filter string → builder tree. Returns null when the
 * string is invalid or uses shapes the builder cannot display (the source-mode
 * editor stays authoritative for those).
 */
export const parseFilterStructure = (filter: string): BuilderNode | null => {
    const text = String(filter ?? "").trim();
    if (!text) return null;
    const cursor: ParseCursor = { text, position: 0 };
    const node = parseFilterItem(cursor);
    if (!node || cursor.position !== text.length) return null;
    return node;
};

/** Ensure a parsed node can be used as the builder root (clauses get wrapped). */
export const toBuilderRoot = (node: BuilderNode | null): BuilderGroup | null => {
    if (!node) return null;
    return node.kind === "group" ? node : createBuilderGroup({ join: "and", children: [node] });
};

/**
 * Structural sanity check for persisted conditions (e.g. search presets) with
 * fresh id regeneration, so restored trees never collide with live ids.
 */
export const reviveBuilderNode = (value: unknown): BuilderNode | null => {
    const source = value as Partial<BuilderNode> | null;
    if (!source || typeof source !== "object") return null;
    if (source.kind === "clause") {
        const op = String(source.op ?? "");
        if (!(["equals", "notEquals", "contains", "startsWith", "endsWith", "present", "gte", "lte", "approx"] as const).includes(op as BuilderOp)) return null;
        return {
            kind: "clause",
            id: nextBuilderNodeId(),
            attribute: String(source.attribute ?? ""),
            op: op as BuilderOp,
            value: String(source.value ?? ""),
            ...(source.negate ? { negate: true } : {}),
        };
    }
    if (source.kind !== "group" || !Array.isArray(source.children)) return null;
    const children = source.children.map((child) => reviveBuilderNode(child));
    if (children.some((child) => !child)) return null;
    return {
        kind: "group",
        id: nextBuilderNodeId(),
        join: source.join === "or" ? "or" : "and",
        children: children as BuilderNode[],
        ...(source.negate ? { negate: true } : {}),
    };
};

/**
 * 树过滤覆盖的命名属性（纯标准 schema：core/cosine/inetorgperson，
 * OpenLDAP/AD 都接受；真机已验证 dc/ou/cn/uid/o 等均可用于过滤器）。
 * objectClass 只在前缀语法里可用（普通关键字 OR 上它会让 "person" 这类
 * 词命中全量条目，语义过于发散）。
 */
export const TREE_FILTER_ATTRIBUTES = ['cn', 'ou', 'dc', 'uid', 'o', 'sn', 'givenName', 'name', 'displayName', 'mail', 'sAMAccountName'] as const;

/**
 * 树过滤前缀语法：`ou=peo` / `cn: ali` 把匹配限定到单个属性（值含子串匹配）；
 * `ou=`（空值）= 存在性过滤（列出全部 OU）；`objectClass=person` 走精确等值。
 * 属性名大小写不敏感；前缀属性名不在白名单时返回 null（整串按普通关键字处理）。
 */
export const parseTreeKeywordPrefix = (keyword: string): { attribute: string; value: string } | null => {
    const match = String(keyword ?? '').trim().match(/^([A-Za-z][A-Za-z0-9-]*)\s*[=:]\s*(.*)$/);
    if (!match) return null;
    const attribute = match[1].toLowerCase();
    if (attribute !== 'objectclass' && !(TREE_FILTER_ATTRIBUTES as readonly string[]).includes(attribute)) return null;
    return { attribute, value: match[2].trim() };
};

/**
 * Build the DN-tree keyword filter used for remote subtree filtering
 * (tiny-rdm LdapConsolePage `buildTreeKeywordFilter` semantics, extended):
 * - `attr=value` / `attr:value` 前缀 → 单属性限定（含 objectClass 等值、
 *   空值存在性）；属性名未识别则整串回落普通关键字。
 * - 普通关键字 → 命名属性集（含 dc/o/sn，补齐 tiny-rdm 原版缺口）的 OR
 *   子串匹配。
 */
export const buildTreeKeywordFilter = (keyword: string): string => {
    const text = String(keyword ?? '').trim();
    const prefixed = parseTreeKeywordPrefix(text);
    if (prefixed) {
        if (prefixed.attribute === 'objectclass') {
            return prefixed.value ? buildEqualityFilter('objectClass', prefixed.value) : '(objectClass=*)';
        }
        if (prefixed.value === '') return buildPresenceFilter(prefixed.attribute);
        return buildSubstringFilter(prefixed.attribute, prefixed.value, 'contains') || '(objectClass=*)';
    }
    const filters = TREE_FILTER_ATTRIBUTES.map((attribute) => buildSubstringFilter(attribute, text, 'contains')).filter(Boolean);
    return combineFilters(filters, 'or') || '(objectClass=*)';
};

/**
 * Lightweight RFC 4515 filter syntax check (client-side pre-validation only;
 * the sidecar performs the authoritative parse). Validates parenthesised
 * structure, filter operators and item shape.
 */
export const validateLDAPFilter = (filter: string): boolean => {
    const text = String(filter ?? '').trim();
    if (!text) return false;

    let position = 0;
    const peek = () => text[position];

    const parseItem = (): boolean => {
        // item := "(" ("&"/"|" filterlist / "!" filter / attr filtertype) ")"
        if (peek() !== '(') return false;
        position += 1;
        const operator = peek();
        if (operator === '&' || operator === '|') {
            position += 1;
            let matched = 0;
            while (peek() === '(') {
                if (!parseItem()) return false;
                matched += 1;
            }
            if (matched < 1) return false;
        } else if (operator === '!') {
            position += 1;
            if (!parseItem()) return false;
        } else {
            // simple item: attr filtertype value — raw parens inside the value
            // are invalid (RFC 4515 requires \28/\29 escaping).
            let sawValue = false;
            while (position < text.length) {
                const char = peek();
                if (char === ')') break;
                if (char === '(') return false;
                sawValue = true;
                position += 1;
            }
            if (!sawValue) return false;
        }
        if (peek() !== ')') return false;
        position += 1;
        return true;
    };

    if (!parseItem()) return false;
    return position === text.length && text.startsWith('(');
};
