package fixture

import "testing"

func TestFixtureShape(t *testing.T) {
	run, steps, readings, cfgs := Build()
	if run.H0Cm <= 0 || run.E0 <= 0 || run.AreaCm2 <= 0 {
		t.Fatal("run header must use positive specimen dimensions")
	}
	if run.DrainFactor != 2 {
		t.Fatalf("drain factor=%d", run.DrainFactor)
	}
	// 至少覆盖首次加载、卸载、再加载三阶段。
	phases := map[string]bool{}
	for _, s := range steps {
		phases[s.Phase] = true
		if s.PressureKPa <= 0 {
			t.Fatalf("all step pressures must be positive for log axis, step %d", s.Seq)
		}
	}
	for _, want := range []string{"load", "unload", "reload"} {
		if !phases[want] {
			t.Fatalf("missing phase %s", want)
		}
	}
	var reset, rollback int
	for _, r := range readings {
		if r.Reset {
			reset++
		}
		if r.Note == "时钟回退读数" {
			rollback++
		}
	}
	if reset != 1 {
		t.Fatalf("expected exactly one platform reset, got %d", reset)
	}
	if rollback != 1 {
		t.Fatalf("expected exactly one clock rollback, got %d", rollback)
	}
	if len(cfgs) != len(steps) {
		t.Fatal("each step needs a default fit config")
	}
}
