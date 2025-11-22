#### Setting up Component Builds in Konflux on New Branch

**Prerequisites:**

- Configuration added in konflux-ci/build-definitions repo
- Existing Konflux-configured branch to copy files from (e.g., `release-0.21`)

**Placeholders:**
- `<target-branch>`: Your target branch (e.g., `release-0.22`)
- `<X-Y>`: Version with dashes (e.g., `0-22`)
- `<component>`: Component name (gateway, globalnet, or route-agent)

**Repeat steps 1-9 for each component:**

##### 1. Checkout Bot's PR Branch

Bot creates PRs on branches named `konflux-submariner-<component>-<X-Y>`.

```bash
git checkout konflux-submariner-<component>-<X-Y>
```

##### 2. Configure YAMLlint to Ignore Generated Directories

Add `.tekton` and `.rpm-lockfiles` to yamllint ignore list (idempotent, preserves existing rules).

```bash
grep -q "\.tekton" .yamllint.yml || sed -i '/^ignore: |$/a\  .tekton' .yamllint.yml
grep -q "\.rpm-lockfiles" .yamllint.yml || sed -i '/^ignore: |$/a\  .rpm-lockfiles' .yamllint.yml
git add .yamllint.yml
git commit -s -m "Configure yamllint to ignore .tekton and .rpm-lockfiles"
```

##### 3. Add RPM Lockfile Support

```bash
# Extract target version once, validate once, derive previous version
TARGET_VERSION=$(echo "<target-branch>" | grep -oP '(?<=release-0\.)\d+$')
[ -z "$TARGET_VERSION" ] && { echo "ERROR: Invalid target branch format. Expected release-0.XX"; exit 1; }
PREV_VERSION=$((TARGET_VERSION - 1))
git checkout origin/release-0.${PREV_VERSION} -- .rpm-lockfiles/update-lockfile.sh .rpm-lockfiles/<component>/
chmod +x .rpm-lockfiles/update-lockfile.sh
.rpm-lockfiles/update-lockfile.sh <component>
ls .rpm-lockfiles/<component>/rpms.lock.yaml || { echo "ERROR: Lockfile generation failed"; exit 1; }
git add .rpm-lockfiles/
git commit -s -m "Add RPM lockfile support for <component>"
```

**Note:** Script copied per-component; Git deduplicates on merge.

##### 4. Add Konflux Dockerfile and Configure Tekton to Use It

```bash
# Extract target version once, validate once, derive all version values
# Formula: Submariner 0.X → ACM 2.(X-7), so 0.22 → 2.15
TARGET_VERSION=$(echo "<target-branch>" | grep -oP '(?<=release-0\.)\d+$')
[ -z "$TARGET_VERSION" ] && { echo "ERROR: Invalid target branch format. Expected release-0.XX"; exit 1; }
PREV_VERSION=$((TARGET_VERSION - 1))
ACM_VERSION=$((TARGET_VERSION - 7))

git checkout origin/release-0.${PREV_VERSION} -- package/Dockerfile.submariner-<component>.konflux
sed -i "s/release-0.${PREV_VERSION}/<target-branch>/g" package/Dockerfile.submariner-<component>.konflux
sed -i "s/cpe=\"cpe:\/a:redhat:acm:[0-9.]*::el9\"/cpe=\"cpe:\/a:redhat:acm:2.${ACM_VERSION}::el9\"/" package/Dockerfile.submariner-<component>.konflux

sed -i 's|package/Dockerfile.submariner-<component>|package/Dockerfile.submariner-<component>.konflux|g' .tekton/*.yaml
git add package/Dockerfile.submariner-<component>.konflux .tekton/*.yaml
git commit -s -m "Add Konflux dockerfile for <component> and configure tekton to use it"
```

##### 5. Enable Hermetic Builds

```bash
# Only add if not already present (idempotent)
# Check for hermetic in spec.params (not pipelineSpec.params definitions)
if ! grep -q "^  - name: hermetic$" .tekton/*.yaml; then
  sed -i '/^  pipelineSpec:$/i\  - name: prefetch-input\n    value: '\''[{"type": "gomod", "path": "."}, {"type": "gomod", "path": "tools"}, {"type": "rpm", "path": "./.rpm-lockfiles/<component>"}]'\''\n  - name: hermetic\n    value: "true"' .tekton/*.yaml
fi
git add .tekton/*.yaml
git commit -s -m "Enable hermetic builds with gomod and RPM prefetching for <component>"
```

##### 6. Add Multi-Platform Support

```bash
# Only add if not already present (idempotent)
grep -q "linux/arm64" .tekton/*.yaml || sed -i '/^    - linux\/x86_64$/a\    - linux/arm64' .tekton/*.yaml
git add .tekton/*.yaml
git commit -s -m "Add multi-platform build support for <component>"
```

##### 7. Enable SBOM Generation

```bash
# Only add if not already present (idempotent)
# Check for build-source-image in spec.params (not pipelineSpec.params definitions)
if ! grep -q "^  - name: build-source-image$" .tekton/*.yaml; then
  sed -i '/  - name: hermetic$/,/    value: "true"$/{/    value: "true"$/a\  - name: build-source-image\n    value: "true"
}' .tekton/*.yaml
fi
git add .tekton/*.yaml
git commit -s -m "Enable SBOM generation for <component>"
```

##### 8. Update Task References

```bash
bash << 'EOF'
set -e

PATCHER_SHA="b001763bb1cd0286a894cfb570fe12dd7f4504bd"
EXPECTED_SHA256="080ad5d7cf7d0cee732a774b7e4dda0e2ccf26b58e08a8516a3b812bc73beb53"

SCRIPT=$(curl -sL "https://raw.githubusercontent.com/simonbaird/konflux-pipeline-patcher/${PATCHER_SHA}/pipeline-patcher")
ACTUAL_SHA256=$(echo "$SCRIPT" | sha256sum | cut -d' ' -f1)

if [[ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]]; then
  echo "ERROR: Script checksum mismatch!"
  exit 1
fi

echo "$SCRIPT" | bash -s bump-task-refs
EOF
git diff --quiet .tekton/*.yaml || { git add .tekton/*.yaml && git commit -s -m "Update Tekton task references to latest versions for <component>"; }
```

**Note:** Updates task references if outdated.

##### 9. Review and Push

```bash
git log origin/<target-branch>..HEAD
git status
git push
```

Expected: 7-8 commits (bot's initial + 6-7 from steps 2-8), clean working tree.

##### 10. Verify All Component PRs

After completing all 3 components:

```bash
for component in gateway globalnet route-agent; do
  gh pr list --head konflux-submariner-$component-<X-Y>
done
```

Expected: 3 PRs (one per component).
