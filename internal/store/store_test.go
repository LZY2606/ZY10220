package store

import (
	"context"
	"testing"

	"consolidation-console/internal/domain"
	"consolidation-console/internal/fixture"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func fixtureSnapshot() domain.ExportRun {
	run, steps, readings, cfgs := fixture.Build()
	return domain.ExportRun{
		SchemaVersion: 1, Run: run, Steps: steps, Readings: readings,
		Configs: cfgs, ChosenCurve: -1,
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	snap := fixtureSnapshot()
	id, err := st.Import(ctx, snap)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	out, err := st.Export(ctx, id)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(out.Readings) != len(snap.Readings) {
		t.Fatalf("reading count mismatch: %d vs %d", len(out.Readings), len(snap.Readings))
	}
	for i := range snap.Readings {
		a, b := snap.Readings[i], out.Readings[i]
		if a.ID != b.ID || a.GaugeMm != b.GaugeMm || a.ClockMin != b.ClockMin ||
			a.Reset != b.Reset || a.OrigStep != b.OrigStep {
			t.Fatalf("reading %d changed after round trip: %+v vs %+v", a.ID, a, b)
		}
	}
	if out.Run.H0Cm != fixture.H0Cm {
		t.Fatalf("run header lost: %+v", out.Run)
	}
}

func TestWipeAndReimport(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id1, _ := st.Import(ctx, fixtureSnapshot())
	if err := st.SetConfig(ctx, id1, domain.StepConfig{Step: 1, Method: "log",
		WindowStartMin: 1, WindowEndMin: 25}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOverride(ctx, id1, domain.Override{ReadingID: 37, NewStep: 4}); err != nil {
		t.Fatal(err)
	}
	if err := st.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	if id, err := st.LatestRunID(ctx); err != nil || id != 0 {
		t.Fatalf("after wipe latest id=%d err=%v", id, err)
	}
	snap := fixtureSnapshot()
	id2, err := st.Import(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := st.Export(ctx, id2)
	if len(out.Overrides) != 0 || out.ChosenCurve != -1 {
		t.Fatalf("manual choices must not survive wipe: %+v", out.Overrides)
	}
	// 读数主键保留，清空后重放仍可反查原读数。
	if out.Readings[0].ID != snap.Readings[0].ID {
		t.Fatal("reading ids must be stable across replay for traceability")
	}
}

func TestManualChoicesPersistSeparately(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, _ := st.Import(ctx, fixtureSnapshot())
	if err := st.SetChosen(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOverride(ctx, id, domain.Override{ReadingID: 37, NewStep: 0}); err != nil {
		t.Fatal(err)
	}
	out, _ := st.Export(ctx, id)
	if out.ChosenCurve != 1 {
		t.Fatalf("chosen=%d", out.ChosenCurve)
	}
	for _, r := range out.Readings {
		if r.ID == 37 && r.OrigStep != 3 {
			t.Fatal("override removal must not mutate the raw reading's original step")
		}
	}
}
