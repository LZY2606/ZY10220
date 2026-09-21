package domain

import "time"

// Run 是一次固结试验的台账头。厚度修正口径：
// H0Cm 初始试样厚度，E0 初始孔隙比，AreaCm2 试样面积，
// DrainFactor 为排水距离因子（双面排水 2，单面排水 1）。
type Run struct {
	ID          int64
	Name        string
	CreatedAt   time.Time
	H0Cm        float64
	E0          float64
	AreaCm2     float64
	DrainFactor int
}

// 加卸载阶段标记。
const (
	PhaseLoad    = "load"
	PhaseUnload  = "unload"
	PhaseReload  = "reload"
	PhaseInitial = "initial"
)

// StepSpec 描述一个荷载级：级次、压力(kPa)与加卸载属性。
type StepSpec struct {
	Seq         int     `json:"seq"`
	PressureKPa float64 `json:"pressure_kpa"`
	Phase       string  `json:"phase"`
}

// Reading 是仪器原始读数（永不就地修改）。
// Reset=true 表示该读数时刻位移计被归零（平台重置），GaugeMm 为归零后读数。
type Reading struct {
	ID          int64   `json:"id"`
	RunID       int64   `json:"run_id"`
	Seq         int     `json:"seq"`
	OrigStep    int     `json:"orig_step"`
	PressureKPa float64 `json:"pressure_kpa"`
	Phase       string  `json:"phase"`
	ClockMin    float64 `json:"clock_min"`
	GaugeMm     float64 `json:"gauge_mm"`
	Reset       bool    `json:"reset"`
	Note        string  `json:"note"`
}

// StepConfig 为每个荷载级保存人工选择的拟合方法与拟合窗(分钟)。
type StepConfig struct {
	Step           int     `json:"step"`
	Method         string  `json:"method"` // sqrt | log
	WindowStartMin float64 `json:"window_start_min"`
	WindowEndMin   float64 `json:"window_end_min"`
}

// ExportRun 是“运行记录导出/重放”的完整快照。
type ExportRun struct {
	SchemaVersion int          `json:"schema_version"`
	Run           Run          `json:"run"`
	Steps         []StepSpec   `json:"steps"`
	Readings      []Reading    `json:"readings"`
	Configs       []StepConfig `json:"configs"`
	Overrides     []Override   `json:"overrides"`
	ChosenCurve   int          `json:"chosen_curve"` // Casagrande 曲率点候选序号，-1 表示自动
}

// Override 把某个读数人工改派到另一荷载级（边界修正）。
type Override struct {
	ReadingID int64 `json:"reading_id"`
	NewStep   int   `json:"new_step"`
}
