# Maintainer policy

See [MAINTAINERS.zh-CN.md](MAINTAINERS.zh-CN.md) for the Chinese version.

## Ownership model

- The four plugin directories are independent release units.
- `shared/frontend/` is the single source of truth for shared host adapters.
- `host/` is an upstream-owned submodule and is read-only by default.
- Marketplace listing syncs automatically from each plugin GitHub Release;
  signing stays outside plugin source maintainers' control.

## Required maintenance work

### Every code change

- Update the relevant protocol and implementation documentation.
- Keep the seven supported locales complete for user-visible text.
- Run the smallest relevant unit, frontend, and smoke checks.
- Review permissions, secret bindings, audit data, and logs.
- Run the repository hygiene check before merging.

### Every host update

Run `scripts/host-sync.sh contract` after the host submodule or released DBX app
changes. Review binary events, filesystem APIs, appearance/locale support,
workbench state, manifest lifecycle, and mocks. Prefer optional degradation over
hard dependence on a newer host API.

### Every release

- Use a new immutable SemVer; never rebuild an existing release in place.
- Use the target plugin tag format: `<plugin>-v<version>`.
- Produce unsigned candidates for all five native targets.
- Verify candidate metadata, package checksums, permissions, and manifest identity.
- Publish the GitHub Release; the DBX Store syncs it automatically (no manual
  review submission).
- Record release notes, known limitations, and rollback information.

## Dependency and incident response

Dependabot updates are reviewed by the affected plugin owner. Security updates
take priority over feature work and must include a regression test or a written
reason why one is not practical.

For an exposed credential, revoke or rotate it first, preserve only redacted
evidence, and follow [SECURITY.md](SECURITY.md). Do not rewrite history as a
substitute for rotating a secret.
