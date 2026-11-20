# Actualizador

Wails v3 desktop app: Go backend, Vue 3 + Nuxt UI frontend. See [AGENTS.md](AGENTS.md).

## Commands

| Task | Command |
| --- | --- |
| Dev mode (developer run, picks a free Vite port; `WAILS_VITE_PORT` forces one) | `wails3 task dev` |
| Production build (output in `bin/`) | `wails3 task build` |
| Package installer | `wails3 task package` |
| Type-check frontend | `cd frontend && npx vue-tsc -b` |
| Format frontend | `cd frontend && npm run format` |
| Format / vet Go | `gofmt -w .` / `go vet ./...` |
