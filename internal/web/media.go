package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/joestump/sigbrowse/internal/ingest"
)

// handleMedia serves an attachment from a conversation's folder in the read-only
// archive. The conversation is keyed by id; the request path is the attachment's
// relative path (e.g. "media/cabin.jpg"). Path traversal is prevented by
// rejecting any cleaned path that escapes the conversation directory.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	conv, err := s.store.GetConversationByID(ctx, id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if conv == nil {
		http.NotFound(w, r)
		return
	}

	rel := r.PathValue("path")
	full, ok := safeMediaPath(s.archiveRoot, conv.Name, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	// Inline images; force download for everything else. The CSP already blocks
	// active content, and nosniff prevents type confusion.
	if isImageExt(full) {
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(full)+"\"")
	}
	// http.ServeContent sets Content-Type from the extension (and sniffs if
	// unknown) and supports range requests.
	http.ServeContent(w, r, filepath.Base(full), info.ModTime(), f)
}

// safeMediaPath resolves a conversation-relative media path to an absolute path
// inside the archive's export/<conversation> directory, returning ok=false if
// the path would escape that directory. It does not follow ".." segments out of
// the conversation folder; legitimate symlinked media dirs inside the folder are
// still served because the lexical containment check is against the conversation
// base, not the symlink target.
func safeMediaPath(archiveRoot, convName, rel string) (string, bool) {
	if archiveRoot == "" || rel == "" {
		return "", false
	}
	base := filepath.Join(archiveRoot, ingest.ExportDir, convName)
	// Clean the relative path and reject absolute or escaping inputs.
	cleanRel := filepath.Clean("/" + strings.TrimPrefix(rel, "/"))
	full := filepath.Join(base, cleanRel)
	relCheck, err := filepath.Rel(base, full)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

// imageExts are the extensions served inline.
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".svg": false, // svg can carry script; never inline
}

func isImageExt(path string) bool {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}
