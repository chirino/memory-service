#!/bin/sh
# manifest-to-csv.sh — thin wrapper that runs the manifest-to-csv Go helper.
# Reads loadtest/results/seed-manifest.json and writes:
#   loadtest/results/conversation-ids.csv
#   loadtest/results/long-tail-conversation-ids.csv
#   loadtest/results/fork-root-ids.csv
#
# Must be run from the repository root.
set -e
go run ./loadtest/benchmarks/manifest-to-csv.go "$@"
