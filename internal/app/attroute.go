package app

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"awesome-reminder/internal/core"
)

const attPrefix = "/att/"

// attPather resolves an attachment ID to its file on disk.
type attPather interface {
	AttachmentPath(id string) (string, error)
}

// attMiddleware serves GET /att/<id> from the attachment store and passes
// every other request to the frontend assets. Unknown or malformed IDs get 404.
func attMiddleware(c attPather) application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := strings.CutPrefix(r.URL.Path, attPrefix)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
				http.NotFound(w, r)
				return
			}
			path, err := c.AttachmentPath(id)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			data, err := os.ReadFile(path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			h := w.Header()
			h.Set("Content-Type", "image/webp")
			h.Set("Cache-Control", "private, max-age=31536000, immutable")
			h.Set("X-Content-Type-Options", "nosniff")
			w.Write(data)
		})
	}
}

// sweepAttachments removes unreferenced attachment files once at startup.
func sweepAttachments(c *core.Core) {
	n, err := c.SweepAttachments()
	log.Printf("attachments: swept %d", n)
	if err != nil {
		log.Printf("attachments sweep: %v", err)
	}
}
