/**
 * LDAP entry exporters (CSV / JSON / LDIF passthrough). Ported from tiny-rdm
 * `frontend/src/modules/tools/ldap/utils/ldapExporter.js`. Pure functions
 * only. Entry shape: `{ dn?: string, DN?: string, attributes?: Record<string,
 * unknown>, attrs?: …, values?: … }`. Multivalued attributes may arrive as
 * arrays or scalars; callers should not have to normalise them.
 */

import { serializeEntriesToLdif, type LdifEntry } from "./ldif";

const CSV_DEFAULTS = Object.freeze({
    delimiter: ',',
    includeHeader: true,
    multivaluedSeparator: '|',
});

const QUOTE_TRIGGER_PATTERN = /[",\r\n]/u;

type AttributeMap = Record<string, unknown>;

export interface LdapExportEntry {
    dn?: string;
    DN?: string;
    attributes?: AttributeMap;
    attrs?: AttributeMap;
    values?: AttributeMap;
}

export interface CsvSerializeOptions {
    columns?: string[];
    delimiter?: string;
    includeHeader?: boolean;
    multivaluedSeparator?: string;
}

const getAttributeMap = (entry: LdapExportEntry): AttributeMap =>
    (entry && (entry.attributes || entry.attrs || entry.values)) || {};

const getDn = (entry: LdapExportEntry): string => String(entry?.dn ?? entry?.DN ?? '');

const normaliseToArray = (raw: unknown): string[] => {
    if (raw == null) return [];
    if (Array.isArray(raw)) return raw.map((item) => String(item ?? ''));
    return [String(raw)];
};

/**
 * Safe accessor for an entry attribute. The lookup is case-insensitive so
 * callers can pass either schema-form ("uid") or user-typed ("UID") names.
 */
export const getEntryAttributeValues = (entry: LdapExportEntry, attribute: string): string[] => {
    const attrs = getAttributeMap(entry);
    if (!attribute) return [];
    if (attribute in attrs) return normaliseToArray(attrs[attribute]);
    const lower = String(attribute).toLowerCase();
    for (const key of Object.keys(attrs)) {
        if (key.toLowerCase() === lower) return normaliseToArray(attrs[key]);
    }
    return [];
};

/** Collect every distinct attribute name across the entry list, first-seen order. */
export const extractEntryAttributeNames = (entries: LdapExportEntry[]): string[] => {
    const seen = new Set<string>();
    const ordered: string[] = [];
    for (const entry of entries || []) {
        const attrs = getAttributeMap(entry);
        for (const key of Object.keys(attrs)) {
            if (!seen.has(key)) {
                seen.add(key);
                ordered.push(key);
            }
        }
    }
    return ordered;
};

const escapeCsvCell = (value: unknown): string => {
    const text = String(value ?? '');
    if (!QUOTE_TRIGGER_PATTERN.test(text)) return text;
    return `"${text.replace(/"/gu, '""')}"`;
};

/**
 * Serialize entries to CSV. The first column is always `dn`; additional
 * columns default to the union of all attribute names but can be pinned via
 * `options.columns`.
 */
export const serializeEntriesToCsv = (entries: LdapExportEntry[], options?: CsvSerializeOptions): string => {
    const config = { ...CSV_DEFAULTS, ...(options || {}) };
    const dataAttributes =
        Array.isArray(options?.columns) && options.columns.length > 0
            ? options.columns.filter((name) => name && name !== 'dn')
            : extractEntryAttributeNames(entries);
    const headers = ['dn', ...dataAttributes];

    const formatCell = (raw: unknown): string => normaliseToArray(raw).join(config.multivaluedSeparator);

    const rows: string[] = [];
    if (config.includeHeader) {
        rows.push(headers.map(escapeCsvCell).join(config.delimiter));
    }
    for (const entry of entries || []) {
        const cells = [
            getDn(entry),
            // Case-insensitive lookup so pinned columns match either schema-form
            // ("cn") or user-typed ("CN") names, mirroring the header union.
            ...dataAttributes.map((name) => formatCell(getEntryAttributeValues(entry, name))),
        ];
        rows.push(cells.map(escapeCsvCell).join(config.delimiter));
    }
    return rows.join('\r\n');
};

export const serializeEntriesToJson = (entries: LdapExportEntry[], options?: { pretty?: boolean }): string => {
    const list = Array.isArray(entries) ? entries : [];
    const payload = list.map((entry) => ({
        dn: getDn(entry),
        attributes: { ...getAttributeMap(entry) },
    }));
    const pretty = options?.pretty !== false;
    return JSON.stringify(payload, null, pretty ? 2 : 0);
};

export const serializeEntriesToLdifText = (entries: LdapExportEntry[], options?: { lineWidth?: number; includeVersion?: boolean }): string =>
    serializeEntriesToLdif(entries as LdifEntry[], options);
