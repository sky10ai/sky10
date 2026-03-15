package fs

import "io"

// ProgressFunc is called with transfer progress updates.
type ProgressFunc func(bytesTransferred, totalBytes int64)

// ProgressReader wraps an io.Reader and reports progress.
type ProgressReader struct {
	r           io.Reader
	total       int64
	transferred int64
	onProgress  ProgressFunc
}

// NewProgressReader wraps r and calls onProgress after each Read.
func NewProgressReader(r io.Reader, total int64, onProgress ProgressFunc) *ProgressReader {
	return &ProgressReader{
		r:          r,
		total:      total,
		onProgress: onProgress,
	}
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.transferred += int64(n)
	if pr.onProgress != nil && n > 0 {
		pr.onProgress(pr.transferred, pr.total)
	}
	return n, err
}

// ProgressWriter wraps an io.Writer and reports progress.
type ProgressWriter struct {
	w           io.Writer
	total       int64
	transferred int64
	onProgress  ProgressFunc
}

// NewProgressWriter wraps w and calls onProgress after each Write.
func NewProgressWriter(w io.Writer, total int64, onProgress ProgressFunc) *ProgressWriter {
	return &ProgressWriter{
		w:          w,
		total:      total,
		onProgress: onProgress,
	}
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.transferred += int64(n)
	if pw.onProgress != nil && n > 0 {
		pw.onProgress(pw.transferred, pw.total)
	}
	return n, err
}
