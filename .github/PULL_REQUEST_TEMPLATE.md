## Summary

Describe the affected plugin(s) and the user-visible or protocol-level change.

## Security and compatibility

- [ ] No credentials, private keys, customer data, or local runtime files are included.
- [ ] Host API changes are optional and have a documented fallback.
- [ ] Shared host compatibility code is in `shared/frontend/` where applicable.
- [ ] Protocol and permission changes are documented.

## Verification

- [ ] Target plugin unit tests pass.
- [ ] Frontend typecheck/test/build pass.
- [ ] Smoke tests pass or documented cases are explicitly `SKIP`.
- [ ] `python3 shared/release/check_repo_hygiene.py` passes.
- [ ] Seven-language copy is complete where UI or manifest text changed.
