package service

import (
	"fmt"
	"log"
	"path/filepath"

	"repotory/internal/db"
	"repotory/internal/service/wikipage"
)

// Approved-page disk projection (ADR-0069): the service half. The mirror
// lives at <database dir>/pages — derived from the LIVE database file rather
// than a platform default, so a test store projects into its own temp dir and
// an evaluation copy never touches the real user's mirror. An in-memory
// database has no file and therefore no mirror.
//
// Default ON, unlike the audit gates: the projection is a local derived
// artifact of ALREADY-APPROVED content in Cinder's own data directory —
// nothing leaves the machine and nothing unreviewed is published, so the
// human approval gate (ADR-0015) is the authorization, same as retrieval and
// the session brief. pages_projection=0 is the kill switch.

// PagesProjectionEnabled reports the kill switch. An absent row means on.
func (s *Service) PagesProjectionEnabled() bool {
	v, _ := db.GetConfig(s.db, "pages_projection")
	return v != "0"
}

// PagesProjectionDir is where this store's mirror lives, or "" for an
// in-memory database.
func (s *Service) PagesProjectionDir() string {
	f := s.liveDatabaseFile()
	if f == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(f), "pages")
}

// refreshPagesProjection rebuilds the mirror after a page mutation that can
// change the approved set. Non-fatal by contract: the database write already
// succeeded, and a mirror that cannot be rebuilt must not make the approval
// look failed — the next mutation or CLI refresh catches up.
func (s *Service) refreshPagesProjection() {
	if !s.PagesProjectionEnabled() {
		return
	}
	dir := s.PagesProjectionDir()
	if dir == "" {
		return
	}
	if _, err := wikipage.ProjectApprovedPages(s.db, dir); err != nil {
		log.Printf("pages projection: %v", err)
	}
}

// RefreshPagesProjection is the explicit rebuild (CLI / catch-all after a
// backup restore or a project deletion, whose cascade removes pages without
// a page-level mutation hook).
func (s *Service) RefreshPagesProjection() (*wikipage.ProjectionReport, error) {
	dir := s.PagesProjectionDir()
	if dir == "" {
		return nil, fmt.Errorf("this database has no file (in-memory), so there is no projection directory")
	}
	return wikipage.ProjectApprovedPages(s.db, dir)
}
