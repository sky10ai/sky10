package fs

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// HandleUpload handles multipart file uploads to a drive.
// POST /upload?drive=X&path=Y
func (s *FSHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		return
	}

	driveName := r.URL.Query().Get("drive")
	filePath := r.URL.Query().Get("path")
	if driveName == "" || filePath == "" {
		http.Error(w, "drive and path query params required", http.StatusBadRequest)
		return
	}

	drive := s.findDrive(driveName)
	if drive == nil {
		http.Error(w, fmt.Sprintf("drive %q not found", driveName), http.StatusNotFound)
		return
	}

	target := filepath.Join(drive.LocalPath, filepath.Clean(filePath))
	if !filepath.HasPrefix(target, drive.LocalPath) {
		http.Error(w, "path escapes drive root", http.StatusBadRequest)
		return
	}

	// Parse multipart — 32MB max in memory, rest to disk.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		http.Error(w, "creating directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	out, err := os.Create(target)
	if err != nil {
		http.Error(w, "creating file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	n, err := io.Copy(out, file)
	out.Close()
	if err != nil {
		os.Remove(target)
		http.Error(w, "writing file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.server.Emit("file.changed", map[string]string{
		"drive": driveName, "path": filePath, "type": "put",
	})
	s.logger.Info("file uploaded via HTTP", "drive", driveName, "path", filePath, "size", n)

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","size":%d}`, n)
}

// HandleDownload serves a file from a drive's local path.
// GET /download?drive=X&path=Y
func (s *FSHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method == http.MethodOptions {
		return
	}

	driveName := r.URL.Query().Get("drive")
	filePath := r.URL.Query().Get("path")
	if driveName == "" || filePath == "" {
		http.Error(w, "drive and path query params required", http.StatusBadRequest)
		return
	}

	drive := s.findDrive(driveName)
	if drive == nil {
		http.Error(w, fmt.Sprintf("drive %q not found", driveName), http.StatusNotFound)
		return
	}

	target := filepath.Join(drive.LocalPath, filepath.Clean(filePath))
	if !filepath.HasPrefix(target, drive.LocalPath) {
		http.Error(w, "path escapes drive root", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "cannot download a directory", http.StatusBadRequest)
		return
	}

	filename := filepath.Base(filePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, target)
}
