package api

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const adminPlaceholderMarker = "The admin UI has not been built into this binary."

func adminFileHandler(embedded embed.FS) http.Handler {
	return adminFileHandlerWithLocalDist(embedded, filepath.Join("web", "admin", "dist"))
}

func adminFileHandlerWithLocalDist(embedded embed.FS, localDist string) http.Handler {
	subtree, err := fs.Sub(embedded, "admin_dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	if shouldUseLocalAdminDist(subtree, localDist) {
		return http.StripPrefix("/admin/", spaFileServer(os.DirFS(localDist), http.FileServer(http.Dir(localDist))))
	}

	fileServer := http.FileServer(http.FS(subtree))
	return http.StripPrefix("/admin/", spaFileServer(subtree, fileServer))
}

func shouldUseLocalAdminDist(embedded fs.FS, localDist string) bool {
	if !isLocalAdminSourceTree(localDist) {
		return false
	}
	if _, err := os.Stat(filepath.Join(localDist, "index.html")); err != nil {
		return false
	}
	index, err := fs.ReadFile(embedded, "index.html")
	if err != nil {
		return false
	}

	return bytes.Contains(index, []byte(adminPlaceholderMarker))
}

func isLocalAdminSourceTree(localDist string) bool {
	if filepath.Base(filepath.Clean(localDist)) != "dist" {
		return false
	}
	adminDir := filepath.Dir(localDist)
	if !hasMuxMailAdminPackage(filepath.Join(adminDir, "package.json")) {
		return false
	}
	if _, err := os.Stat(filepath.Join(adminDir, "vite.config.ts")); err != nil {
		return false
	}

	return true
}

func hasMuxMailAdminPackage(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}

	return manifest.Name == "@muxmail/admin"
}

func spaFileServer(files fs.FS, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		setAdminSecurityHeaders(w)
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			http.NotFound(w, request)
			return
		}

		path := strings.TrimPrefix(request.URL.Path, "/")
		if path == "" {
			serveSPAIndex(w, request, files)
			return
		}
		if !fs.ValidPath(path) || strings.ContainsAny(path, "\\:") || strings.Contains(path, "%") {
			http.NotFound(w, request)
			return
		}
		if strings.HasPrefix(path, "assets/") && strings.HasSuffix(path, "/") {
			http.NotFound(w, request)
			return
		}
		if path == "index.html" {
			serveSPAIndex(w, request, files)
			return
		}
		if strings.HasSuffix(path, "/") {
			serveSPAIndex(w, request, files)
			return
		}
		if info, err := fs.Stat(files, path); err == nil {
			if info.IsDir() {
				http.NotFound(w, request)
				return
			}
			fallback.ServeHTTP(w, request)
			return
		}
		if strings.HasPrefix(path, "assets/") {
			http.NotFound(w, request)
			return
		}

		serveSPAIndex(w, request, files)
	})
}

func setAdminSecurityHeaders(w http.ResponseWriter) {
	headers := w.Header()
	headers.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("X-Frame-Options", "DENY")
}

func serveSPAIndex(w http.ResponseWriter, request *http.Request, files fs.FS) {
	data, err := fs.ReadFile(files, "index.html")
	if err != nil {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
