package store

import (
	"testing"

	"consolidation/internal/consol"
)

func openMem(t *testing.T) *Store {
	t.Helper()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSeedAndReadBack(t *testing.T) {
	st := openMem(t)
	seeded, err := st.SeedIfEmpty()
	if err != nil || !seeded {
		t.Fatalf("seeded=%v err=%v", seeded, err)
	}
	// second seed is a no-op
	seeded2, _ := st.SeedIfEmpty()
	if seeded2 {
		t.Fatal("reseeded a non-empty db")
	}
	run, err := st.FirstRun()
	if err != nil {
		t.Fatal(err)
	}
	rs, err := st.Readings(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 {
		t.Fatal("no readings")
	}
	for i, r := range rs {
		if r.Seq != i {
			t.Fatalf("seq broken at %d: %d", i, r.Seq)
		}
	}
	stages, err := st.Stages(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 13 {
		t.Fatalf("stages = %d, want 13", len(stages))
	}
}

func TestStageBoundsCorrectionKeepsRaw(t *testing.T) {
	st := openMem(t)
	st.SeedIfEmpty()
	run, _ := st.FirstRun()
	before, _ := st.Readings(run.ID)
	err := st.UpdateStageBounds(run.ID, consol.Stage{
		No: 3, PressureKPa: 55, Kind: consol.KindLoad, StartSeq: 39, EndSeq: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	stages, _ := st.Stages(run.ID)
	if stages[3].PressureKPa != 55 || stages[3].StartSeq != 39 {
		t.Fatalf("stage not updated: %+v", stages[3])
	}
	after, _ := st.Readings(run.ID)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("raw reading %d mutated", i)
		}
	}
}

func TestFitsAndCandidates(t *testing.T) {
	st := openMem(t)
	st.SeedIfEmpty()
	run, _ := st.FirstRun()
	if err := st.SaveFit(run.ID, 6, 1, consol.FitWindow{Method: "sqrt", T0S: 0, T1S: 900}); err != nil {
		t.Fatal(err)
	}
	fits, _ := st.Fits(run.ID)
	if len(fits) != 1 || fits[0].StageNo != 6 || fits[0].SegmentIdx != 1 {
		t.Fatalf("fits = %+v", fits)
	}
	id, err := st.AddCandidate(run.ID, consol.CurvatureCandidate{LogP: 1.9, E: 0.5, Note: "knee"})
	if err != nil {
		t.Fatal(err)
	}
	cands, _ := st.Candidates(run.ID)
	if len(cands) != 1 || cands[0].ID != id {
		t.Fatalf("candidates = %+v", cands)
	}
	st.DeleteFit(fits[0].ID)
	st.DeleteCandidate(id)
	fits, _ = st.Fits(run.ID)
	cands, _ = st.Candidates(run.ID)
	if len(fits) != 0 || len(cands) != 0 {
		t.Fatal("deletes failed")
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	st := openMem(t)
	st.SeedIfEmpty()
	run, _ := st.FirstRun()
	st.SaveFit(run.ID, 2, 0, consol.FitWindow{Method: "log", T0S: 60, T1S: 14400})
	st.AddCandidate(run.ID, consol.CurvatureCandidate{LogP: 2.0, E: 0.4})
	st.UpdateStageBounds(run.ID, consol.Stage{No: 1, PressureKPa: 12.5, Kind: consol.KindLoad, StartSeq: 12, EndSeq: 24})

	data, err := st.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	// wipe and re-import
	if err := st.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FirstRun(); err == nil {
		t.Fatal("run survived Clear")
	}
	if err := st.ImportJSON(data); err != nil {
		t.Fatal(err)
	}
	data2, err := st.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	// exports differ only in the timestamp
	var d1, d2 Dump
	if err := jsonUnmarshal(data, &d1); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal(data2, &d2); err != nil {
		t.Fatal(err)
	}
	d1.ExportedAt, d2.ExportedAt = "", ""
	d1.Run.ID, d2.Run.ID = 0, 0
	if !dumpEqual(&d1, &d2) {
		t.Fatal("export/import round trip changed the record")
	}
	if d2.Stages[1].StartSeq != 12 || d2.Stages[1].EndSeq != 24 {
		t.Fatalf("stage correction lost: %+v", d2.Stages[1])
	}
	if len(d2.Fits) != 1 || len(d2.Candidates) != 1 {
		t.Fatalf("fits/candidates lost: %+v %+v", d2.Fits, d2.Candidates)
	}
}
