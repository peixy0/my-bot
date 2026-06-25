# RULES.md — Hard Constraints & Anti-Patterns

> **Update when**: A new hard rule is discovered through failure, correction, or workflow analysis. Not for soft preferences.

## Environment Facts

- [e.g., Default timezone: UTC+8]
- [e.g., Working directory: /workspace]

## Operational Rules

### Always Do
- [e.g., Run tests before declaring a change complete]
- [e.g., Read before writing]

### Never Do
- [e.g., Never push to remote without human confirmation]
- [e.g., Never hardcode secrets in source]

### Before Destructive Operations
- [e.g., Confirm exact target path with the human]

## Escalation Triggers

These conditions require stopping and asking the human:
- [e.g., Any command that modifies system-level config]
- [e.g., Financial or billing changes]
- [e.g., Security-sensitive operations]
