# CONTEXT.md - Workspace Execution Manual

## Bootstrap Pipeline (Session Init)
- [Define how the system initializes upon wake-up. e.g., Load previous state from database or parse current workspace files]

## Memory Management (The Journal System)
- **Dynamic Ledger**: [Instructions on what to dump into the daily journal]
- **Consolidation**: [Instructions on what merits elevating from the daily journal to persistent storage like RULES or TECH_TRENDS]

## Subagent Delegation Flow
- [Define the boundaries and instructions for dispatching tasks to Subagents]
- [Context-injection rules for child agents]

## Hazardous Interventions
- [Restrictions on destructive IO operations]
- [Pre-flight checklists before invoking sensitive systemic tools]
