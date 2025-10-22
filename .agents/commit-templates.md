# Commit Message Templates

## Rules

- Imperative mood (Add, Fix, Update, not Added/Fixed/Updated)
- Subject ≤50 chars, body lines ≤72 chars
- Always sign-off: `git commit -s`

## Version Bumps

```text
Bump <package> from <old-version> to <new-version>
```

## CVE Fixes

**Single CVE:**
```text
Bump <abbreviated-package> for <CVE-ID>

Full package: <full-package-path>
```

**Multiple CVEs (one package update):**

Use "for CVEs" in subject to stay ≤50 chars, list all in body.

```text
Bump <abbreviated-package> for CVEs

Full package: <full-package-path>
Fixes: <CVE-ID-1>, <CVE-ID-2>
```

**Package abbreviations:**
- `github.com/docker/docker` → `docker/docker`
- `golang.org/x/oauth2` → `x/oauth2`
- `helm.sh/helm/v3` → `helm/v3`
- Keep `k8s.io/` prefix

**Examples:**
```text
Bump docker/docker for GHSA-4vq8-7jfc-9cvp

Full package: github.com/docker/docker
```

```text
Bump helm/v3 for CVEs

Full package: helm.sh/helm/v3
Fixes: GHSA-f9f8-9pmf-xv68, GHSA-9h84-qmv7-982p
```

## Code Changes

```text
Add <feature>
Fix <bug>
Update <component>
Remove <deprecated-feature>
```
