package web

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
)

var inlineScripts = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// ContentPolicy hashes only application inline scripts. Untrusted document HTML
// never enters an executable script context. Inline styles support KaTeX/Mermaid;
// inline scripts and eval are intentionally absent from this policy.
func ContentPolicy(page string) string {
	sources := []string{"'self'"}
	for _, match := range inlineScripts.FindAllStringSubmatch(page, -1) {
		sum := sha256.Sum256([]byte(match[1]))
		sources = append(sources, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return "default-src 'none'; script-src " + strings.Join(sources, " ") + "; style-src 'self' 'unsafe-inline'; img-src 'self' data: http: https:; font-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"
}

// ProtectStatic adds the policy before any active elements are parsed. Frame
// ancestry is enforced by live response headers; CSP meta cannot enforce it.
func ProtectStatic(page string) string {
	return strings.Replace(page, `<meta charset="utf-8">`, `<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="`+ContentPolicy(page)+`">`, 1)
}
