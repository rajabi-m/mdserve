package web

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

func TestContentPolicyHashesApplicationScripts(t *testing.T) {
	script := "window.test = true;"
	page := `<script src="local.js"></script><script>` + script + `</script><script type="application/json">"untrusted"</script>`
	sum := sha256.Sum256([]byte(script))
	csp := ContentPolicy(page)
	if !strings.Contains(csp, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Fatal("application hash missing")
	}
	if strings.Count(csp, "'sha256-") != 1 {
		t.Fatal("hashed inert/external scripts")
	}
}
