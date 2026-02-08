# Checkpoint Maintenance

**Triggers**: Modifying tutorials (`site/src/pages/docs/`), checkpoints (`*/examples/doc-checkpoints/`), or memory-service APIs.

## Maintenance Rules

**When tutorials change:**
1. Rebuild affected checkpoints following updated steps
2. Verify all subsequent checkpoints still work
3. Update checkpoint READMEs if features changed

**When APIs change:**
1. Identify which checkpoints use the affected API
2. Update affected checkpoints and tutorials
3. Rebuild and test all affected checkpoints

**When checkpoint code changes:**
1. Verify it matches what the tutorial produces
2. Test: `./mvnw clean compile` and tutorial curl commands
3. Update README if functionality changed

---
*From [Enhancement 038](../../docs/enhancements/038-tutorial-checkpoints.md)*
