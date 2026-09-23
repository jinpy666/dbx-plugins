# Public repository release checklist

Use this checklist before making the DBX plugin repository public. Never put
real tokens, private keys, customer data, internal addresses, local filesystem
paths, or deployment details in this document.

## Code and history

- [ ] `python3 shared/release/check_repo_hygiene.py` passes.
- [ ] `git ls-files` contains no environment files, keys, certificates,
      databases, connection exports, runtime state, or build artifacts.
- [ ] Commit author and committer emails are appropriate for public history.
- [ ] Unrelated research, account-management, deployment, and internal tooling
      have been removed.
- [ ] The `host/` submodule points to a public, compatible upstream revision.

## License and identity

- [ ] Copyright holder is confirmed by the maintainers.
- [ ] The software license is confirmed and a root `LICENSE` file is present.
- [ ] Publisher, source, homepage, and license metadata are accurate.
- [ ] Third-party licenses and NOTICE obligations are reviewed.

## Open-source governance

- [ ] Bilingual README, contribution, conduct, security, and support documents
      are published.
- [ ] CODEOWNERS, issue templates, pull request templates, and dependency updates
      are enabled.
- [ ] Repository hygiene and manifest CI pass.
- [ ] Private vulnerability reporting is tested.

## Release and supply chain

Multi-platform candidates are built and verified by [Multi-platform CI and release output](CI_MULTI_PLATFORM.en.md);
this section covers the remaining manual release checks before the DBX Store
auto-sync.

- [ ] Every plugin uses an immutable `<plugin>-v<version>` tag.
- [ ] Unsigned candidates are reproducible for all five native targets.
- [ ] Candidate hash, size, plugin ID, version, and target are validated.
- [ ] Candidates contain no `signature.json` or official signing key.
- [ ] The GitHub Release is published and the DBX Store auto-sync (signing and
      catalog) completed.

## Final public-release actions

- [ ] The quick start works from a clean clone.
- [ ] At least one plugin installs successfully into clean app data.
- [ ] Public URLs, repository permissions, branch protection, and Actions
      permissions have been reviewed.
- [ ] Current versions, known limitations, and rollback versions are recorded.
