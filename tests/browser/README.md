# Browser regressions

Development only; the Go binary has no Node or network dependency.

```
npm ci --prefix tests/browser
npx --prefix tests/browser playwright install chromium
npm test --prefix tests/browser
```

To use an installed Chrome instead, set `MDSERVE_CHROME` to its executable.
Tests build a temporary binary and use disposable dummy documents, isolated
browser profiles, and local listeners. IPv6 loopback must be available.
`hosts.cjs` checks actual browser rendering, IPv4/IPv6/wildcard binding,
busy-port fallback, same-origin fetch, and unauthorized Host/Origin/methods.
These exercise DNS-rebinding prerequisites; they do not simulate a complete attack.
