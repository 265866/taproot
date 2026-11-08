# taproot dashboard

The dashboard taproot serves at `/` on its proxy port. It is a React and TypeScript app, built with Vite into `index.html` and hashed chunks under `assets/`, which the Go server embeds.

It started as a fork of the [CLIProxyAPI Management Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) and no longer follows it. It has no login page and holds no key: taproot's access guard decides who reaches it.

```sh
bun install --frozen-lockfile
bun run dev      # Vite at http://localhost:5173, proxying to a local taproot
bun run verify   # tests, lint, and the production build
```

`bash ../scripts/taproot/build-web.sh` builds the dashboard and copies it to where the server embeds it. [`AGENTS.md`](AGENTS.md) describes the structure, conventions, and checks. The [repository README](../README.md) covers taproot itself. The dashboard keeps the Management Center's [MIT License](LICENSE).
