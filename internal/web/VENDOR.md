# Embedded vendor inventory and security review

## Sanitizer provenance

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

## Diagram and math updates

The advisory review on 2026-10-09 replaced Mermaid 10.9.1 with
[10.9.8](https://github.com/mermaid-js/mermaid/releases/tag/v10.9.8), and
standalone KaTeX 0.16.10 with
[0.18.2](https://github.com/KaTeX/KaTeX/releases/tag/v0.18.2).
Mermaid remains a single embedded browser bundle; no runtime imports or CDN were
added. KaTeX JS, CSS and auto-render are from the same release. Its woff2 fonts
were compared/copied from that release and are byte-identical to the existing
fonts. Original font subsets and highlighting styles were retained.

Reproduce with `npm pack mermaid@10.9.8 katex@0.18.2 --ignore-scripts` in a
temporary directory; extract each package separately. Copy Mermaid's
`dist/mermaid.min.js`; copy KaTeX's `dist/katex.min.js`, `dist/katex.min.css`,
`dist/contrib/auto-render.min.js` and `dist/fonts/*.woff2`. Keep upstream bytes
unchanged. Mermaid's MIT license is from its tagged upstream `LICENSE` and
KaTeX's MIT license is from the npm package, stored alongside the bundles.
The targeted `.gitattributes` whitespace setting preserves trailing spaces
inside Mermaid's minified template literals.

| Artifact | SHA-256 |
| --- | --- |
| mermaid-10.9.8.tgz | `d41f5a1593dcca5a04d44618089b72dbfb2b3b750bd5c3f20927579312c6d692` |
| mermaid.min.js | `8d607d7ef1d077a8aa202e18e62212bfa992c68bfeabc5cf45d51a128fe6675d` |
| katex-0.18.2.tgz | `a71df261a7080b53a4be44ea1b40e8e73e84cdd5d3311239310568e76437c018` |
| katex.min.js | `90354db602936d813daadda391a4376d071f5d1241d8cfddf091df4c2e38730d` |
| katex.min.css | `7dc91b92608db5f3523cfda520df36307b2b389e1725d71ace6e4ef8dee066c7` |
| auto-render.min.js | `e5372d199bcdae8b4de71d0f7ceba72a4ba12774a27c60a6f1f77d03b3228ee4` |

## Dated advisory review — 2026-10-09

| Library | Embedded version | Review result |
| --- | --- | --- |
| marked | 12.0.2 (banner) | Upstream's older ReDoS advisories concern earlier releases; GHSA-6v9c-7cg6-27q7 lists 18.0.0/18.0.1. HTML output still requires sanitization. |
| highlight.js | 11.9.0 (banner) | Upstream's published prototype-pollution/grammar ReDoS advisories concern older versions; its security policy supports 11.x. |
| DOMPurify (reader) | 3.4.16 | Includes patches through the September 23 advisories. Reader sanitizes strings to detached fragments without IN_PLACE or custom hooks. |
| Mermaid | 10.9.8 (pinned npm artifact) | Includes v10 patches for published Gantt DoS, config/classDef CSS and state-label HTML injection, bundled sanitizer and configuration prototype-pollution advisories. |
| KaTeX (reader) | 0.18.2 (runtime version) | Includes the htmlData attribute and inherited-option fixes. Earlier expansion and URL fixes are retained. `trust:false` remains explicit. |
| Mermaid-internal DOMPurify | 3.4.2 (bundle literal) | Later published advisories cover IN_PLACE, hooks, custom-element and mutable policy modes. Mermaid uses string sanitization without these application-controlled modes; the reader additionally sanitizes diagram output with 3.4.16. This is a scoped assessment, not a clean transitive audit. |
| Mermaid-internal KaTeX | 0.16.45 (bundle literal) | Newer inherited-option advisory still lists this version. The renderer supplies trusted configuration; a prerequisite prototype pollution was not demonstrated. The reader's separate math renderer is patched to 0.18.2. |

Primary review sources:

- [Mermaid advisory list](https://github.com/mermaid-js/mermaid/security/advisories),
  including [Gantt DoS](https://github.com/mermaid-js/mermaid/security/advisories/GHSA-6m6c-36f7-fhxh),
  [CSS injection](https://github.com/mermaid-js/mermaid/security/advisories/GHSA-xcj9-5m2h-648r),
  [bundled DOMPurify](https://github.com/mermaid-js/mermaid/security/advisories/GHSA-m4gq-x24j-jpmf)
  and [config APIs](https://github.com/mermaid-js/mermaid/security/advisories/GHSA-c4c3-pg64-4m4v).
- [KaTeX advisory list](https://github.com/KaTeX/KaTeX/security/advisories),
  [htmlData](https://github.com/KaTeX/KaTeX/security/advisories/GHSA-cg87-wmx4-v546)
  and [inherited options](https://github.com/KaTeX/KaTeX/security/advisories/GHSA-238p-pmpm-9mq7).
- [DOMPurify advisory list](https://github.com/cure53/DOMPurify/security/advisories)
  and its public GitHub advisory API (all 25 published records reviewed).
- [marked advisory list](https://github.com/markedjs/marked/security/advisories)
  and [highlight.js security policy/advisories](https://github.com/highlightjs/highlight.js/security).

The Gantt and inherited-trust probes fail against the original bundles and pass
against these updates. Run `npm test --prefix tests/browser` when updating;
full live/static regressions verify sanitizer protection independently of CSP,
diagrams/math/images, offline local resources and mixed RTL/LTR content.

This review checks published upstream advisories and selected bundle call sites,
not every bundled/transitive dependency or every possible input. Original marked,
highlight.js and font provenance was inherited from upstream rather than freshly
reproduced. No released-binary, font-parser or complete source audit is claimed.
Rendering remains on the browser main thread; arbitrary resource exhaustion is
not prevented. Recheck upstream advisories before future releases.
