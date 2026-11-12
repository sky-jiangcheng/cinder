package writer

import (
	"fmt"
	"os"
	"path/filepath"
)

// Files the election lives in, next to the database. Both are derived from the
// data directory, so every Cinder process on the machine agrees on which
// knowledge base a given lock guards — that identity is the point, and a
// per-process temp path would defeat it.
const (
	lockName     = "repotory.writer.lock"
	endpointName = "repotory.writer.endpoint"
)

func lockPath(dir string) string { return filepath.Join(dir, lockName) }

// EndpointPath is where the writer publishes its address and token.
func EndpointPath(dir string) string { return filepath.Join(dir, endpointName) }

// procTag identifies the holder inside the lock file, so a reader can name the
// process that is holding the write path instead of guessing.
func procTag() string { return fmt.Sprintf("pid=%d", os.Getpid()) }

// DirOf returns the data directory that holds a database path. The election
// files live beside the database on purpose: one knowledge base, one writer —
// two databases (a rehearsal copy, a restored snapshot) must not share a lock.
func DirOf(dbPath string) string { return filepath.Dir(dbPath) }
