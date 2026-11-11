package review

import (
	"context"
	"sort"
	"strings"
)

// MultiRepo implements Repo over the Java files of every collected root.
// It loads each file into memory once so cross-project lookups cost zero git calls.
type MultiRepo struct {
	files     map[string]string
	cache     map[string][]string
	truncated bool
	skipped   int
}

// maxMultiRepoFiles bounds the total number of loaded Java files across all roots.
const maxMultiRepoFiles = 20000

// PathKey encodes a root and a repo-relative path into one unique key.
func PathKey(root, rel string) string { return root + "\x00" + rel }

// SplitKey decodes a PathKey into its root and relative path.
// A key without the separator is treated as a plain relative path.
func SplitKey(key string) (root, rel string) {
	if i := strings.IndexByte(key, 0); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// NewMultiRepo loads the Java files from every root, including roots with no commits in the window.
func NewMultiRepo(ctx context.Context, roots []string) *MultiRepo {
	m := &MultiRepo{files: map[string]string{}, cache: map[string][]string{}}
	for _, root := range roots {
		rels := javaFiles(ctx, root)
		// javaFiles caps a single root at maxGrepFiles without reporting it.
		if len(rels) >= maxGrepFiles {
			m.truncated = true
		}
		for _, rel := range rels {
			// Stop loading at the global bound but count every skipped file.
			if len(m.files) >= maxMultiRepoFiles {
				m.truncated = true
				m.skipped++
				continue
			}
			content, err := FileAtCommit(ctx, root, "HEAD", rel)
			// Untracked files have no HEAD blob and are skipped, not fatal.
			if err != nil {
				m.skipped++
				continue
			}
			m.files[PathKey(root, rel)] = content
		}
	}
	return m
}

// GrepJava returns the sorted encoded keys of files containing the literal.
// Results are cached per literal.
func (m *MultiRepo) GrepJava(literal string) []string {
	if hits, ok := m.cache[literal]; ok {
		return hits
	}
	var hits []string
	for key, content := range m.files {
		if strings.Contains(content, literal) {
			hits = append(hits, key)
		}
	}
	sort.Strings(hits)
	m.cache[literal] = hits
	return hits
}

// Read returns a loaded file's content by encoded key without any git call.
func (m *MultiRepo) Read(key string) (string, bool) {
	c, ok := m.files[key]
	return c, ok
}

// Truncated reports whether a file bound stopped the index early.
func (m *MultiRepo) Truncated() bool { return m.truncated }

// Skipped returns the number of files that were skipped during loading.
func (m *MultiRepo) Skipped() int { return m.skipped }
