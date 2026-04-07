<!--
Thanks for contributing to Marauder!

Use this checklist as a guide. Delete sections that don't apply, but
please leave the heading so reviewers can scan quickly.
-->

## Summary

<!-- 1-3 sentences. What does this PR do, and why? -->

## Type of change

<!-- Tick the box that applies. -->

- [ ] Bug fix
- [ ] New feature (tracker / client / notifier plugin, UI page, etc.)
- [ ] Refactor / cleanup
- [ ] Documentation only
- [ ] CI / build / tooling
- [ ] Dependency update
- [ ] Other (explain below)

## Plugin work (delete if not applicable)

- [ ] If you added or changed a tracker plugin, you noted whether you
      have validated it against a **real live instance** or only
      against fixture HTML.
- [ ] If you added a new tracker, it implements `Tracker` and any
      relevant capability interfaces (`WithCredentials`, `WithQuality`,
      `WithCloudflare`).
- [ ] You wired the plugin into `cmd/server/main.go` via a blank
      import.

## Test plan

<!-- Spell out exactly what you ran. Mention the commands so a
reviewer can reproduce. -->

- [ ] `go build ./...` and `go vet ./...` clean
- [ ] `go test ./...` passes
- [ ] If you touched the frontend: `npm run build` and `npm run typecheck` clean
- [ ] If applicable: full E2E walkthrough from `docs/test-e2e-magnet.md`

## Documentation

- [ ] CHANGELOG.md updated under `[Unreleased]`
- [ ] If you closed a roadmap item, you ticked it in `docs/ROADMAP.md`
- [ ] If you added a new public type/function, it has a doc comment
      that explains *why* it exists, not just *what* it is

## Reviewer notes

<!-- Anything you want a reviewer to focus on, watch out for, or
push back on. -->
