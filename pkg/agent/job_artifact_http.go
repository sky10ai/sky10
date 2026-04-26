package agent

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (s *JobStore) HandleArtifactDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobID := strings.TrimSpace(r.URL.Query().Get("job_id"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if jobID == "" || key == "" {
		http.Error(w, "job_id and key are required", http.StatusBadRequest)
		return
	}
	result, err := s.Get(r.Context(), jobID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	job := result.Job
	ref, ok := findJobResultRef(job, key)
	if !ok {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	path, err := artifactLocalPath(ref)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := requireArtifactPathInOutputDir(job, path); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "artifact not found", http.StatusNotFound)
			return
		}
		http.Error(w, "open artifact", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "stat artifact", http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		http.Error(w, "artifact is a directory", http.StatusBadRequest)
		return
	}
	contentType := strings.TrimSpace(ref.MimeType)
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(path))
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	filename := filepath.Base(path)
	if filename != "." && filename != string(filepath.Separator) {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	}
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func findJobResultRef(job AgentJob, key string) (AgentPayloadRef, bool) {
	for _, ref := range job.ResultRefs {
		if strings.TrimSpace(ref.Key) == key {
			return ref, true
		}
	}
	return AgentPayloadRef{}, false
}

func artifactLocalPath(ref AgentPayloadRef) (string, error) {
	rawURI := strings.TrimSpace(ref.URI)
	if rawURI == "" {
		return "", fmt.Errorf("artifact URI is empty")
	}
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("artifact URI is invalid")
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("artifact URI scheme %q is not downloadable", parsed.Scheme)
	}
	path := parsed.Path
	if path == "" {
		path = strings.TrimPrefix(rawURI, "file://")
	}
	if path == "" {
		return "", fmt.Errorf("artifact path is empty")
	}
	return filepath.Clean(path), nil
}

func requireArtifactPathInOutputDir(job AgentJob, artifactPath string) error {
	outputDir := strings.TrimSpace(job.OutputDir)
	if outputDir == "" {
		return nil
	}
	base := filepath.Clean(outputDir)
	path := filepath.Clean(artifactPath)
	if resolvedBase, err := filepath.EvalSymlinks(base); err == nil {
		base = resolvedBase
	}
	if resolvedPath, err := filepath.EvalSymlinks(path); err == nil {
		path = resolvedPath
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return fmt.Errorf("artifact path is outside the job output directory")
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
		return nil
	}
	return fmt.Errorf("artifact path is outside the job output directory")
}
