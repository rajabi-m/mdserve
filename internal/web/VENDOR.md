# Sanitizer provenance

DOMPurify 3.4.16, upstream https://github.com/cure53/DOMPurify,
release https://github.com/cure53/DOMPurify/releases/tag/3.4.16 (2026-09-23).
Checked upstream release and npm version on 2026-10-09. Vendored unmodified
`dist/purify.min.js` as `assets/purify.min.js`, with Apache-2.0 license in
`assets/DOMPurify-LICENSE` (upstream also offers MPL-2.0). Its source-map comment
is retained; the development source map is not shipped.

Reproduce from the published npm package in a temporary directory:

```
npm pack dompurify@3.4.16 --pack-destination /tmp
mkdir -p /tmp/mdserve-dompurify
tar -xzf /tmp/dompurify-3.4.16.tgz -C /tmp/mdserve-dompurify
cp /tmp/mdserve-dompurify/package/dist/purify.min.js internal/web/assets/purify.min.js
cp /tmp/mdserve-dompurify/package/LICENSE internal/web/assets/DOMPurify-LICENSE
shasum -a 256 /tmp/dompurify-3.4.16.tgz internal/web/assets/purify.min.js
```

SHA-256:

- npm tarball: `d288ced25e6bda3e5fc76d1fc9bca899260cf886e005e84f232e686ada57f1b4`
- vendored JS: `2c90a9b46d6463f26038a29b686e82bc91de01fdac9d5229e7cfe3b360134ea2`

These identify the downloaded bytes; they are not independent authentication.
Review upstream security advisories when updating. Run Go and browser regressions
including sanitization with CSP removed. `reader-security.js` is project code,
not part of the upstream library. HTML uses the HTML-only profile; diagrams use
strict Mermaid followed by the SVG profile. Do not permit unsafe URL schemes or
attach document-specified callbacks. The Go renderer produces untrusted HTML;
static exports encode it as HTML-safe JSON until the browser sanitizer runs.
