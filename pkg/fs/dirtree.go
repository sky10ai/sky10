package fs

import (
	"crypto/sha3"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DirHash computes a Merkle tree hash of a directory. Each file contributes
// its name and SHA3-256 content hash. Each directory contributes its name
// and the recursive hash of its children (sorted by name). Empty directories
// contribute their name and a zero hash. The result is deterministic —
// identical content always produces the same hash regardless of filesystem
// ordering or creation time.
//
// The ignore function, if non-nil, filters relative paths (forward-slash
// separated).
//
// Returns the hex-encoded SHA3-256 root hash.
func DirHash(root string, ignore func(string) bool) (string, error) {
	hash, err := dirHashRecursive(root, root, ignore)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", root, err)
	}
	return hash, nil
}

// DirTree computes a Merkle tree of a directory, returning per-path hashes.
// Useful for diffing — compare roots first, then recurse into mismatched
// subtrees to find the divergence.
func DirTree(root string, ignore func(string) bool) (map[string]string, error) {
	tree := make(map[string]string)
	_, err := dirTreeRecursive(root, root, ignore, tree)
	if err != nil {
		return nil, fmt.Errorf("hashing %s: %w", root, err)
	}
	return tree, nil
}

func dirHashRecursive(dir, root string, ignore func(string) bool) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var visible []os.DirEntry
	for _, e := range entries {
		rel := relPath(dir, e.Name(), root)
		if ignore != nil && ignore(rel) {
			continue
		}
		visible = append(visible, e)
	}
	sort.Slice(visible, func(i, j int) bool {
		return visible[i].Name() < visible[j].Name()
	})

	h := sha3.New256()
	for _, e := range visible {
		child := filepath.Join(dir, e.Name())
		if e.IsDir() {
			sub, err := dirHashRecursive(child, root, ignore)
			if err != nil {
				return "", err
			}
			h.Write([]byte(e.Name() + "/"))
			h.Write([]byte(sub))
		} else {
			cksum, err := fileChecksum(child)
			if err != nil {
				return "", err
			}
			h.Write([]byte(e.Name()))
			h.Write([]byte(cksum))
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func dirTreeRecursive(dir, root string, ignore func(string) bool, tree map[string]string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	var visible []os.DirEntry
	for _, e := range entries {
		rel := relPath(dir, e.Name(), root)
		if ignore != nil && ignore(rel) {
			continue
		}
		visible = append(visible, e)
	}
	sort.Slice(visible, func(i, j int) bool {
		return visible[i].Name() < visible[j].Name()
	})

	h := sha3.New256()
	for _, e := range visible {
		child := filepath.Join(dir, e.Name())
		rel := relPath(dir, e.Name(), root)
		if e.IsDir() {
			sub, err := dirTreeRecursive(child, root, ignore, tree)
			if err != nil {
				return "", err
			}
			tree[rel] = sub
			h.Write([]byte(e.Name() + "/"))
			h.Write([]byte(sub))
		} else {
			cksum, err := fileChecksum(child)
			if err != nil {
				return "", err
			}
			tree[rel] = cksum
			h.Write([]byte(e.Name()))
			h.Write([]byte(cksum))
		}
	}

	hash := hex.EncodeToString(h.Sum(nil))
	// Store the directory's own hash (root is ".")
	dirRel, _ := filepath.Rel(root, dir)
	dirRel = filepath.ToSlash(dirRel)
	tree[dirRel] = hash

	return hash, nil
}

func relPath(dir, name, root string) string {
	full := filepath.Join(dir, name)
	rel, _ := filepath.Rel(root, full)
	return filepath.ToSlash(rel)
}
