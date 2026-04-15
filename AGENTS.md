# AGENTS.md

## Project: provider – Akash Network Provider services

### Required Context

### Spec-First Development

All changes MUST be reflected in SPEC.md and DESIGN.md **before** implementation begins:

1. **Propose** the change by describing what needs updating in the spec/design docs.
2. **Update** SPEC.md and/or DESIGN.md with the agreed-upon design.
3. **Implement** the code changes to match the updated spec.

Never implement code that contradicts or isn't documented in the spec. If the spec is wrong or incomplete, fix the spec first.

### Environment Setup

This project uses **direnv** (`.envrc`) to configure the shell environment (paths, env vars, tool versions). Before running any `make` command or build step, you **must** ensure the direnv environment is loaded:

```bash
direnv allow
```

Without this, builds and tooling will fail or use incorrect versions.

### Code Conventions

- **Language**: Go
- **Module path**: `github.com/akash-network/provider`
- **Config format**: YAML with 2-space indentation
- **Config management**: Viper (reading, writing, env binding, flag binding, live-reload)
- **Global variables**: Default to no package-level `var` usage. If you believe a global is necessary, you MUST ask for direction first (you may offer alternatives or suggestions). Only proceed after explicit approval and, if needed, a spec update.
- **`init`** - every `init` usage must be explicitly approved
- **Build**: `make bins` — the compiled binary is placed in `.cache/bin/`

### Workflow

- **Plan before implementing.** Every solution — regardless of agent mode (plan, build, or any other) — MUST be presented as a plan and approved before any code changes are made. Do not skip the planning step even if the change seems trivial.

### Problem Solving

- **Never guess or hack.** If something is not working and you don't understand why, ask for instructions. Do not invent workarounds, nil-guards, or fallback paths to mask a problem you haven't fully traced.
