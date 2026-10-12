# Pachanga — agent instructions

Spec: `docs/SPEC.md` (source of truth). State: `docs/status.json` (read first, update last).

## Every iteration
1. Read `docs/status.json`: `current_milestone`, `next_steps`, `pending_questions`, `blockers`.
2. Do the work. Don't build features from later milestones (spec §0).
3. Before ending, update `docs/status.json`: move done items, append `decisions` / `findings`, refresh `next_steps`, add `pending_questions` for the product owner. Bump `updated_at`. One log entry per session in `iterations`.

## Agent behaviour
- **Plan mode for large chunks.** A milestone, or any change spanning several domains/apps, starts in plan mode (`EnterPlanMode`). Get the plan approved, then execute.
- **Cheap models for well-scoped work.** Once a subtask is fully specified (exact files, commands, expected output, success check), spawn it with `Agent` using `model: "haiku"` (`subagent_type: "general-purpose"`). Examples: boilerplate from an exact template, running a command and reporting output, mechanical renames, i18n key fills.
- **Keep judgment on the lead model.** Architecture, ambiguity, cross-file reasoning, business rules (quorum, ratings, conduct score), security code: lead model only.
- **Exploration** uses `Explore`/`general-purpose` at normal tier, not haiku.
- **Verify** every subagent result (diff, test run) before marking it done in `status.json`.
- Run independent subagents in parallel, one message. **Exception:** never let two agents run `npm install`/`npx expo install` at once in this workspace (shared root node_modules corrupts). Serialize installs; recover with `npm ci`.

## Commands
- Docker lives at `~/.docker/bin` (user's `.zshrc`); agent shells: `export PATH=$HOME/.docker/bin:$PATH`.
- `make up` / `make seed` / `make down` — local stack (api :8080, Postgres :5432, S3 :8333). Seed password `pachanga123`; admin TOTP URL printed by seed.
- `make gen` (OpenAPI + sqlc + TS client), `make lint`, `make test` (needs Docker), `make ci` (everything CI runs).
- Edit `docs/openapi.yaml` or `db/queries/*.sql` → `make gen`. Never hand-edit `internal/api`, `internal/db`, `packages/api-client/src/schema.d.ts`.

## Code conventions
- Lazy = minimal: stdlib/already-installed deps first, no speculative abstractions. Mark known-ceiling shortcuts with `// ponytail:` comments.
- Ambiguity → simplest option consistent with spec, record in `status.json.decisions`.
- Code/comments English; user strings via i18n keys (es-ES default, en).
