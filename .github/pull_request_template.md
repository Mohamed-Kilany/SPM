# Pull request

<!--
Keep pull requests small and single-purpose. Production is advanced only by
merging a pull request whose required checks are green. Never include secrets,
kubeconfigs, private keys, database dumps or generated build output.
-->

## Summary

<!-- What changed and why. Link the task ID(s) from TASKS.md. -->

- Task(s):
- Change type: `feature` / `fix` / `change` / `docs` / `chore`
- Branch:

## Risk and blast radius

<!-- What can break? Is a migration, feature flag, or coordinated deploy needed? -->

- Risk level: `low` / `medium` / `high`
- Affected surfaces:
- Rollback plan:

## Verification

<!-- Paste the commands you ran and the result. CI is the final authority. -->

- [ ] Go: `gofmt`, `go vet`, `go test -race ./...`, build
- [ ] UI: `npm run typecheck`, `npm test`, `npm run build`
- [ ] AI: `python -m pytest -q`
- [ ] Compose: production and test stack config validation
- [ ] Images: API, UI and AI Docker images build
- [ ] Manual verification (describe):

## Data, API and operations

- [ ] No database migration
- [ ] Migration is expand/contract safe and reversible
- [ ] OpenAPI and typed UI client updated
- [ ] Audit event emitted for every mutation
- [ ] Telemetry added or updated (metrics, traces, logs)
- [ ] Runbook or README updated
- [ ] No new external runtime dependency

## Security and privacy

- [ ] Authorization checked server-side (not only hidden in the UI)
- [ ] No secret, token, key, kubeconfig or `.env` content in the diff
- [ ] User input validated and output encoded
- [ ] No new outbound network call
- [ ] Dependency changes reviewed by the dependency-review gate

## UI review

<!-- Required for user-visible changes. -->

- [ ] Keyboard-only path works
- [ ] Screen-reader name/role/state is correct
- [ ] Focus is visible and managed
- [ ] Contrast meets WCAG 2.2 AA
- [ ] 1280 px, 768 px and 375 px layouts checked
- [ ] Loading, empty, error and partial states checked
- [ ] Screenshots or recording attached

## Reviewer notes

<!-- Anything the reviewer should focus on, known limitations, follow-ups. -->
