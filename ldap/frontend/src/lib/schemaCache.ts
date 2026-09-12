/**
 * Schema metadata cache + RFC 4512 description parsing.
 *
 * Ported from tiny-rdm `frontend/src/modules/tools/ldap/composables/
 * useLdapSchemaCache.js`: a hoisted per-connection cache with TTL so schema
 * lookups (attribute autocomplete, objectClass-driven hints) survive
 * component remounts without refetching on every keystroke. The loader feeds
 * from the sidecar `ldap/schema` method (server-side cache on the Go side;
 * pass `refresh: true` to force a reload).
 */

import { ref } from 'vue';

const DEFAULT_TTL_MS = 30 * 60 * 1000;

export interface ObjectClassAttributes {
    must: string[];
    may: string[];
}

export interface SchemaMetadata {
    attributeNames: string[];
    objectClassAttributes: Record<string, ObjectClassAttributes>;
    rawAttributeTypes?: string[];
    rawObjectClasses?: string[];
}

interface CacheEntry {
    payload: SchemaMetadata;
    fetchedAt: number;
}

// -- RFC 4512 description parsing (pure helpers) -----------------------------

/** Split a parenthesised definition body into top-level tokens. */
function tokenizeSchemaBody(body: string): string[] {
    const tokens: string[] = [];
    let depth = 0;
    let current = '';
    let quoted = false;
    for (const char of body) {
        if (quoted) {
            if (char === "'") quoted = false;
            else current += char;
            continue;
        }
        if (char === "'") {
            quoted = true;
            current += char;
            continue;
        }
        if (char === '(') {
            depth += 1;
            if (depth === 1) continue;
        } else if (char === ')') {
            depth -= 1;
            if (depth === 0) continue;
        }
        if (depth === 0 && /\s/.test(char)) {
            if (current) tokens.push(current);
            current = '';
            continue;
        }
        current += char;
    }
    if (current) tokens.push(current);
    return tokens;
}

/** Extract the quoted / bare token list that follows `keyword` (NAME, SUP…). */
function extractKeywordValues(definition: string, keyword: string): string[] {
    const match = new RegExp(`${keyword}\\s+(?:(?:\\()\\s*([^)]*)\\)|'([^']*)'|([^\\s)]+))`, 'iu').exec(definition);
    if (!match) return [];
    if (match[1] != null) {
        return [...match[1].matchAll(/'([^']*)'|([^\s$()]+)/gu)].map((m) => (m[1] ?? m[2] ?? '').trim()).filter(Boolean);
    }
    const value = match[2] ?? match[3] ?? '';
    return value.trim() ? [value.trim()] : [];
}

function parseAttributeType(definition: string): string[] {
    return extractKeywordValues(definition, 'NAME');
}

interface ParsedObjectClass {
    names: string[];
    must: string[];
    may: string[];
}

function parseObjectClass(definition: string): ParsedObjectClass {
    const names = extractKeywordValues(definition, 'NAME');
    const must = extractKeywordValues(definition, 'MUST');
    const may = extractKeywordValues(definition, 'MAY');
    return { names, must, may };
}

/**
 * Derive the frontend schema metadata from the sidecar `ldap/schema` return
 * (`attributeTypes` / `objectClasses` are raw RFC 4512 description strings).
 */
export function deriveSchemaMetadata(attributeTypes: unknown, objectClasses: unknown): SchemaMetadata {
    const attributeNames: string[] = [];
    const seenAttributes = new Set<string>();
    for (const definition of Array.isArray(attributeTypes) ? attributeTypes : []) {
        for (const name of parseAttributeType(String(definition ?? ''))) {
            const key = name.toLowerCase();
            if (!key || seenAttributes.has(key)) continue;
            seenAttributes.add(key);
            attributeNames.push(name);
        }
    }

    const objectClassAttributes: Record<string, ObjectClassAttributes> = {};
    for (const definition of Array.isArray(objectClasses) ? objectClasses : []) {
        const parsed = parseObjectClass(String(definition ?? ''));
        if (parsed.names.length === 0) continue;
        const entry: ObjectClassAttributes = { must: parsed.must, may: parsed.may };
        for (const name of parsed.names) {
            objectClassAttributes[name] = entry;
        }
    }

    return {
        attributeNames,
        objectClassAttributes,
        rawAttributeTypes: Array.isArray(attributeTypes) ? attributeTypes.map(String) : [],
        rawObjectClasses: Array.isArray(objectClasses) ? objectClasses.map(String) : [],
    };
}

export interface SchemaCacheOptions {
    ttlMs?: number;
    loader?: (connectionId: string) => Promise<Partial<SchemaMetadata>>;
}

export function useLdapSchemaCache(options?: SchemaCacheOptions) {
    const ttlMs = Math.max(0, Number(options?.ttlMs) || DEFAULT_TTL_MS);
    const loader = typeof options?.loader === 'function' ? options!.loader : null;

    const loading = ref(false);
    const error = ref<unknown>(null);
    const attributeNames = ref<string[]>([]);
    const objectClassAttributes = ref<Record<string, ObjectClassAttributes>>({});

    const cache = new Map<string, CacheEntry>();
    const inFlight = new Map<string, Promise<SchemaMetadata>>();
    let currentConnectionId = '';

    const applyEntry = (entry: CacheEntry) => {
        attributeNames.value = entry?.payload?.attributeNames || [];
        objectClassAttributes.value = entry?.payload?.objectClassAttributes || {};
    };

    const ensureLoaded = async (connectionId: string): Promise<SchemaMetadata | null> => {
        const key = String(connectionId ?? '');
        if (!key) return null;
        if (!loader) {
            const empty: CacheEntry = { payload: { attributeNames: [], objectClassAttributes: {} }, fetchedAt: Date.now() };
            applyEntry(empty);
            return empty.payload;
        }
        currentConnectionId = key;

        const cached = cache.get(key);
        if (cached && Date.now() - cached.fetchedAt < ttlMs) {
            applyEntry(cached);
            return cached.payload;
        }

        const existing = inFlight.get(key);
        if (existing) return existing;

        const pending = (async () => {
            loading.value = true;
            error.value = null;
            try {
                const payload = (await loader(key)) || {};
                const entry: CacheEntry = {
                    payload: {
                        attributeNames: payload.attributeNames || [],
                        objectClassAttributes: payload.objectClassAttributes || {},
                        rawObjectClasses: payload.rawObjectClasses,
                    },
                    fetchedAt: Date.now(),
                };
                cache.set(key, entry);
                if (currentConnectionId === key) applyEntry(entry);
                return entry.payload;
            } catch (err) {
                error.value = err;
                throw err;
            } finally {
                inFlight.delete(key);
                if (inFlight.size === 0) loading.value = false;
            }
        })();
        inFlight.set(key, pending);
        return pending;
    };

    const invalidate = (connectionId?: string) => {
        if (connectionId == null) {
            cache.clear();
            return;
        }
        cache.delete(String(connectionId));
    };

    const clear = () => {
        cache.clear();
        inFlight.clear();
        loading.value = false;
        error.value = null;
        attributeNames.value = [];
        objectClassAttributes.value = {};
        currentConnectionId = '';
    };

    return {
        loading,
        error,
        attributeNames,
        objectClassAttributes,
        ensureLoaded,
        invalidate,
        clear,
    };
}
