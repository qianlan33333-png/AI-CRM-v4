package webshell

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const adminRuntimeAssetPrefix = "/assets/"

var contentHashedAssetName = regexp.MustCompile(`-[A-Z0-9]{8}\.(?:css|js|mjs)$`)

// RuntimeAssetsHandler serves only content-hashed frontend build outputs with
// browser-private immutable caching. Every other /assets request remains with
// the compatibility handler so legacy, unhashed files keep their no-store
// contract until they join the manifest-backed build.
type RuntimeAssetsHandler struct {
	dist     string
	assets   map[string]runtimeAsset
	fallback http.Handler
}

type runtimeAsset struct {
	sha256 string
	etag   string
}

type runtimeAssetManifest struct {
	Files map[string]struct {
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// NewRuntimeAssetsHandler reads and verifies the release manifest when the
// process is composed. An incomplete manifest deliberately leaves all requests
// on the supplied compatibility handler instead of guessing which files are
// safe to cache for a year.
func NewRuntimeAssetsHandler(dist string, fallback http.Handler) http.Handler {
	handler := &RuntimeAssetsHandler{dist: dist, assets: map[string]runtimeAsset{}, fallback: fallback}
	if fallback == nil {
		handler.fallback = http.NotFoundHandler()
	}
	handler.loadManifest()
	return handler
}

func (handler *RuntimeAssetsHandler) loadManifest() {
	if handler == nil || strings.TrimSpace(handler.dist) == "" {
		return
	}
	file, err := os.Open(filepath.Join(handler.dist, "asset-manifest.json"))
	if err != nil {
		return
	}
	defer file.Close()
	var manifest runtimeAssetManifest
	if json.NewDecoder(io.LimitReader(file, 4<<20)).Decode(&manifest) != nil {
		return
	}
	for releasePath, metadata := range manifest.Files {
		if !validHashedRuntimeAsset(releasePath, metadata.SHA256) {
			continue
		}
		if !runtimeAssetMatches(filepath.Join(handler.dist, filepath.FromSlash(releasePath)), metadata.SHA256) {
			continue
		}
		handler.assets[releasePath] = runtimeAsset{sha256: metadata.SHA256, etag: `"` + metadata.SHA256 + `"`}
	}
}

func validHashedRuntimeAsset(releasePath, sha256 string) bool {
	if !strings.HasPrefix(releasePath, "assets/") || strings.Contains(releasePath, "\\") || path.Clean(releasePath) != releasePath {
		return false
	}
	if !contentHashedAssetName.MatchString(path.Base(releasePath)) {
		return false
	}
	if len(sha256) != 64 {
		return false
	}
	for _, value := range sha256 {
		if !(value >= '0' && value <= '9' || value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}

func (handler *RuntimeAssetsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil {
		http.NotFound(writer, request)
		return
	}
	if request == nil || (request.Method != http.MethodGet && request.Method != http.MethodHead) {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	releasePath, asset, ok := handler.assetFor(request.URL.Path)
	if !ok {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	file, err := os.Open(filepath.Join(handler.dist, filepath.FromSlash(releasePath)))
	if err != nil {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	if !runtimeAssetMatchesOpenFile(file, asset.sha256) {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	if contentType == "" {
		handler.fallback.ServeHTTP(writer, request)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	writer.Header().Set("ETag", asset.etag)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(cacheSafeResponseWriter{ResponseWriter: writer}, request, info.Name(), info.ModTime(), file)
}

// cacheSafeResponseWriter prevents conditional and range errors from retaining
// a cache policy selected before http.ServeContent evaluates those conditions.
// A 304 is a successful cached representation and deliberately retains them.
type cacheSafeResponseWriter struct{ http.ResponseWriter }

func (writer cacheSafeResponseWriter) WriteHeader(status int) {
	if status != http.StatusOK && status != http.StatusPartialContent && status != http.StatusNotModified {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Del("ETag")
	}
	writer.ResponseWriter.WriteHeader(status)
}

func runtimeAssetMatches(filename, expectedSHA256 string) bool {
	file, err := os.Open(filename)
	if err != nil {
		return false
	}
	defer file.Close()
	return runtimeAssetMatchesOpenFile(file, expectedSHA256)
}

func runtimeAssetMatchesOpenFile(file *os.File, expectedSHA256 string) bool {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false
	}
	return hex.EncodeToString(hash.Sum(nil)) == expectedSHA256
}

func (handler *RuntimeAssetsHandler) assetFor(requestPath string) (string, runtimeAsset, bool) {
	if handler == nil || !strings.HasPrefix(requestPath, adminRuntimeAssetPrefix) {
		return "", runtimeAsset{}, false
	}
	relative := strings.TrimPrefix(requestPath, adminRuntimeAssetPrefix)
	if relative == "" || strings.Contains(relative, "\\") || path.Clean(relative) != relative {
		return "", runtimeAsset{}, false
	}
	asset, ok := handler.assets["assets/"+relative]
	return "assets/" + relative, asset, ok
}
