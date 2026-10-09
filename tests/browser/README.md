# Browser regressions

Development only; the Go binary has no Node or network dependency.

```
npm ci --prefix tests/browser
cd tests/browser && npx playwright install chromium && cd ../..
npm test --prefix tests/browser
```

To use an installed Chrome instead, set `MDSERVE_CHROME` to its executable.
Tests build a temporary binary and use disposable dummy documents, isolated
browser profiles, and local listeners. IPv6 loopback must be available.
`hosts.cjs` checks actual browser rendering, IPv4/IPv6/wildcard binding,
busy-port fallback, same-origin fetch, and unauthorized Host/Origin/methods.
These exercise DNS-rebinding prerequisites; they do not simulate a complete attack.

`security.cjs` checks live/static HTML and SVG payloads, event handlers, unsafe
links, iframe/object/style/form content, filename injection, attempted dummy
neighbor-document exfiltration to a controlled local endpoint, strict Mermaid,
and CSP enforcement. It removes CSP in a separate test to prove sanitization
independently. Compatibility covers headings, links, local images, tables,
checkboxes, highlighting, diagrams, math, themes, RTL paragraphs and LTR islands;
static pages are also loaded via file://. Set MDSERVE_KEEP_ARTIFACTS=1 to retain
dummy-only fixtures and screenshots at the printed temporary path.

`vendor.cjs` runs isolated browser processes with a 15-second parent deadline.
It checks that all-days Gantt exclusions terminate and KaTeX ignores inherited
`trust` options. Deadline failures kill the disposable browser process group on
POSIX (or its process tree on Windows). These reproduce vendor advisories without
using private data or external endpoints.

CI installs locked development dependencies and Playwright Chromium on Linux;
Go regressions run separately on Linux, macOS and Windows. Hosted workflow results
are distinct from local tests and must be checked before merging.
