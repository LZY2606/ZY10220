// Package store 是 SQLite 持久化层。
// 表设计对应四层口径：raw_readings 永不更新，overrides/configs/chosen 保存人工选择。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"consolidation-console/internal/domain"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  h0_cm REAL NOT NULL,
  e0 REAL NOT NULL,
  area_cm2 REAL NOT NULL,
  drain_factor INTEGER NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS step_specs (
  run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  pressure_kpa REAL NOT NULL,
  phase TEXT NOT NULL,
  PRIMARY KEY(run_id, seq)
);
CREATE TABLE IF NOT EXISTS raw_readings (
  id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  orig_step INTEGER NOT NULL,
  pressure_kpa REAL NOT NULL,
  phase TEXT NOT NULL,
  clock_min REAL NOT NULL,
  gauge_mm REAL NOT NULL,
  reset INTEGER NOT NULL DEFAULT 0,
  note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS step_configs (
  run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  step INTEGER NOT NULL,
  method TEXT NOT NULL,
  window_start_min REAL NOT NULL,
  window_end_min REAL NOT NULL,
  PRIMARY KEY(run_id, step)
);
CREATE TABLE IF NOT EXISTS overrides (
  run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  reading_id INTEGER NOT NULL,
  new_step INTEGER NOT NULL,
  PRIMARY KEY(run_id, reading_id)
);
CREATE TABLE IF NOT EXISTS chosen_curve (
  run_id INTEGER PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
  curve_index INTEGER NOT NULL
);
`

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// Import 以一个完整快照重建运行记录（用于首次导入 fixture 或清空后重放）。
// 返回新 run 主键。
func (s *Store) Import(ctx context.Context, snap domain.ExportRun) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO runs(name,h0_cm,e0,area_cm2,drain_factor) VALUES(?,?,?,?,?)`,
		snap.Run.Name, snap.Run.H0Cm, snap.Run.E0, snap.Run.AreaCm2, snap.Run.DrainFactor)
	if err != nil {
		return 0, err
	}
	runID, _ := res.LastInsertId()

	for _, st := range snap.Steps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO step_specs(run_id,seq,pressure_kpa,phase) VALUES(?,?,?,?)`,
			runID, st.Seq, st.PressureKPa, st.Phase); err != nil {
			return 0, err
		}
	}
	ins, err := tx.PrepareContext(ctx,
		`INSERT INTO raw_readings(id,run_id,seq,orig_step,pressure_kpa,phase,clock_min,gauge_mm,reset,note)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer ins.Close()
	for _, rd := range snap.Readings {
		if _, err := ins.ExecContext(ctx, rd.ID, runID, rd.Seq, rd.OrigStep,
			rd.PressureKPa, rd.Phase, rd.ClockMin, rd.GaugeMm, btoi(rd.Reset), rd.Note); err != nil {
			return 0, fmt.Errorf("insert reading %d: %w", rd.ID, err)
		}
	}
	for _, c := range snap.Configs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO step_configs(run_id,step,method,window_start_min,window_end_min)
			 VALUES(?,?,?,?,?)`, runID, c.Step, c.Method, c.WindowStartMin, c.WindowEndMin); err != nil {
			return 0, err
		}
	}
	for _, o := range snap.Overrides {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO overrides(run_id,reading_id,new_step) VALUES(?,?,?)`,
			runID, o.ReadingID, o.NewStep); err != nil {
			return 0, err
		}
	}
	if snap.ChosenCurve != 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chosen_curve(run_id,curve_index) VALUES(?,?)`,
			runID, snap.ChosenCurve); err != nil {
			return 0, err
		}
	}
	return runID, tx.Commit()
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LatestRunID 返回最近一次运行记录；无记录返回 0。
func (s *Store) LatestRunID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM runs ORDER BY id DESC LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (s *Store) ListRuns(ctx context.Context) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,h0_cm,e0,area_cm2,drain_factor,created_at FROM runs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		var r domain.Run
		var created string
		if err := rows.Scan(&r.ID, &r.Name, &r.H0Cm, &r.E0, &r.AreaCm2, &r.DrainFactor, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Export 导出某次运行的完整快照（原始读数 + 人工选择）。
func (s *Store) Export(ctx context.Context, runID int64) (domain.ExportRun, error) {
	snap := domain.ExportRun{SchemaVersion: 1, ChosenCurve: -1}
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,name,h0_cm,e0,area_cm2,drain_factor,created_at FROM runs WHERE id=?`, runID).
		Scan(&snap.Run.ID, &snap.Run.Name, &snap.Run.H0Cm, &snap.Run.E0,
			&snap.Run.AreaCm2, &snap.Run.DrainFactor, &created)
	if err != nil {
		return snap, err
	}
	snap.Run.CreatedAt = parseTime(created)

	rows, err := s.db.QueryContext(ctx,
		`SELECT seq,pressure_kpa,phase FROM step_specs WHERE run_id=? ORDER BY seq`, runID)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var st domain.StepSpec
		if err := rows.Scan(&st.Seq, &st.PressureKPa, &st.Phase); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Steps = append(snap.Steps, st)
	}
	rows.Close()

	rrows, err := s.db.QueryContext(ctx,
		`SELECT id,seq,orig_step,pressure_kpa,phase,clock_min,gauge_mm,reset,note
		 FROM raw_readings WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		return snap, err
	}
	for rrows.Next() {
		var rd domain.Reading
		var reset int
		if err := rrows.Scan(&rd.ID, &rd.Seq, &rd.OrigStep, &rd.PressureKPa, &rd.Phase,
			&rd.ClockMin, &rd.GaugeMm, &reset, &rd.Note); err != nil {
			rrows.Close()
			return snap, err
		}
		rd.RunID = runID
		rd.Reset = reset == 1
		snap.Readings = append(snap.Readings, rd)
	}
	rrows.Close()

	crows, err := s.db.QueryContext(ctx,
		`SELECT step,method,window_start_min,window_end_min FROM step_configs WHERE run_id=? ORDER BY step`, runID)
	if err != nil {
		return snap, err
	}
	for crows.Next() {
		var c domain.StepConfig
		if err := crows.Scan(&c.Step, &c.Method, &c.WindowStartMin, &c.WindowEndMin); err != nil {
			crows.Close()
			return snap, err
		}
		snap.Configs = append(snap.Configs, c)
	}
	crows.Close()

	orows, err := s.db.QueryContext(ctx,
		`SELECT reading_id,new_step FROM overrides WHERE run_id=?`, runID)
	if err != nil {
		return snap, err
	}
	for orows.Next() {
		var o domain.Override
		if err := orows.Scan(&o.ReadingID, &o.NewStep); err != nil {
			orows.Close()
			return snap, err
		}
		snap.Overrides = append(snap.Overrides, o)
	}
	orows.Close()

	_ = s.db.QueryRowContext(ctx,
		`SELECT curve_index FROM chosen_curve WHERE run_id=?`, runID).Scan(&snap.ChosenCurve)
	return snap, nil
}

func (s *Store) SetConfig(ctx context.Context, runID int64, c domain.StepConfig) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO step_configs(run_id,step,method,window_start_min,window_end_min)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(run_id,step) DO UPDATE SET
		   method=excluded.method,
		   window_start_min=excluded.window_start_min,
		   window_end_min=excluded.window_end_min`,
		runID, c.Step, c.Method, c.WindowStartMin, c.WindowEndMin)
	return err
}

func (s *Store) SetOverride(ctx context.Context, runID int64, o domain.Override) error {
	if o.NewStep == 0 {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM overrides WHERE run_id=? AND reading_id=?`, runID, o.ReadingID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO overrides(run_id,reading_id,new_step) VALUES(?,?,?)
		 ON CONFLICT(run_id,reading_id) DO UPDATE SET new_step=excluded.new_step`,
		runID, o.ReadingID, o.NewStep)
	return err
}

func (s *Store) SetChosen(ctx context.Context, runID int64, idx int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chosen_curve(run_id,curve_index) VALUES(?,?)
		 ON CONFLICT(run_id) DO UPDATE SET curve_index=excluded.curve_index`, runID, idx)
	return err
}

func (s *Store) Wipe(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM chosen_curve; DELETE FROM overrides; DELETE FROM step_configs;
		 DELETE FROM raw_readings; DELETE FROM step_specs; DELETE FROM runs;`)
	return err
}

func parseTime(s string) time.Time {
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
