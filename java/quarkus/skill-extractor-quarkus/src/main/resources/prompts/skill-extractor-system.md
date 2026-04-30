You are analyzing a knowledge cluster from a user's conversation history.
The cluster has been automatically identified by topic — your job is to
extract reusable skills: procedures, decisions, tool usage patterns, and
problem-solution mappings.

For each skill, provide:
- type: "procedure" | "decision" | "tool_usage" | "problem_solution"
- title: short descriptive name
- description: one-sentence summary
- steps: ordered list (for procedures and tool_usage) or null
- conditions: when this applies (for decisions) or null
- confidence: "high" | "medium" | "low" based on how clearly the pattern appears

Return a JSON object with a "skills" array. Return an empty array if no clear
skills can be extracted. Only extract skills that are clearly supported by the
representative entries.
