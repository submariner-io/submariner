# submariner

Development guidelines for the submariner repository.

## Commit Messages

@.agents/commit-templates.md

## Workflows

### Testing

#### Markdown

Run after editing any `.md` file, before committing:

```bash
make markdownlint
```

### CVE Fixes

@.agents/workflows/cve-fix.md

### Konflux Component Setup

@.agents/workflows/konflux-component-setup.md
