# Contributing to DBX plugins

See [CONTRIBUTING.zh-CN.md](CONTRIBUTING.zh-CN.md) for the Chinese version.

## Before changing code

1. Read the target plugin README and the relevant public maintenance policy.
2. Confirm that the change belongs to one of the four existing plugins.
3. Check `git status` and preserve unrelated local changes.

## Development rules

- Keep plugin protocols under `<domain>/<action>` with camelCase fields.
- Put shared host compatibility code in `shared/frontend/`; do not copy it into
  each plugin.
- Store credentials through host secret bindings. Never log or persist them.
- Update protocol documentation, progress tracking, and all seven locales with
  the code change.
- Keep tests deterministic and offline unless a test is explicitly marked as a
  container or live smoke test.
- Treat unavailable integration environments as documented `SKIP`.

## Verification

Run the target plugin's `scripts/test.sh`. Before a pull request, also run:

```bash
shared/test-all.sh --quick
python3 shared/release/check_repo_hygiene.py
```

## Pull requests

Explain the affected plugin, protocol changes, security implications, and
verification results. Do not include `.dbxp` artifacts, build directories,
screenshots, local app data, credentials, or private keys.

Release changes must follow
[the release checklist](shared/release/RELEASE_CHECKLIST.zh-CN.md).
