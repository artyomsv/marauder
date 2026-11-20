# pgx held below 5.12 until pgxmock v6 is stable

| Field | Value |
|-------|-------|
| Criticality | Medium |
| Complexity | Small |
| Location | `.github/dependabot.yml` (gomod `ignore`), `backend/go.mod` |
| Found during | Dependabot PR #246, CI on 2026-11-16 |
| Date | 2026-11-20 |

## Issue

pgx 5.12.0 added `Tx()` to the `pgx.BatchResults` interface. pgxmock
v5.2.0 does not implement it, so every repo test fails to compile:

```
pgxmock/v5@v5.2.0/batch.go:76:16: *batchResults does not implement pgx.BatchResults (missing method Tx)
```

The fix (pgxmock #343) ships only in pgxmock v6, which is still in beta
(`v6.0.0-beta2`, 2026-11-04). Dependabot is told to ignore pgx `>=5.12.0`,
so pgx stays on 5.11.x.

goose v3.30.0+ requires pgx 5.12, and Go picks the highest version any
dependency asks for, so goose is held below 3.30 as well (PR #255).

## Risks

A Dependabot `ignore` also applies to security updates. A pgx advisory
fixed only in 5.12 or later raises an alert but no pull request. Check
pgx advisories by hand while the hold is in place.

## Suggested Solutions

1. When pgxmock v6 is stable, change the import path from
   `github.com/pashagolub/pgxmock/v5` to `/v6` in the repo tests, bump pgx,
   and remove both `ignore` entries (pgx and goose) and this file.
