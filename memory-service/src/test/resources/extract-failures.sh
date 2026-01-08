#!/bin/bash
# Extract failed Cucumber scenarios and steps from cucumber.json
# Outputs: target/cucumber/failures.json and target/cucumber/failures.txt

cd "$(dirname "$0")/../../.."
set -euo pipefail

# Determine project base directory (memory-service module)
# When run from Maven, working directory is memory-service/
# When run directly, we need to find the module directory
if [ -d "target" ]; then
    PROJECT_BASE="$(pwd)"
elif [ -d "memory-service/target" ]; then
    PROJECT_BASE="$(pwd)/memory-service"
else
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    PROJECT_BASE="$(cd "${SCRIPT_DIR}/../.." && pwd)"
fi

CUCUMBER_DIR="${PROJECT_BASE}/target/cucumber"
CUCUMBER_JSON="${CUCUMBER_DIR}/cucumber.json"
FAILURES_JSON="${CUCUMBER_DIR}/failures.json"
FAILURES_TXT="${CUCUMBER_DIR}/failures.txt"

# Ensure cucumber directory exists
mkdir -p "${CUCUMBER_DIR}"

# Delete failures.txt at the start of test execution to ensure clean state
rm -f "${FAILURES_TXT}" "${FAILURES_JSON}"

# Check if jq is available
if ! command -v jq &> /dev/null; then
    echo "Error: jq is required but not installed. Install jq to extract failures." >&2
    exit 1
fi

# Check if cucumber.json exists
if [ ! -f "${CUCUMBER_JSON}" ]; then
    echo "No cucumber.json found at ${CUCUMBER_JSON}" >&2
    exit 0
fi

# Extract only failed scenarios and steps with full error details
FAILURES=$(jq -c '[.[] | 
    select(.elements[]? | select(.steps[]? | select(.result.status == "failed" or .result.status == "undefined"))) |
    {
        uri: .uri,
        name: .name,
        id: .id,
        keyword: .keyword,
        elements: [.elements[]? | 
            select(.steps[]? | select(.result.status == "failed" or .result.status == "undefined")) |
            {
                id: .id,
                name: .name,
                keyword: .keyword,
                type: .type,
                steps: [.steps[]? | 
                    select(.result.status == "failed" or .result.status == "undefined") |
                    {
                        keyword: .keyword,
                        name: .name,
                        line: .line,
                        match: .match,
                        result: {
                            status: .result.status,
                            duration: .result.duration,
                            error_message: .result.error_message,
                            error_type: .result.error_type
                        }
                    }
                ]
            }
        ]
    }
]' "${CUCUMBER_JSON}" 2>/dev/null || echo "[]")

# Check if there are any failures
FAILURE_COUNT=$(echo "${FAILURES}" | jq 'length')

if [ "${FAILURE_COUNT}" -eq 0 ]; then
    # No failures - remove failures.json if it exists
    rm -f "${FAILURES_JSON}" "${FAILURES_TXT}"
    exit 0
fi

# Write failures.json
echo "${FAILURES}" | jq '.' > "${FAILURES_JSON}"

# Generate text summary
{
    echo "CUCUMBER TEST FAILURES"
    echo "====================="
    echo ""
    
    echo "${FAILURES}" | jq -r '.[] | 
        "Feature: \(.uri // "unknown")",
        "Scenario: \(.name // "unknown")",
        "",
        (.elements[]? | 
            "  Scenario: \(.name // "unknown")",
            "  Steps:",
            (.steps[]? |
                "    - \(.keyword // "") \(.name // "")",
                "      Status: \(.result.status // "unknown")",
                "      Line: \(.line // "unknown")",
                (if .result.error_type then "      Error Type: \(.result.error_type)" else "" end),
                (if .result.error_message then 
                    "      Error Message:",
                    (.result.error_message | split("\n") | map("        " + .) | .[])
                else "" end),
                ""
            )
        ),
        "---",
        ""
    '
} > "${FAILURES_TXT}"

echo "Extracted ${FAILURE_COUNT} failed scenario(s) to ${FAILURES_JSON} and ${FAILURES_TXT}"
