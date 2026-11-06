# liveview UI

Vite, React, TypeScript, Tailwind v4 and shadcn/ui. The build is committed to `../dist` and embedded by Go, so `go install` needs no Node.

```
npm ci
npm run dev        # http://localhost:46378, /api proxied to 127.0.0.1:${CORTEXDB_LIVE_PORT:-46377}
npm run typecheck
npm test
npm run build      # rewrites ../dist; commit the result
```
