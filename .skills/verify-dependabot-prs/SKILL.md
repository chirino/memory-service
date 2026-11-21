---
name: verify-dependabot-prs
description: Batch, validate, and consolidate open Dependabot pull requests for memory-service. Use when asked to inspect the Dependabot queue, verify dependency PRs efficiently, reproduce dependency-update CI failures, combine compatible dependency PRs, or create a validated consolidated dependency PR.
---

# Verify Dependabot PRs

Use `scripts/dependabot_batch.py` for discovery, isolated validation, caching, and publication. Keep the user's current worktree untouched.

## Workflow

1. Run discovery from the repository root:

   ```sh
   python3 .skills/verify-dependabot-prs/scripts/dependabot_batch.py discover
   ```

2. Inspect every selected PR before executing dependency code:
   - Confirm it is authored by Dependabot and targets the default branch.
   - Review its manifest and lockfile changes.
   - Read the project's pin ledger, normally `.dependencies/pins.json`. Also check the legacy `.codex/dependency-pins.json` location and intentional holds documented in manifests, overrides, replacements, and nearby comments. Honor applicable holds in the current checkout as well as the default-branch revision being validated.
   - Compare direct and changed transitive versions against each hold's scope, class, reason, and review condition. A routine exact version for reproducibility is not automatically a compatibility hold.
   - Preserve user-defined and unknown-legacy holds. Defer conflicting PRs unless the user has authorized revisiting the hold or its documented review condition has been met. Green CI and an expired release cooldown do not override compatibility or policy holds.
   - Re-evaluate temporary cooldown pins against current publication metadata and the resolved dependency graph. If a PR needs a pin change, prepare that change as a separate prerequisite before validating the exact PR heads. Do not silently remove holds or edit the user's worktree or the runner's batch commit. A temporary cooldown-only `uv` `exclude-newer` cutoff that blocks a verified security fix is such a pin: adjust it in the prerequisite without extra approval. Genuine compatibility, user-defined, and legacy holds still apply.
   - Classify each PR as a security fix or a routine update before checking age. Follow the security-fix rules in the `dependency-update` skill: map the exact PR head to a GHSA, CVE, or Go advisory by ecosystem, manifest path, affected range, and fixed version. Dependabot authorship or `open-pull-requests-limit: 0` alone does not prove a security fix, and a newest release does not fix a vulnerability the current version already fixed.
   - Enforce the 7-day release cooldown for direct and changed transitive versions using authoritative registry or release metadata. A version that fixes a verified advisory is exempt automatically. Every other changed version, including unrelated transitives, must still pass.
   - Defer a PR if any non-exempt changed version is too new or its publication time cannot be verified.
   - Record the checked PR number, exact head SHA, versions, publication dates, pin-review conclusion, and eligibility in a JSON cooldown report. Mark a PR eligible only after both the cooldown (or a documented security exemption) and applicable pin checks pass. The runner accepts only `"status": "eligible"` with a non-empty `summary`, so record a security exemption as `eligible`, state it in `summary`, and list the advisory ID, fixed version, and evidence URL in the optional `securityExemptions` field. Store this temporary report outside the tracked repository files. Use this shape:

     ```json
     {
       "checkedAt": "2026-07-10T18:00:00Z",
       "prs": {
         "344": {
           "headSha": "full commit SHA",
           "status": "eligible",
           "summary": "Listed security fixes are exempt from the cooldown. All other changed versions were published at least 7 days ago, and all changes respect the holds in .dependencies/pins.json.",
           "securityExemptions": [
             {
               "package": "name",
               "version": "fixed version",
               "advisory": "GHSA-xxxx-xxxx-xxxx",
               "evidence": "https://github.com/advisories/GHSA-xxxx-xxxx-xxxx"
             }
           ]
         }
       }
     }
     ```

3. Use current CI as evidence and prioritization, not as a substitute for local validation:
   - Batch PRs whose `verify-generated`, `test-go`, `test-java`, and `test-site` jobs are green.
   - Isolate CI-red PRs initially so a known failure does not contaminate the green batch.
   - Treat fail-fast cancellations as secondary to the first failed job.
   - Put PRs with overlapping files or merge conflicts into separate ordered batches.
   - A missing CI run may be expected for paths ignored by `.github/workflows/ci.yml`; report it explicitly.

4. Dry-run the selected batch:

   ```sh
   python3 .skills/verify-dependabot-prs/scripts/dependabot_batch.py run \
     --prs 344 346 347 \
     --cooldown-report /absolute/path/cooldown.json \
     --dry-run
   ```

5. Validate the batch. The runner creates a disposable `wt` worktree and devcontainer, merges the exact PR heads onto the latest default-branch SHA, and runs these gates sequentially:
   - `task generate`, then require a clean Git worktree.
   - `task test`.
   - `task test:site`.

   If reviewed prerequisite changes (for example a lockfile cutoff cleanup) must precede the PR heads, commit them in a separate worktree and pass `--base-commit <sha>`. The commit must descend from the default-branch tip, becomes part of the cache key, and is listed in the PR body.

   Never run `task test` and `task test:site` concurrently in one worktree. Stop the batch at the first failure, preserve its worktree and logs, and continue investigating from those artifacts.

   Recheck applicable holds against the merged dependency graph before running gates or reusing a cached pass. Review changes to the ledger itself; a PR removing a hold must satisfy the same authorization or review condition as a version update that conflicts with it.

6. If a multi-PR batch fails, test smaller halves without `--publish`. Prioritize PRs whose current CI already points at the failing gate. If both halves pass but the combined batch fails, report a cross-PR interaction and minimize the reproducing set. Always rerun the final surviving combined set.

7. Publish only the final maximal passing batch:

   ```sh
   python3 .skills/verify-dependabot-prs/scripts/dependabot_batch.py run \
     --prs 344 346 347 \
     --cooldown-report /absolute/path/cooldown.json \
     --publish
   ```

   Publication pushes the exact validated batch commit to a new `chore/dependabot-batch-*` branch and opens one draft PR. Batch merge commits must use the caller's normal Git author and committer identity; do not override `user.name`, `user.email`, or the corresponding `GIT_*` environment variables. The PR body records the source PRs and SHAs, their CI states, the cooldown evidence, and all local gates. Do not edit, close, approve, or merge the original Dependabot PRs.

## Result handling

- Cache results under the repository's common Git directory, keyed by the base SHA, ordered PR head SHAs, and gate definition. Reuse only an exact passing cache entry.
- Remove a passing worktree after the batch PR is created unless `--keep-passed` is set.
- Preserve failed worktrees and logs. Nothing is pushed when preparation or validation fails.
- Report PRs deferred by a pin or hold with the dependency, proposed version, ledger or manifest location, pin class, reason, and condition for reconsideration. Report expired cooldown pins that still need a prerequisite cleanup change.
- Report the batch PR URL and its new CI status. The new batch PR's CI is the final remote integration signal; do not mark it ready or merge it unless the user separately requests that action.
