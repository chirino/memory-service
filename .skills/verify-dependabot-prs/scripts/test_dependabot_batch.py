#!/usr/bin/env python3
"""Unit tests for dependabot_batch.py. Run: python3 -m unittest discover -s <scripts dir>."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import dependabot_batch as batch


def git(repo: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args], cwd=repo, text=True, capture_output=True, check=True
    )
    return result.stdout.strip()


def commit(repo: Path, name: str, message: str) -> str:
    (repo / name).write_text(message, encoding="utf-8")
    git(repo, "add", name)
    git(repo, "commit", "-q", "-m", message)
    return git(repo, "rev-parse", "HEAD")


PRS = [
    {"number": 10, "headRefOid": "a" * 40},
    {"number": 11, "headRefOid": "b" * 40},
]


class FingerprintTest(unittest.TestCase):
    def test_without_prerequisite_matches_legacy_payload(self) -> None:
        legacy = {
            "runnerVersion": batch.RUNNER_VERSION,
            "base": "base",
            "prs": [(10, "a" * 40), (11, "b" * 40)],
            "gates": batch.GATES,
        }
        encoded = json.dumps(legacy, sort_keys=True, separators=(",", ":")).encode()
        expected = hashlib.sha256(encoded).hexdigest()[:16]
        self.assertEqual(batch.fingerprint("base", PRS), expected)

    def test_prerequisite_changes_the_cache_key(self) -> None:
        plain = batch.fingerprint("base", PRS)
        first = batch.fingerprint("base", PRS, "1" * 40)
        second = batch.fingerprint("base", PRS, "2" * 40)
        self.assertEqual(len({plain, first, second}), 3)


class ResolvePrerequisiteTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.repo = Path(self.tmp.name)
        git(self.repo, "init", "-q", "-b", "main")
        git(self.repo, "config", "user.name", "Test")
        git(self.repo, "config", "user.email", "test@example.invalid")
        self.tip = commit(self.repo, "base.txt", "chore: base")

    def test_descendant_commit_is_accepted_with_subjects(self) -> None:
        commit(self.repo, "one.txt", "chore(deps): first prerequisite")
        head = commit(self.repo, "two.txt", "fix(deps): second prerequisite")
        sha, subjects = batch.resolve_prerequisite(self.repo, "HEAD", self.tip)
        self.assertEqual(sha, head)
        self.assertEqual(
            [subject.split(" ", 1)[1] for subject in subjects],
            ["chore(deps): first prerequisite", "fix(deps): second prerequisite"],
        )

    def test_commit_that_does_not_descend_from_tip_is_rejected(self) -> None:
        git(self.repo, "switch", "-q", "-c", "side", self.tip)
        side = commit(self.repo, "side.txt", "chore: side")
        git(self.repo, "switch", "-q", "main")
        newer_tip = commit(self.repo, "newer.txt", "chore: newer tip")
        with self.assertRaisesRegex(batch.BatchError, "must descend from"):
            batch.resolve_prerequisite(self.repo, side, newer_tip)

    def test_unknown_revision_is_rejected(self) -> None:
        with self.assertRaisesRegex(batch.BatchError, "is not a commit"):
            batch.resolve_prerequisite(self.repo, "no-such-revision", self.tip)


class PullRequestBodyTest(unittest.TestCase):
    PRS = [
        {"number": 10, "headRefOid": "a" * 40, "title": "bump x", "ciState": "green"}
    ]
    REPORT = {"prs": {"10": {"summary": "age checked"}}}

    def body(self, prerequisites: list[str] | None) -> str:
        return batch.pr_body(self.PRS, self.REPORT, "base", "head", "tree", prerequisites)

    def test_lists_prerequisite_commits_when_present(self) -> None:
        body = self.body(["abc1234 chore(deps): retire cutoff"])
        self.assertIn("## Prerequisite commits (not from Dependabot)", body)
        self.assertIn("- abc1234 chore(deps): retire cutoff", body)
        self.assertIn("- [x] `task test:site`", body)

    def test_omits_prerequisite_section_otherwise(self) -> None:
        self.assertNotIn("Prerequisite commits", self.body(None))
        self.assertNotIn("Prerequisite commits", self.body([]))


class CooldownReportTest(unittest.TestCase):
    def validate(self, entry: dict[str, str]) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "report.json"
            path.write_text(json.dumps({"prs": {"10": entry}}), encoding="utf-8")
            batch.validate_cooldown_report(path, [PRS[0]])

    def test_eligible_entry_with_matching_head_passes(self) -> None:
        self.validate({"headSha": "a" * 40, "status": "eligible", "summary": "ok"})

    def test_stale_head_and_non_eligible_status_fail(self) -> None:
        with self.assertRaisesRegex(batch.BatchError, "stale"):
            self.validate({"headSha": "c" * 40, "status": "eligible", "summary": "ok"})
        with self.assertRaisesRegex(batch.BatchError, "not cooldown-eligible"):
            self.validate({"headSha": "a" * 40, "status": "deferred", "summary": "ok"})


if __name__ == "__main__":
    unittest.main()
