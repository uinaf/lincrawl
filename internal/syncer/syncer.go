// Package syncer orchestrates ingestion into the local store. Today it
// covers the fixture path; live tail and exact paths land alongside the
// Linear GraphQL client.
package syncer

import (
	"errors"

	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/store"
)

// FixtureResult summarises a fixture ingest run for JSON output.
type FixtureResult struct {
	Source string       `json:"source"`
	Counts store.Counts `json:"counts"`
}

// IngestFixture loads a fixture directory and writes it to the store.
func IngestFixture(s *store.Store, fixtureDir string) (FixtureResult, error) {
	if s == nil {
		return FixtureResult{}, errors.New("syncer: nil store")
	}
	snap, err := linear.LoadFixture(fixtureDir)
	if err != nil {
		return FixtureResult{}, err
	}
	if err := s.IngestSnapshot(snap); err != nil {
		return FixtureResult{}, err
	}
	counts, err := s.Counts()
	if err != nil {
		return FixtureResult{}, err
	}
	return FixtureResult{Source: fixtureDir, Counts: counts}, nil
}
