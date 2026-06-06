package portalapi

import (
	"net/http"
	"strings"
)

// Swagger UI and Redoc are served as static HTML that loads the latest
// release of each library from a public CDN and points it at the
// embedded OpenAPI spec at /openapi.yaml. No server-side state, no
// build-time codegen — Just Two Files.
//
// For air-gapped deployments where the CDN isn't reachable, bundle the
// Swagger UI assets via `go:embed` instead. That migration is small
// enough to be a one-line change here when needed.

const swaggerUIHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>FDH Portal API — Swagger UI</title>
    <link
      rel="stylesheet"
      href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css"
    />
    <style>
      body { margin: 0; background: #fafafa; }
      .topbar { display: none; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
    <script>
      window.onload = function () {
        window.ui = SwaggerUIBundle({
          url: "/openapi.yaml",
          dom_id: "#swagger-ui",
          deepLinking: true,
          presets: [
            SwaggerUIBundle.presets.apis,
            SwaggerUIStandalonePreset
          ],
          plugins: [SwaggerUIBundle.plugins.DownloadUrl],
          layout: "StandaloneLayout",
          // Try-it-out works against the same host that serves this page.
          tryItOutEnabled: true,
          // Don't validate against swagger.io's online validator — we're
          // an internal product and may run behind firewalls.
          validatorUrl: null
        });
      };
    </script>
  </body>
</html>
`

const redocHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>FDH Portal API — Redoc</title>
    <link
      href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700"
      rel="stylesheet"
    />
    <style>body { margin: 0; padding: 0; }</style>
  </head>
  <body>
    <redoc spec-url="/openapi.yaml" hide-loading></redoc>
    <script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"></script>
  </body>
</html>
`

const docsIndexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>FDH Portal API — Documentation</title>
    <style>
      body {
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto,
          sans-serif;
        max-width: 720px;
        margin: 4rem auto;
        padding: 0 1rem;
        color: #0f172a;
        line-height: 1.5;
      }
      h1 { font-size: 1.875rem; margin: 0 0 0.5rem 0; }
      p { color: #475569; }
      ul { padding-left: 1.5rem; }
      li { margin-bottom: 0.5rem; }
      a {
        color: #2563eb;
        text-decoration: none;
        font-weight: 500;
      }
      a:hover { text-decoration: underline; }
      code {
        background: #f1f5f9;
        padding: 0.125rem 0.375rem;
        border-radius: 0.25rem;
        font-size: 0.875rem;
      }
      .card {
        border: 1px solid #e2e8f0;
        border-radius: 0.5rem;
        padding: 1.25rem;
        margin: 1rem 0;
      }
      .muted { color: #64748b; font-size: 0.875rem; }
    </style>
  </head>
  <body>
    <h1>FDH Portal API</h1>
    <p class="muted">
      JSON HTTP API that backs the forge Development Hub portal.
      Pick the documentation viewer you prefer.
    </p>

    <div class="card">
      <h2>Swagger UI</h2>
      <p>
        Interactive — fold endpoints, try requests against this server
        directly from the browser.
      </p>
      <p><a href="/api/docs/swagger">Open Swagger UI →</a></p>
    </div>

    <div class="card">
      <h2>Redoc</h2>
      <p>
        Reference-style — one long scrollable page, optimized for reading
        the spec end-to-end.
      </p>
      <p><a href="/api/docs/redoc">Open Redoc →</a></p>
    </div>

    <div class="card">
      <h2>Raw OpenAPI spec</h2>
      <p>
        The OpenAPI 3.1 YAML document. Feed it to any compatible tool
        (codegen, Postman import, etc.).
      </p>
      <p><a href="/openapi.yaml"><code>GET /openapi.yaml</code> →</a></p>
    </div>

    <p class="muted" style="margin-top: 2rem;">
      Need the portal UI instead? Visit
      <a href="/">the portal home</a>.
    </p>
  </body>
</html>
`

// handleDocsIndex serves the docs landing page that links to the two UIs.
func (s *Server) handleDocsIndex(w http.ResponseWriter, r *http.Request) {
	// Treat exact /api/docs and /api/docs/ the same as the index.
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/api/docs":
		// fall through to render the index
	case "/api/docs/swagger":
		s.serveHTML(w, swaggerUIHTML)
		return
	case "/api/docs/redoc":
		s.serveHTML(w, redocHTML)
		return
	default:
		http.NotFound(w, r)
		return
	}
	s.serveHTML(w, docsIndexHTML)
}

// handleRedoc serves Redoc at the convenience top-level /redoc path.
func (s *Server) handleRedoc(w http.ResponseWriter, r *http.Request) {
	s.serveHTML(w, redocHTML)
}

func (s *Server) serveHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Short cache — the HTML is static but evolves with the OpenAPI spec.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}
