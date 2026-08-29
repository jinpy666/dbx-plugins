/**
 * Base-DN inference helpers. Ported from tiny-rdm
 * `frontend/src/modules/tools/ldap/utils/baseDn.js` — derive `dc=…,dc=…`
 * candidates from a profile's logical host / URL.
 */

interface LdapProfileLike {
    target?: Record<string, unknown>;
    kerberos?: Record<string, unknown>;
    saslHost?: unknown;
    url?: unknown;
    host?: unknown;
}

const normalizeHostCandidate = (candidate: unknown): string => {
    const source = String(candidate || '').trim();
    if (!source) return '';
    try {
        const parsed = new URL(source.includes('://') ? source : `ldap://${source}`);
        if (parsed.hostname) return parsed.hostname;
    } catch {
        const host = source
            .replace(/^[a-z][a-z0-9+.-]*:\/\//iu, '')
            .split('/')[0]
            .split(':')[0]
            .trim();
        if (host) return host;
    }
    return '';
}

export const extractLdapProfileHostCandidates = (profile: LdapProfileLike = {}): string[] => {
    const target = (profile?.target || {}) as Record<string, unknown>;
    const kerberos = (profile?.kerberos || {}) as Record<string, unknown>;
    const candidates = [
        target.logicalHost,
        target.tlsServerName,
        target.kerberosSpnHost,
        target.kerberosSPNHost,
        target.servicePrincipalName,
        kerberos.spnHost,
        kerberos.serviceHost,
        kerberos.servicePrincipalName,
        profile?.saslHost,
        profile?.url,
        profile?.host,
    ]
    const seen = new Set<string>()
    const hosts: string[] = []
    for (const candidate of candidates) {
        const host = normalizeHostCandidate(candidate)
        const key = host.toLowerCase()
        if (!host || seen.has(key)) continue
        seen.add(key)
        hosts.push(host)
    }
    return hosts
}

export const extractLdapProfileHost = (profile: LdapProfileLike = {}): string =>
    extractLdapProfileHostCandidates(profile)[0] || ''

export const inferBaseDnFromLdapHost = (host: unknown): string => {
    const labels = String(host || '')
        .trim()
        .replace(/\.$/u, '')
        .toLowerCase()
        .split('.')
        .map((part) => part.trim())
        .filter((part) => /^[a-z0-9-]+$/u.test(part))
    if (labels.length < 2) return ''
    if (labels.every((part) => /^\d+$/u.test(part))) return ''
    const first = labels[0]
    if (
        labels.length > 2 &&
        (/^(?:ldap|ldaps|ad|ads|gc|directory|auth|sso)(?:[-\d].*)?$/iu.test(first) ||
            /^dc[a-z0-9-]*\d*$/iu.test(first))
    ) {
        labels.shift()
    }
    if (labels.length < 2) return ''
    return labels.map((label) => `dc=${label}`).join(',')
}

export const inferBaseDnCandidatesFromProfile = (profile: LdapProfileLike = {}): string[] => {
    const seen = new Set<string>()
    return extractLdapProfileHostCandidates(profile).reduce<string[]>((items, host) => {
        const baseDn = inferBaseDnFromLdapHost(host)
        const key = baseDn.toLowerCase()
        if (!baseDn || seen.has(key)) return items
        seen.add(key)
        items.push(baseDn)
        return items
    }, [])
}

export const inferBaseDnFromProfile = (profile: LdapProfileLike = {}): string =>
    inferBaseDnCandidatesFromProfile(profile)[0] || ''
