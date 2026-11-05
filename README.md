# My Product

Wails v3 desktop app: Go backend, Vue 3 + Nuxt UI frontend. See [AGENTS.md](AGENTS.md).

## Commands

| Task | Command |
| --- | --- |
| Dev mode | `wails3 dev` |
| Production build | `wails3 build` |
| Package installer | `wails3 package` |
| Type-check frontend | `cd frontend && npx vue-tsc -b` |
| Format frontend | `cd frontend && npm run format` |
| Format / vet Go | `gofmt -w .` / `go vet ./...` |
