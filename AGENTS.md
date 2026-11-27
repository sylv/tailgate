tailgate is a reverse proxy that puts Docker apps on a tailnet and through Tailscale Funnel, each on its own hostname, behind a login. It is one Go binary with an embedded React UI. `README.md` has what it does and how it's used

## Layout

- `main.go` and the Go packages next to it are the backend: Docker labels, tsnet nodes, auth, OIDC, the proxy and SQLite storage
- `client/` (TypeScript, React, Vite, Tailwind) is the UI. It uses pnpm, never npm or yarn. Its build output is embedded into the binary with `go:embed`

## Security

tailgate decides what is reachable from the internet, so mistakes are costly

- Fail closed. An unknown label, an unreadable database, a node whose name doesn't match its label or any other doubt means the app is not routed or the request is denied
- Whether a request came from the tailnet or from Funnel comes from which listener accepted it. Never infer it from an IP address or a header
- Tailgate credentials and cookies never reach the app. Strip them before proxying

## Conventions

- Go tests are `_test.go` files next to the code, run with `go test ./...`. Tests must not need a tailnet, Docker or the network
- `.dockerignore` ignores everything by default and lists each Go package dir it keeps, so a new or renamed package has to be added there or the image build fails
- tailgate runs as PID 1 in its container, so it handles SIGTERM itself and shuts down its nodes cleanly

## Style

Keep it simple. Make the smallest change that solves the problem. Fewer lines beat "elegant" boilerplate

- Redesign from first principles. When integrating a change, don't bolt it onto the existing design. Redesign as if the requirement had been a foundational assumption from the start. Read all affected files first and ask "if we were writing this from scratch with this new requirement, what would we build?". Then carry the change through every reference: types, docs, examples
- Subtract before you add. Remove dead code and complexity first, then build on the simpler base. Design for observed usage, not speculative edge cases. No speculative validators, parsers or guards beyond what the task demands
- No backward compatibility by default. When an internal API or shape changes, migrate every caller and delete the old one in the same change. No compat layers, legacy fallbacks or step-by-step migrations unless asked
- For the same mechanical edit across many files, write a script or codemod instead of editing each file by hand
- Collapse layers that only forward: wrappers with one caller, adapters with no second implementation, pass-through methods
- Test behavior, not implementation. Keep a test that fails when the behavior breaks and keeps passing when only the internals change. Delete or skip the rest, including tests of trivial code or of what the types already guarantee
- Don't comment what the code already says. Comment non-obvious behavior forced by something we can't change, like Tailscale, Docker, a library or a protocol. If our own code needs a comment to make sense, fix the code instead. Write comments in lowercase and keep them casual, like `// tailscale renames clashing nodes to name-1 without telling us`

### Writing

Before writing or editing docs, READMEs, AGENTS.md or PR descriptions, read `.agents/skills/writing/SKILL.md`. For everything else, including comments, commit messages and replies, the short version is: no em dashes, semicolons or bold, no period at the end of a list item or paragraph, plain words and no filler

## Git

- Commit in small, sensible pieces
- Never push without explicit permission
- Commit messages are short and lowercase, like `fix funnel cert retry`

## Before you finish

Run `gofmt -w` on changed Go files, then `go vet ./...` and `go test ./...`. In `client/`, run `pnpm exec oxfmt <changed files>` and `pnpm lint`. Generated files and the UI build output are excluded from formatting and linting and must stay excluded

## Maintaining these docs

- Don't add to this file yourself. If you think something belongs here, say so and propose the wording. The bar is that an agent would reliably get it wrong without the note, and getting it wrong is common or costly
- If your change makes something here wrong (a renamed path, a removed package, a changed command), update it and tell the user exactly what you changed
- READMEs describe how a unit works. Update them when your change makes them wrong. Keep them short and limited to what it is, how it fits in and the gotchas. Don't log feature details or history
