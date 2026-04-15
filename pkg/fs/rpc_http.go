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
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(driveDataDir(drive.ID)), "upload-*")
	if err != nil {
		http.Error(w, "creating staging file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	session, err := newTransferSession(transferSessionsDir(driveDataDir(drive.ID)), "upload", tmpPath, target)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		http.Error(w, "creating transfer session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	n, err := io.Copy(tmpFile, file)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		session.remove()
		http.Error(w, "writing file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		session.remove()
		http.Error(w, "closing upload temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := session.markStaged(); err != nil {
		os.Remove(tmpPath)
		session.remove()
		http.Error(w, "marking upload staged: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := publishStagedFile(tmpPath, target); err != nil {
		http.Error(w, "publishing upload: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := session.remove(); err != nil {
		s.logger.Warn("upload transfer session cleanup failed", "drive", driveName, "path", filePath, "error", err)
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
