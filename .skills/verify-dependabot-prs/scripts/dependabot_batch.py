#!/usr/bin/env python3
"""Discover, validate, and publish consolidated Dependabot PR batches."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
from typing import Any, Iterable


RUNNER_VERSION = 1
REQUIRED_CHECKS = {
    "build (verify-generated)",
    "build (test-go)",
    "build (test-java)",
    "build (test-site)",
}
FAILURE_CONCLUSIONS = {
    "ACTION_REQUIRED",
    "FAILURE",
    "STARTUP_FAILURE",
    "TIMED_OUT",
}
PENDING_STATUSES = {"EXPECTED", "IN_PROGRESS", "PENDING", "QUEUED", "REQUESTED", "WAITING"}
GATES = ("task generate", "clean generated diff", "task test", "task test:site")


class BatchError(RuntimeError):
    pass


def command(
    args: Iterable[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    capture: bool = True,
    log: Path | None = None,
) -> subprocess.CompletedProcess[str]:
    argv = list(args)
    if log is not None:
        log.parent.mkdir(parents=True, exist_ok=True)
        with log.open("w", encoding="utf-8") as stream:
            result = subprocess.run(
                argv,
                cwd=cwd,
                text=True,
                stdout=stream,
                stderr=subprocess.STDOUT,
                check=False,
            )
    else:
        result = subprocess.run(
            argv,
            cwd=cwd,
            text=True,
            capture_output=capture,
            check=False,
        )
    if check and result.returncode != 0:
        detail = ""
        if log is not None:
            detail = f"; see {log}"
        elif result.stderr:
            detail = f": {result.stderr.strip()}"
        elif result.stdout:
            detail = f": {result.stdout.strip()}"
        raise BatchError(f"command failed ({shlex.join(argv)}){detail}")
    return result


def json_command(args: Iterable[str], *, cwd: Path | None = None) -> Any:
    result = command(args, cwd=cwd)
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise BatchError(f"invalid JSON from {shlex.join(list(args))}: {exc}") from exc


def repo_root() -> Path:
    result = command(["git", "rev-parse", "--show-toplevel"])
    return Path(result.stdout.strip()).resolve()


def repo_info(root: Path) -> tuple[str, str]:
    data = json_command(
        ["gh", "repo", "view", "--json", "nameWithOwner,defaultBranchRef"], cwd=root
    )
    return data["nameWithOwner"], data["defaultBranchRef"]["name"]


def ci_state(checks: list[dict[str, Any]]) -> str:
    relevant = [check for check in checks if check.get("name") in REQUIRED_CHECKS]
    if not relevant:
        return "none"
    if any((check.get("conclusion") or "").upper() in FAILURE_CONCLUSIONS for check in relevant):
        return "red"
    if any((check.get("status") or "").upper() in PENDING_STATUSES for check in relevant):
        return "pending"
    passed = {
        check.get("name")
        for check in relevant
        if (check.get("conclusion") or "").upper() in {"SUCCESS", "SKIPPED", "NEUTRAL"}
    }
    if REQUIRED_CHECKS.issubset(passed):
        return "green"
    if any((check.get("conclusion") or "").upper() == "CANCELLED" for check in relevant):
        return "cancelled"
    return "partial"


def discover(root: Path) -> list[dict[str, Any]]:
    fields = ",".join(
        [
            "number",
            "title",
            "url",
            "baseRefName",
            "headRefName",
            "headRefOid",
            "mergeable",
            "statusCheckRollup",
            "files",
        ]
    )
    prs = json_command(
        [
            "gh",
            "pr",
            "list",
            "--state",
            "open",
            "--author",
            "app/dependabot",
            "--limit",
            "100",
            "--json",
            fields,
        ],
        cwd=root,
    )
    for pr in prs:
        pr["ciState"] = ci_state(pr.get("statusCheckRollup") or [])
        pr["filePaths"] = sorted(item["path"] for item in pr.get("files") or [])
    return sorted(prs, key=lambda item: item["number"])


def print_discovery(prs: list[dict[str, Any]]) -> None:
    if not prs:
        print("No open Dependabot PRs found.")
        return
    print("PR\tCI\tMergeable\tFiles\tTitle")
    for pr in prs:
        print(
            f"#{pr['number']}\t{pr['ciState']}\t{pr['mergeable']}\t"
            f"{len(pr['filePaths'])}\t{pr['title']}"
        )
    overlaps: list[str] = []
    for index, left in enumerate(prs):
        left_files = set(left["filePaths"])
        for right in prs[index + 1 :]:
            shared = left_files.intersection(right["filePaths"])
            if shared:
                overlaps.append(
                    f"#{left['number']}/#{right['number']}: {', '.join(sorted(shared))}"
                )
    if overlaps:
        print("\nOverlapping PR files:")
        for overlap in overlaps:
            print(f"- {overlap}")


def selected_prs(all_prs: list[dict[str, Any]], numbers: list[int]) -> list[dict[str, Any]]:
    by_number = {pr["number"]: pr for pr in all_prs}
    missing = [number for number in numbers if number not in by_number]
    if missing:
        raise BatchError(f"not open Dependabot PRs: {', '.join(map(str, missing))}")
    return [by_number[number] for number in numbers]


def validate_cooldown_report(path: Path, prs: list[dict[str, Any]]) -> dict[str, Any]:
    try:
        report = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise BatchError(f"cannot read cooldown report {path}: {exc}") from exc
    entries = report.get("prs") or {}
    for pr in prs:
        entry = entries.get(str(pr["number"]))
        if not entry:
            raise BatchError(f"cooldown report has no entry for PR #{pr['number']}")
        if entry.get("headSha") != pr["headRefOid"]:
            raise BatchError(f"cooldown report SHA is stale for PR #{pr['number']}")
        if entry.get("status") != "eligible":
            raise BatchError(f"PR #{pr['number']} is not cooldown-eligible")
        if not entry.get("summary"):
            raise BatchError(f"cooldown report lacks a summary for PR #{pr['number']}")
    return report


def base_sha(root: Path, repo: str, branch: str) -> str:
    data = json_command(["gh", "api", f"repos/{repo}/commits/{branch}"], cwd=root)
    return data["sha"]


def fingerprint(base: str, prs: list[dict[str, Any]]) -> str:
    payload = {
        "runnerVersion": RUNNER_VERSION,
        "base": base,
        "prs": [(pr["number"], pr["headRefOid"]) for pr in prs],
        "gates": GATES,
    }
    encoded = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()[:16]


def common_git_dir(root: Path) -> Path:
    value = command(["git", "rev-parse", "--git-common-dir"], cwd=root).stdout.strip()
    path = Path(value)
    if not path.is_absolute():
        path = root / path
    return path.resolve()


def worktree_path(root: Path, name: str) -> Path:
    return root.parent / f"{root.name}@{name}"


def fetch_refs(
    root: Path,
    prs: list[dict[str, Any]],
    base_branch: str,
    expected_base: str,
    namespace: str,
) -> str:
    base_ref = f"refs/dependabot-batch/{namespace}/base"
    command(
        ["git", "fetch", "origin", f"refs/heads/{base_branch}:{base_ref}"], cwd=root
    )
    fetched_base = command(["git", "rev-parse", base_ref], cwd=root).stdout.strip()
    if fetched_base != expected_base:
        raise BatchError("default branch changed during preparation; rediscover and retry")
    for pr in prs:
        ref = f"refs/dependabot-batch/{namespace}/pr-{pr['number']}"
        command(
            ["git", "fetch", "origin", f"refs/pull/{pr['number']}/head:{ref}"], cwd=root
        )
        fetched = command(["git", "rev-parse", ref], cwd=root).stdout.strip()
        if fetched != pr["headRefOid"]:
            raise BatchError(f"fetched SHA changed for PR #{pr['number']}; rediscover and retry")
    return base_ref


def prepare_worktree(
    root: Path,
    prs: list[dict[str, Any]],
    base_ref: str,
    name: str,
    branch: str,
    namespace: str,
) -> tuple[Path, str]:
    path = worktree_path(root, name)
    if path.exists():
        raise BatchError(f"worktree path already exists: {path}")
    command(["wt", "add", name], cwd=root, capture=False)
    command(["git", "switch", "-c", branch, base_ref], cwd=path)
    command(["git", "config", "user.name", "Dependabot Batch Verifier"], cwd=path)
    command(
        ["git", "config", "user.email", "dependabot-batch@users.noreply.github.com"], cwd=path
    )
    for pr in prs:
        ref = f"refs/dependabot-batch/{namespace}/pr-{pr['number']}"
        result = command(
            [
                "git",
                "merge",
                "--no-ff",
                "-m",
                f"chore(deps): merge dependabot PR #{pr['number']}",
                ref,
            ],
            cwd=path,
            check=False,
        )
        if result.returncode != 0:
            raise BatchError(f"merge conflict while adding PR #{pr['number']}; preserved {path}")
    head = command(["git", "rev-parse", "HEAD"], cwd=path).stdout.strip()
    return path, head


def state_file(state_dir: Path) -> Path:
    return state_dir / "state.json"


def cached_pass(state_dir: Path, expected_tree: str) -> bool:
    try:
        state = json.loads(state_file(state_dir).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return False
    return state.get("verdict") == "passed" and state.get("validatedTree") == expected_tree


def write_state(state_dir: Path, **values: Any) -> None:
    state_dir.mkdir(parents=True, exist_ok=True)
    path = state_file(state_dir)
    current: dict[str, Any] = {}
    try:
        current = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        pass
    current.update(values)
    path.write_text(json.dumps(current, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def run_gate(root: Path, name: str, state_dir: Path, label: str, task_name: str) -> None:
    log_name = label.replace(" ", "-") + ".log"
    log = state_dir / log_name
    result = command(
        ["wt", "exec", name, "--", "task", task_name],
        cwd=root,
        check=False,
        log=log,
    )
    if result.returncode != 0:
        raise BatchError(f"{label} failed; see {log}")


def verify(
    root: Path,
    path: Path,
    name: str,
    state_dir: Path,
    expected_head: str,
    expected_tree: str,
) -> None:
    if cached_pass(state_dir, expected_tree):
        print(f"Reusing passing validation cache at {state_dir}")
        return
    write_state(
        state_dir,
        verdict="running",
        validatedHead=expected_head,
        validatedTree=expected_tree,
        startedAt=dt.datetime.now(dt.timezone.utc).isoformat(),
    )
    command(["wt", "up", name], cwd=root, capture=False)
    try:
        run_gate(root, name, state_dir, "task generate", "generate")
        status = command(
            ["git", "status", "--porcelain", "--untracked-files=all"], cwd=path
        ).stdout
        if status:
            diff_log = state_dir / "generated-diff.log"
            diff = command(["git", "diff", "--ignore-space-at-eol"], cwd=path)
            diff_log.write_text(status + "\n" + diff.stdout, encoding="utf-8")
            raise BatchError(f"task generate left tracked or untracked changes; see {diff_log}")
        run_gate(root, name, state_dir, "task test", "test")
        run_gate(root, name, state_dir, "task test site", "test:site")
    except BatchError:
        write_state(
            state_dir,
            verdict="failed",
            completedAt=dt.datetime.now(dt.timezone.utc).isoformat(),
        )
        raise
    final_head = command(["git", "rev-parse", "HEAD"], cwd=path).stdout.strip()
    if final_head != expected_head:
        raise BatchError("batch HEAD changed during validation")
    final_tree = command(["git", "rev-parse", "HEAD^{tree}"], cwd=path).stdout.strip()
    if final_tree != expected_tree:
        raise BatchError("batch tree changed during validation")
    write_state(
        state_dir,
        verdict="passed",
        completedAt=dt.datetime.now(dt.timezone.utc).isoformat(),
    )


def pr_body(
    prs: list[dict[str, Any]],
    report: dict[str, Any],
    base: str,
    validated_head: str,
    validated_tree: str,
) -> str:
    lines = [
        "## Summary",
        "",
        "Consolidates the following Dependabot PRs into one locally validated batch:",
        "",
    ]
    for pr in prs:
        lines.append(
            f"- #{pr['number']} — {pr['title']} (`{pr['headRefOid'][:12]}`, original CI: {pr['ciState']})"
        )
    lines.extend(["", "## Supply-chain cooldown", ""])
    for pr in prs:
        entry = report["prs"][str(pr["number"])]
        lines.append(f"- #{pr['number']}: {entry['summary']}")
    lines.extend(
        [
            "",
            "## Validation",
            "",
            "- [x] `task generate`",
            "- [x] clean Git worktree after generation",
            "- [x] `task test`",
            "- [x] `task test:site`",
            "",
            f"Base SHA: `{base}`",
            f"Validated batch SHA: `{validated_head}`",
            f"Validated tree SHA: `{validated_tree}`",
            "",
            "The original Dependabot PRs are intentionally left unchanged.",
        ]
    )
    return "\n".join(lines) + "\n"


def publish(
    root: Path,
    path: Path,
    repo: str,
    base_branch: str,
    branch: str,
    prs: list[dict[str, Any]],
    report: dict[str, Any],
    base: str,
    validated_head: str,
    validated_tree: str,
    state_dir: Path,
) -> str:
    current = command(["git", "rev-parse", "HEAD"], cwd=path).stdout.strip()
    current_tree = command(["git", "rev-parse", "HEAD^{tree}"], cwd=path).stdout.strip()
    if (
        current != validated_head
        or current_tree != validated_tree
        or not cached_pass(state_dir, validated_tree)
    ):
        raise BatchError("refusing to publish a branch that is not the exact validated HEAD")
    command(["git", "push", "--set-upstream", "origin", branch], cwd=path, capture=False)
    body_path = state_dir / "pull-request-body.md"
    body_path.write_text(
        pr_body(prs, report, base, validated_head, validated_tree),
        encoding="utf-8",
    )
    title = f"chore(deps): consolidate {len(prs)} dependabot updates"
    result = command(
        [
            "gh",
            "pr",
            "create",
            "--repo",
            repo,
            "--base",
            base_branch,
            "--head",
            branch,
            "--title",
            title,
            "--body-file",
            str(body_path),
            "--draft",
        ],
        cwd=path,
    )
    url = result.stdout.strip()
    write_state(state_dir, publishedPr=url)
    return url


def dry_run(
    prs: list[dict[str, Any]], base_branch: str, base: str, branch: str, report: Path
) -> None:
    print(f"Base: {base_branch} @ {base}")
    print(f"Batch branch: {branch}")
    print(f"Cooldown report: {report}")
    print("PR order:")
    for pr in prs:
        print(f"- #{pr['number']} @ {pr['headRefOid']} (CI: {pr['ciState']})")
    print("Gates:")
    for gate in GATES:
        print(f"- {gate}")


def run_batch(args: argparse.Namespace) -> None:
    root = repo_root()
    repo, default_branch = repo_info(root)
    all_prs = discover(root)
    prs = selected_prs(all_prs, args.prs)
    if any(pr["baseRefName"] != default_branch for pr in prs):
        raise BatchError("all selected PRs must target the default branch")
    report_path = Path(args.cooldown_report).expanduser().resolve()
    report = validate_cooldown_report(report_path, prs)
    base = base_sha(root, repo, default_branch)
    batch_id = fingerprint(base, prs)
    timestamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d")
    name = args.name or f"dependabot-{timestamp}-{batch_id[:8]}"
    branch = f"chore/dependabot-batch-{timestamp}-{batch_id[:8]}"
    if args.dry_run:
        dry_run(prs, default_branch, base, branch, report_path)
        return

    state_dir = common_git_dir(root) / "dependabot-verification" / batch_id
    namespace = f"batch-{batch_id}"
    base_ref = fetch_refs(root, prs, default_branch, base, namespace)
    path, head = prepare_worktree(root, prs, base_ref, name, branch, namespace)
    tree = command(["git", "rev-parse", "HEAD^{tree}"], cwd=path).stdout.strip()
    write_state(
        state_dir,
        baseSha=base,
        branch=branch,
        prs=[{"number": pr["number"], "headSha": pr["headRefOid"]} for pr in prs],
        worktree=str(path),
    )
    try:
        verify(root, path, name, state_dir, head, tree)
        print(f"Batch passed all gates. Logs: {state_dir}")
        if args.publish:
            url = publish(
                root,
                path,
                repo,
                default_branch,
                branch,
                prs,
                report,
                base,
                head,
                tree,
                state_dir,
            )
            print(f"Created draft batch PR: {url}")
            if not args.keep_passed:
                command(["wt", "down", name], cwd=root, check=False, capture=False)
                command(["wt", "rm", name, "--force"], cwd=root, capture=False)
        elif not args.keep_passed:
            command(["wt", "down", name], cwd=root, check=False, capture=False)
            command(["wt", "rm", name, "--force"], cwd=root, capture=False)
    except BatchError:
        print(f"Preserved failed batch worktree: {path}", file=sys.stderr)
        print(f"Logs and state: {state_dir}", file=sys.stderr)
        raise


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    subparsers = result.add_subparsers(dest="command", required=True)
    discover_parser = subparsers.add_parser("discover", help="list open Dependabot PRs")
    discover_parser.add_argument("--json", action="store_true", help="emit JSON")

    run_parser = subparsers.add_parser("run", help="prepare and validate a selected batch")
    run_parser.add_argument("--prs", nargs="+", type=int, required=True, metavar="NUMBER")
    run_parser.add_argument("--cooldown-report", required=True)
    run_parser.add_argument("--name", help="override the disposable wt worktree name")
    run_parser.add_argument("--publish", action="store_true", help="push and open a draft PR")
    run_parser.add_argument("--dry-run", action="store_true")
    run_parser.add_argument("--keep-passed", action="store_true")
    return result


def main() -> int:
    args = parser().parse_args()
    try:
        root = repo_root()
        if args.command == "discover":
            prs = discover(root)
            if args.json:
                print(json.dumps(prs, indent=2, sort_keys=True))
            else:
                print_discovery(prs)
            return 0
        run_batch(args)
        return 0
    except BatchError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
