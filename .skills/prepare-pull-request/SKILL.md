---
name: prepare-pull-request
description: Prepare Memory Service changes for pull request submission. Use whenever Codex is asked to create, open, submit, publish, or ready a pull request, or to perform final PR preflight. Require `task generate` before the final PR commit so generated sources and repository-wide formatting are included.
---

# Prepare Pull Request

Run this workflow before invoking any commit or pull-request publishing workflow.

1. Inspect `git status --short` and preserve unrelated user changes.
2. From the repository root, run:

   ```bash
   task generate
   ```

   This is the canonical generation and formatting command for the repository. Do not substitute narrower formatters for this required preflight.
3. If `task generate` fails, diagnose the failure and stop PR submission until it succeeds. Do not submit a PR with partially generated output.
4. Review `git status --short` and the complete diff after generation. Keep intended generated and formatting changes in the PR; do not silently discard unexpected changes.
5. Run `git diff --check` and the verification required for the affected modules. Use the `build-test` skill to select those commands.
6. Ensure every intended change produced by `task generate` is committed before creating or updating the PR. If generation ran after the previous final commit, create the required follow-up commit before submission.

Report the `task generate` result and the verification performed when handing off the PR.
