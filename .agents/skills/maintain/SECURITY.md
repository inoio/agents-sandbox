# Security

Assess open security signals and route them. This area **assesses and
recommends**; it does not change repository settings. Discovery is read-only;
any comment or routing is an approved [batch-plan](SKILL.md#batch-plan-protocol)
row.

## Repo facts these flows assume

- The repo is **public**. Dependabot **vulnerability alerts** are available.
- As of 2026-10-07, **secret scanning**, **push protection**, and **dependabot
  security updates** are **enabled**; **code scanning** default setup is
  configured but had produced no analysis yet. `secret_scanning_non_provider_patterns`
  and `secret_scanning_validity_checks` remain **disabled**.
- Re-check the enabled features and alert count rather than assuming; the state
  changes.

## Discovery

```sh
gh api repos/{owner}/{repo}/dependabot/alerts?state=open \
  --jq '[.[] | {number, state, severity: .security_advisory.severity,
               summary: .security_advisory.summary,
               package: .dependency.package.name,
               manifest: .dependency.manifest_path,
               patched: .security_vulnerability.first_patched_version.identifier}]'

gh pr list --state open --author app/dependabot \
  --json number,title,labels,headRefName
```

## Assess

For each open alert, determine:

- severity and the vulnerable version range,
- the first patched version,
- the manifest, and
- whether an open dependabot PR already bumps to a patched version.

## Route into the plan

- **Alert covered by an open, safe dependabot bump** → the row is handled by the
  [dependabot merge](DEPENDABOT.md); annotate it
  `merge #<n>: fixes <alert summary>`.
- **Alert with no PR** → propose a `flag:` / manual fix row. Never auto-bump a
  major; if the only patch is a major bump, it is a human decision.
- **Disabled security features** (security updates, secret scanning, code
  scanning) → a recommendation row naming the exact action a human must take.
  The skill cannot perform it.

## Boundaries

- Never enable or disable a repository setting.
- Never merge outside the [dependabot policy](DEPENDABOT.md#policy).
- No write without an approved plan row.
