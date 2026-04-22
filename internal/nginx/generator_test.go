package nginx

import (
	"strings"
	"testing"
)

func TestRenderConfigFallbackWhenNoDemos(t *testing.T) {
	rendered, err := renderConfig(nil, nil)
	if err != nil {
		t.Fatalf("renderConfig returned error: %v", err)
	}

	if !strings.Contains(rendered, "listen 80 default_server") {
		t.Fatal("expected fallback config to listen on the default HTTP server")
	}
	if !strings.Contains(rendered, "location /.well-known/acme-challenge/") {
		t.Fatal("expected fallback config to keep ACME challenge routing")
	}
	if !strings.Contains(rendered, "return 404;") {
		t.Fatal("expected fallback config to return 404 for unmatched requests")
	}
}

func TestRenderConfigForwardsHTTPRequestsToDemoUpstream(t *testing.T) {
	rendered, err := renderConfig([]DemoEntry{{
		Name:      "demo-1",
		Subdomain: "demo-1",
		IP:        "10.0.3.2",
		Port:      "80",
	}}, nil)
	if err != nil {
		t.Fatalf("renderConfig returned error: %v", err)
	}

	assertContainsAll(t, rendered,
		"upstream demo-1 {",
		"server 10.0.3.2:80;",
		"server_name demo-1.iiab.io;",
		"proxy_pass http://demo-1;",
		"proxy_set_header Host $host;",
		"proxy_set_header X-Real-IP $remote_addr;",
		"proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
	)

	if strings.Contains(rendered, "return 301 https://$host$request_uri;") {
		t.Fatal("expected non-SSL demo to proxy HTTP directly instead of redirecting")
	}
}

func TestRenderConfigRedirectsHTTPAndForwardsHTTPSForSSLDemo(t *testing.T) {
	rendered, err := renderConfig([]DemoEntry{{
		Name:      "secure-demo",
		Subdomain: "secure-demo",
		IP:        "10.0.3.9",
		Port:      "80",
		HasSSL:    true,
	}}, nil)
	if err != nil {
		t.Fatalf("renderConfig returned error: %v", err)
	}

	assertContainsAll(t, rendered,
		"server_name secure-demo.iiab.io;",
		"return 301 https://$host$request_uri;",
		"listen 443 ssl;",
		"ssl_certificate /etc/letsencrypt/live/secure-demo.iiab.io/fullchain.pem;",
		"ssl_certificate_key /etc/letsencrypt/live/secure-demo.iiab.io/privkey.pem;",
		"proxy_pass http://secure-demo;",
		"proxy_set_header Host $host;",
		"proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;",
		"proxy_set_header X-Forwarded-Proto $scheme;",
		"proxy_set_header X-Forwarded-Host $host;",
		"proxy_http_version 1.1;",
		"proxy_set_header Upgrade $http_upgrade;",
		`proxy_set_header Connection "upgrade";`,
	)
}

func TestRenderConfigAddsWildcardFallbackRedirectWhenWildcardHasSSL(t *testing.T) {
	wildcard := DemoEntry{
		Name:      "wildcard-demo",
		Subdomain: "wildcard-demo",
		IP:        "10.0.3.7",
		Port:      "80",
		HasSSL:    true,
		Wildcard:  true,
	}

	rendered, err := renderConfig([]DemoEntry{wildcard}, &wildcard)
	if err != nil {
		t.Fatalf("renderConfig returned error: %v", err)
	}

	assertContainsAll(t, rendered,
		"listen 80 default_server;",
		"return 302 https://wildcard-demo.iiab.io$request_uri;",
		"listen 443 ssl default_server;",
		"ssl_certificate /etc/letsencrypt/live/wildcard-demo.iiab.io/fullchain.pem;",
	)
}

func assertContainsAll(t *testing.T, rendered string, want ...string) {
	t.Helper()

	for _, needle := range want {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected rendered config to contain %q\n\n%s", needle, rendered)
		}
	}
}
