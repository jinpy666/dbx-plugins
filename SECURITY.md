# Security policy

See [SECURITY.zh-CN.md](SECURITY.zh-CN.md) for the Chinese version.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use a private GitHub
Security Advisory for this repository:

<https://github.com/jinpy666/dbx-plugins/security/advisories/new>

Include the affected plugin and version, impact, reproduction steps, logs with
credentials removed, and a suggested severity. Do not attach private keys,
passwords, production data, or complete connection exports.

If private advisories are unavailable, contact the repository maintainers through
the GitHub repository profile and request a private security channel.

## Scope

Reports may cover plugin code, sidecars, package/build workflows, secret handling,
host bridge usage, and release integrity. DBX host vulnerabilities should also be
reported upstream when the root cause is outside this repository.

## Credentials and test data

Use disposable local or container credentials only. Never commit real credentials,
private keys, tokens, customer data, or signed release keys. Rotate any secret
that was accidentally exposed, even if the commit is later deleted.
