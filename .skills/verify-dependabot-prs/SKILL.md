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
   - Enforce the 14-day release cooldown for direct and changed transitive versions using authoritative registry or release metadata.
   - Defer a PR if any changed version is too new or its publication time cannot be verified.
   - Record the checked PR number, exact head SHA, versions, publication dates, and conclusion in a JSON cooldown report. Use this shape:

     ```json
     {
       "checkedAt": "2026-07-10T18:00:00Z",
       "prs": {
         "344": {
           "headSha": "full commit SHA",
           "status": "eligible",
           "summary": "All changed versions were published at least 14 days ago."
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

   Never run `task test` and `task test:site` concurrently in one worktree. Stop the batch at the first failure, preserve its worktree and logs, and continue investigating from those artifacts.

6. If a multi-PR batch fails, test smaller halves without `--publish`. Prioritize PRs whose current CI already points at the failing gate. If both halves pass but the combined batch fails, report a cross-PR interaction and minimize the reproducing set. Always rerun the final surviving combined set.

7. Publish only the final maximal passing batch:

   ```sh
   python3 .skills/verify-dependabot-prs/scripts/dependabot_batch.py run \
     --prs 344 346 347 \
     --cooldown-report /absolute/path/cooldown.json \
     --publish
   ```

   Publication pushes the exact validated batch commit to a new `chore/dependabot-batch-*` branch and opens one draft PR. The PR body records the source PRs and SHAs, their CI states, the cooldown evidence, and all local gates. Do not edit, close, approve, or merge the original Dependabot PRs.

## Result handling

- Cache results under the repository's common Git directory, keyed by the base SHA, ordered PR head SHAs, and gate definition. Reuse only an exact passing cache entry.
- Remove a passing worktree after the batch PR is created unless `--keep-passed` is set.
- Preserve failed worktrees and logs. Nothing is pushed when preparation or validation fails.
- Report the batch PR URL and its new CI status. The new batch PR's CI is the final remote integration signal; do not mark it ready or merge it unless the user separately requests that action.
