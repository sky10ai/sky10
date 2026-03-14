package skyfs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Index is a local SQLite cache of remote file state and sync metadata.
// It enables fast queries without hitting S3 on every operation.
type Index struct {
	db *sql.DB
}

// OpenIndex opens or creates a local SQLite index at the given path.
func OpenIndex(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Index{db: db}, nil
}

func initSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS remote_files (
		path TEXT PRIMARY KEY,
		chunks TEXT,
		size INTEGER,
		modified TEXT,
		checksum TEXT,
		namespace TEXT
	);
	CREATE TABLE IF NOT EXISTS chunks (
		hash TEXT PRIMARY KEY,
		location TEXT,
		size INTEGER,
		cached BOOLEAN DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT
	);`

	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	return nil
}

// Close closes the index database.
func (idx *Index) Close() error {
	return idx.db.Close()
}

// SyncFromManifest updates the local index from a manifest.
// This replaces all remote_files entries with the manifest contents.
func (idx *Index) SyncFromManifest(m *Manifest) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM remote_files"); err != nil {
		return fmt.Errorf("clearing remote_files: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO remote_files (path, chunks, size, modified, checksum, namespace)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing insert: %w", err)
	}
	defer stmt.Close()

	for path, entry := range m.Tree {
		chunksJSON, _ := json.Marshal(entry.Chunks)
		_, err := stmt.Exec(path, string(chunksJSON), entry.Size,
			entry.Modified.Format("2006-01-02T15:04:05Z"), entry.Checksum, entry.Namespace)
		if err != nil {
			return fmt.Errorf("inserting %s: %w", path, err)
		}
	}

	return tx.Commit()
}

// ListFiles returns file entries matching the prefix from the local index.
func (idx *Index) ListFiles(prefix string) ([]ManifestEntry, error) {
	rows, err := idx.db.Query(
		`SELECT path, chunks, size, modified, checksum, namespace
		 FROM remote_files WHERE path LIKE ? ORDER BY path`,
		prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("querying files: %w", err)
	}
	defer rows.Close()

	var entries []ManifestEntry
	for rows.Next() {
		var path, chunksJSON, modified, checksum, namespace string
		var size int64
		if err := rows.Scan(&path, &chunksJSON, &size, &modified, &checksum, &namespace); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		var chunks []string
		json.Unmarshal([]byte(chunksJSON), &chunks)

		entries = append(entries, ManifestEntry{
			Path: path,
			FileEntry: FileEntry{
				Chunks:    chunks,
				Size:      size,
				Checksum:  checksum,
				Namespace: namespace,
			},
		})
	}

	return entries, rows.Err()
}

// LookupFile returns a single file entry by exact path.
func (idx *Index) LookupFile(path string) (*FileEntry, error) {
	var chunksJSON, modified, checksum, namespace string
	var size int64

	err := idx.db.QueryRow(
		`SELECT chunks, size, modified, checksum, namespace FROM remote_files WHERE path = ?`,
		path).Scan(&chunksJSON, &size, &modified, &checksum, &namespace)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("looking up %s: %w", path, err)
	}

	var chunks []string
	json.Unmarshal([]byte(chunksJSON), &chunks)

	return &FileEntry{
		Chunks:    chunks,
		Size:      size,
		Checksum:  checksum,
		Namespace: namespace,
	}, nil
}

// FileCount returns the number of files in the index.
func (idx *Index) FileCount() (int, error) {
	var count int
	err := idx.db.QueryRow("SELECT COUNT(*) FROM remote_files").Scan(&count)
	return count, err
}

// SetState stores a key-value pair in the sync state table.
func (idx *Index) SetState(key, value string) error {
	_, err := idx.db.Exec(
		`INSERT OR REPLACE INTO sync_state (key, value) VALUES (?, ?)`,
		key, value)
	return err
}

// GetState retrieves a value from the sync state table.
func (idx *Index) GetState(key string) (string, error) {
	var value string
	err := idx.db.QueryRow(
		`SELECT value FROM sync_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SearchFiles returns files matching a search query (path contains query).
func (idx *Index) SearchFiles(query string) ([]ManifestEntry, error) {
	return idx.ListFiles(strings.ReplaceAll(query, "*", "%"))
}
