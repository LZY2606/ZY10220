// Package store persists runs, raw readings, stage boundaries, fit windows
// and curvature candidates in SQLite. Raw readings are insert-only from the
// application's point of view; user input lands in stages / fits /
// candidates, keeping raw, corrected, geometry and manual choices separate.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"consolidation/internal/consol"
	"consolidation/internal/fixture"
)

// Store wraps the SQLite handle.
type Store struct{ db *sql.DB }

// Open opens (and migrates) the database at path. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS runs(
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  h0 REAL NOT NULL,
  e0 REAL NOT NULL,
  double_drain INTEGER NOT NULL,
  created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS readings(
  run_id INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  stage INTEGER NOT NULL,
  pressure REAL NOT NULL,
  elapsed_s REAL NOT NULL,
  dial REAL NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, seq));
CREATE TABLE IF NOT EXISTS stages(
  run_id INTEGER NOT NULL,
  no INTEGER NOT NULL,
  pressure REAL NOT NULL,
  kind TEXT NOT NULL,
  start_seq INTEGER NOT NULL,
  end_seq INTEGER NOT NULL,
  PRIMARY KEY(run_id, no));
CREATE TABLE IF NOT EXISTS fits(
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL,
  stage_no INTEGER NOT NULL,
  segment_idx INTEGER NOT NULL,
  method TEXT NOT NULL,
  t0 REAL NOT NULL,
  t1 REAL NOT NULL,
  created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS candidates(
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL,
  logp REAL NOT NULL,
  e REAL NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL);`)
	return err
}

// SeedIfEmpty loads the fixture run when the database has no runs yet.
// Returns true when seeding happened.
func (s *Store) SeedIfEmpty() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	return true, s.ImportRun(fixture.Build())
}

// ImportRun inserts a complete run with stages and raw readings.
func (s *Store) ImportRun(r fixture.Run) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO runs(name,h0,e0,double_drain,created_at) VALUES(?,?,?,?,?)`,
		r.Name, r.Specimen.Height0MM, r.Specimen.VoidRatio0, bool2int(r.Specimen.DoubleDrain),
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	runID, _ := res.LastInsertId()
	for _, rd := range r.Readings {
		if _, err := tx.Exec(`INSERT INTO readings(run_id,seq,stage,pressure,elapsed_s,dial,note) VALUES(?,?,?,?,?,?,?)`,
			runID, rd.Seq, rd.Stage, rd.PressureKPa, rd.ElapsedS, rd.DialMM, rd.Note); err != nil {
			return err
		}
	}
	for _, st := range r.Stages {
		if _, err := tx.Exec(`INSERT INTO stages(run_id,no,pressure,kind,start_seq,end_seq) VALUES(?,?,?,?,?,?)`,
			runID, st.No, st.PressureKPa, string(st.Kind), st.StartSeq, st.EndSeq); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func bool2int(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RunRow is the persisted run header.
type RunRow struct {
	ID       int64
	Name     string
	Specimen consol.Specimen
}

// FirstRun returns the first run (the console is single-run focused).
func (s *Store) FirstRun() (RunRow, error) {
	var r RunRow
	var dd int
	err := s.db.QueryRow(`SELECT id,name,h0,e0,double_drain FROM runs ORDER BY id LIMIT 1`).
		Scan(&r.ID, &r.Name, &r.Specimen.Height0MM, &r.Specimen.VoidRatio0, &dd)
	r.Specimen.DoubleDrain = dd != 0
	return r, err
}

// Readings returns raw readings in true record order.
func (s *Store) Readings(runID int64) ([]consol.Reading, error) {
	rows, err := s.db.Query(`SELECT seq,stage,pressure,elapsed_s,dial,note FROM readings WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []consol.Reading
	for rows.Next() {
		var r consol.Reading
		if err := rows.Scan(&r.Seq, &r.Stage, &r.PressureKPa, &r.ElapsedS, &r.DialMM, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Stages returns stage boundaries ordered by stage number.
func (s *Store) Stages(runID int64) ([]consol.Stage, error) {
	rows, err := s.db.Query(`SELECT no,pressure,kind,start_seq,end_seq FROM stages WHERE run_id=? ORDER BY no`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []consol.Stage
	for rows.Next() {
		var st consol.Stage
		var kind string
		if err := rows.Scan(&st.No, &st.PressureKPa, &kind, &st.StartSeq, &st.EndSeq); err != nil {
			return nil, err
		}
		st.Kind = consol.StageKind(kind)
		out = append(out, st)
	}
	return out, rows.Err()
}

// UpdateStageBounds applies a user correction to one stage's boundaries and
// pressure. Raw readings are untouched.
func (s *Store) UpdateStageBounds(runID int64, st consol.Stage) error {
	_, err := s.db.Exec(`UPDATE stages SET pressure=?,kind=?,start_seq=?,end_seq=? WHERE run_id=? AND no=?`,
		st.PressureKPa, string(st.Kind), st.StartSeq, st.EndSeq, runID, st.No)
	return err
}

// FitRow is a saved fit-window selection (manual input; results are derived).
type FitRow struct {
	ID         int64
	RunID      int64
	StageNo    int
	SegmentIdx int
	Window     consol.FitWindow
}

// SaveFit records a fit-window selection.
func (s *Store) SaveFit(runID int64, stageNo, segIdx int, w consol.FitWindow) error {
	_, err := s.db.Exec(`INSERT INTO fits(run_id,stage_no,segment_idx,method,t0,t1,created_at) VALUES(?,?,?,?,?,?,?)`,
		runID, stageNo, segIdx, w.Method, w.T0S, w.T1S, time.Now().UTC().Format(time.RFC3339))
	return err
}

// Fits lists saved fit windows for a run.
func (s *Store) Fits(runID int64) ([]FitRow, error) {
	rows, err := s.db.Query(`SELECT id,run_id,stage_no,segment_idx,method,t0,t1 FROM fits WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FitRow
	for rows.Next() {
		var f FitRow
		if err := rows.Scan(&f.ID, &f.RunID, &f.StageNo, &f.SegmentIdx, &f.Window.Method, &f.Window.T0S, &f.Window.T1S); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFit removes a saved fit window.
func (s *Store) DeleteFit(id int64) error {
	_, err := s.db.Exec(`DELETE FROM fits WHERE id=?`, id)
	return err
}

// CandidateRow is a saved curvature candidate (manual input).
type CandidateRow struct {
	ID int64
	Pt consol.CurvatureCandidate
}

// AddCandidate saves a curvature candidate picked on the e-log(p) chart.
func (s *Store) AddCandidate(runID int64, c consol.CurvatureCandidate) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO candidates(run_id,logp,e,note,created_at) VALUES(?,?,?,?,?)`,
		runID, c.LogP, c.E, c.Note, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Candidates lists saved curvature candidates.
func (s *Store) Candidates(runID int64) ([]CandidateRow, error) {
	rows, err := s.db.Query(`SELECT id,logp,e,note FROM candidates WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CandidateRow
	for rows.Next() {
		var c CandidateRow
		if err := rows.Scan(&c.ID, &c.Pt.LogP, &c.Pt.E, &c.Pt.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCandidate removes a curvature candidate.
func (s *Store) DeleteCandidate(id int64) error {
	_, err := s.db.Exec(`DELETE FROM candidates WHERE id=?`, id)
	return err
}

// Dump is the exportable run record: raw readings, stage corrections, fit
// windows and candidates, all in one JSON document.
type Dump struct {
	Run        RunRow                      `json:"run"`
	Readings   []consol.Reading            `json:"readings"`
	Stages     []consol.Stage              `json:"stages"`
	Fits       []FitRow                    `json:"fits"`
	Candidates []consol.CurvatureCandidate `json:"candidates"`
	ExportedAt string                      `json:"exported_at"`
}

// Export serialises the first run and all associated records.
func (s *Store) Export() (*Dump, error) {
	run, err := s.FirstRun()
	if err != nil {
		return nil, err
	}
	d := &Dump{Run: run, ExportedAt: time.Now().UTC().Format(time.RFC3339)}
	if d.Readings, err = s.Readings(run.ID); err != nil {
		return nil, err
	}
	if d.Stages, err = s.Stages(run.ID); err != nil {
		return nil, err
	}
	if d.Fits, err = s.Fits(run.ID); err != nil {
		return nil, err
	}
	cands, err := s.Candidates(run.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		d.Candidates = append(d.Candidates, c.Pt)
	}
	return d, nil
}

// ExportJSON renders the dump as indented JSON.
func (s *Store) ExportJSON() ([]byte, error) {
	d, err := s.Export()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(d, "", "  ")
}

// Clear wipes every table. Used before re-importing a run record.
func (s *Store) Clear() error {
	_, err := s.db.Exec(`DELETE FROM candidates; DELETE FROM fits; DELETE FROM stages; DELETE FROM readings; DELETE FROM runs;`)
	return err
}

// ImportDump replaces the database content with an exported run record.
func (s *Store) ImportDump(d *Dump) error {
	if err := s.Clear(); err != nil {
		return err
	}
	fr := fixture.Run{
		Name: d.Run.Name,
		Specimen: consol.Specimen{
			Height0MM:   d.Run.Specimen.Height0MM,
			VoidRatio0:  d.Run.Specimen.VoidRatio0,
			DoubleDrain: d.Run.Specimen.DoubleDrain,
		},
		Stages:   d.Stages,
		Readings: d.Readings,
	}
	if err := s.ImportRun(fr); err != nil {
		return err
	}
	run, err := s.FirstRun()
	if err != nil {
		return err
	}
	for _, f := range d.Fits {
		if err := s.SaveFit(run.ID, f.StageNo, f.SegmentIdx, f.Window); err != nil {
			return err
		}
	}
	for _, c := range d.Candidates {
		if _, err := s.AddCandidate(run.ID, c); err != nil {
			return err
		}
	}
	return nil
}

// ImportJSON parses and imports an exported run record.
func (s *Store) ImportJSON(data []byte) error {
	var d Dump
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("parse dump: %w", err)
	}
	return s.ImportDump(&d)
}
