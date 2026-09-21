package web

import (
	"html/template"

	"consolidation/internal/consol"
)

type scaleInfo struct {
	XMin, XMax, YMin, YMax float64
	PadL, PadR, PadT, PadB float64
	W, H                   float64
	InvertY                bool
}

type pageModel struct {
	View           *ViewData
	LoadPath       template.HTML
	TimeDisp       template.HTML
	ELogP          template.HTML
	ELogPScale     *scaleInfo
	ELogPScaleJSON template.JS
	Stages         []consol.Stage
	KindColors     map[consol.StageKind]string
}

var pageTpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>固结路径查验台</title>
<style>
 body{font-family:-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;margin:0;background:#f4f5f7;color:#222}
 header{background:#1f3a5f;color:#fff;padding:14px 24px}
 header h1{margin:0;font-size:20px}
 header p{margin:4px 0 0;font-size:13px;opacity:.85}
 main{max-width:920px;margin:0 auto;padding:16px}
 section{background:#fff;border:1px solid #ddd;border-radius:8px;padding:16px;margin-bottom:16px}
 h2{font-size:16px;margin:0 0 10px;border-left:4px solid #1f6fb2;padding-left:8px}
 table{border-collapse:collapse;width:100%;font-size:12.5px}
 th,td{border:1px solid #e0e0e0;padding:4px 6px;text-align:right}
 th{background:#f0f3f7;text-align:center}
 td.l,th.l{text-align:left}
 input,select{font-size:12.5px;padding:2px 4px}
 input[type=number]{width:80px}
 button{font-size:12.5px;padding:3px 10px;cursor:pointer}
 .tag{display:inline-block;padding:1px 6px;border-radius:3px;color:#fff;font-size:11px}
 .warn{color:#c0392b;font-size:12px}
 .ok{color:#1e8449}
 .muted{color:#777;font-size:12px}
 .pcbox{background:#fdf3e0;border:1px solid #f5b041;border-radius:6px;padding:8px 12px;margin:8px 0;font-size:14px}
 svg{max-width:100%;height:auto}
 #elogp{cursor:crosshair}
 .forms{display:flex;gap:18px;flex-wrap:wrap;font-size:13px}
 .forms fieldset{border:1px solid #ddd;border-radius:6px;padding:8px 12px}
</style>
</head>
<body>
<header>
 <h1>固结路径查验台</h1>
 <p>{{.View.Run.Name}} ｜ 试样 H₀={{printf "%.2f" .View.Run.Specimen.Height0MM}} mm，e₀={{printf "%.3f" .View.Run.Specimen.VoidRatio0}}，{{if .View.Run.Specimen.DoubleDrain}}双面排水{{else}}单面排水{{end}}</p>
</header>
<main>

<section>
 <h2>荷载路径（真实记录顺序，不按压力重排）</h2>
 {{.LoadPath}}
 <p class="muted">蓝=首次加载，红=卸载，绿=再加载，灰=零荷载初始状态。卸载/再加载分支按记录顺序保留，绝不按压力排序混入首次加载曲线。</p>
</section>

<section>
 <h2>时间-位移（分段诊断）</h2>
 {{.TimeDisp}}
 <p class="muted">时钟回退与位移计重置处自动分段，各段独立成线，不拼接成虚假趋势。</p>
 <table>
  <tr><th>级次</th><th>分段</th><th>诊断</th><th>点数</th><th>t 范围 (s)</th><th>排水路径 (mm)</th><th>√t 法 cv (m²/年)</th><th>lg t 法 cv (m²/年)</th></tr>
  {{range .View.Segments}}
  <tr>
   <td>{{.StageNo}}</td><td>{{.Index}}</td>
   <td class="l">{{if .Flags}}<span class="warn">{{range .Flags}}{{.}} {{end}}</span>{{else}}<span class="ok">正常</span>{{end}}</td>
   <td>{{.N}}</td>
   <td>{{printf "%.0f–%.0f" .T0S .T1S}}</td>
   <td>{{printf "%.2f" .DrainMM}}</td>
   <td>{{if .SqrtResult}}{{printf "%.3f" .SqrtResult.CvM2Yr}}{{else}}<span class="warn">{{.SqrtErr}}</span>{{end}}</td>
   <td>{{if .LogResult}}{{printf "%.3f" .LogResult.CvM2Yr}}{{else}}<span class="warn">{{.LogErr}}</span>{{end}}</td>
  </tr>
  {{end}}
 </table>
</section>

<section>
 <h2>孔隙比 - 对数压力（Casagrande 几何法）</h2>
 {{if .ELogPScale}}
 <div id="elogp-wrap">{{.ELogP}}</div>
 <p class="muted">点击图表拾取曲率点候选。零荷载初始状态（{{range .View.Skipped}}seq {{.Seq}} {{end}}）压力非正，不进入对数轴，仅在荷载路径中保留。</p>
 {{else}}<p class="warn">无可绘制点</p>{{end}}
 {{if .View.HasPc}}
 <div class="pcbox">
  {{if eq (len .View.PcResults) 1}}
   先期固结压力候选：pc ≈ <b>{{printf "%.1f" .View.PcLo}} kPa</b>
  {{else}}
   曲率点不唯一，先期固结压力范围：<b>{{printf "%.1f" .View.PcLo}} – {{printf "%.1f" .View.PcHi}} kPa</b>（{{len .View.PcResults}} 个候选）
  {{end}}
 </div>
 {{end}}
 {{range .View.PcErrs}}<p class="warn">候选构造失败：{{.}}</p>{{end}}
 <table>
  <tr><th>#</th><th>log₁₀p</th><th>e</th><th>备注</th><th>pc (kPa)</th><th></th></tr>
  {{range .View.CandViews}}
  <tr>
   <td>{{.Row.ID}}</td>
   <td>{{printf "%.4f" .Row.Pt.LogP}}</td>
   <td>{{printf "%.4f" .Row.Pt.E}}</td>
   <td class="l">{{.Row.Pt.Note}}</td>
   <td>{{if .Pc}}{{printf "%.1f" .Pc}}{{else}}<span class="warn">{{.Err}}</span>{{end}}</td>
   <td><form method="post" action="/candidate/delete" style="display:inline"><input type="hidden" name="id" value="{{.Row.ID}}"><button>删除</button></form></td>
  </tr>
  {{end}}
 </table>
 <div class="forms" style="margin-top:8px">
  <form method="post" action="/candidate/add">
   log₁₀p <input type="number" step="any" name="logp" required>
   e <input type="number" step="any" name="e" required>
   备注 <input type="text" name="note">
   <button>添加候选</button>
  </form>
 </div>
</section>

<section>
 <h2>级次边界修正</h2>
 <table>
  <tr><th>级次</th><th>压力 (kPa)</th><th>类型</th><th>起始 seq</th><th>结束 seq</th><th></th></tr>
  {{range .Stages}}
  <tr>
   <form method="post" action="/stage/update">
   <td>{{.No}}<input type="hidden" name="no" value="{{.No}}"></td>
   <td><input type="number" step="any" name="pressure" value="{{.PressureKPa}}"></td>
   <td><select name="kind">
     {{$k := .Kind}}
     <option value="initial" {{if eq $k "initial"}}selected{{end}}>initial</option>
     <option value="load" {{if eq $k "load"}}selected{{end}}>load</option>
     <option value="unload" {{if eq $k "unload"}}selected{{end}}>unload</option>
     <option value="reload" {{if eq $k "reload"}}selected{{end}}>reload</option>
   </select></td>
   <td><input type="number" name="start_seq" value="{{.StartSeq}}"></td>
   <td><input type="number" name="end_seq" value="{{.EndSeq}}"></td>
   <td><button>保存</button></td>
   </form>
  </tr>
  {{end}}
 </table>
 <p class="muted">只改级次边界与归属，原始读数保持不变。</p>
</section>

<section>
 <h2>固结拟合窗（人工选择）</h2>
 <table>
  <tr><th>#</th><th>级次</th><th>分段</th><th>方法</th><th>窗 (s)</th><th>d₀</th><th>d₁₀₀</th><th>t₅₀/t₉₀ (s)</th><th>cv (m²/年)</th><th></th></tr>
  {{range .View.SavedFits}}
  <tr>
   <td>{{.Row.ID}}</td><td>{{.Row.StageNo}}</td><td>{{.Row.SegmentIdx}}</td>
   <td>{{.Row.Window.Method}}</td>
   <td>{{printf "%.0f–%.0f" .Row.Window.T0S .Row.Window.T1S}}</td>
   {{if .Result}}
   <td>{{printf "%.3f" .Result.D0}}</td><td>{{printf "%.3f" .Result.D100}}</td>
   <td>{{if eq .Row.Window.Method "sqrt"}}{{printf "%.0f" .Result.T90S}}{{else}}{{printf "%.0f" .Result.T50S}}{{end}}</td>
   <td>{{printf "%.3f" .Result.CvM2Yr}}</td>
   {{else}}<td colspan="4" class="warn l">{{.Err}}</td>{{end}}
   <td><form method="post" action="/fit/delete" style="display:inline"><input type="hidden" name="id" value="{{.Row.ID}}"><button>删除</button></form></td>
  </tr>
  {{end}}
 </table>
 <div class="forms" style="margin-top:8px">
  <form method="post" action="/fit/add">
   级次 <input type="number" name="stage" required>
   分段 <input type="number" name="segment" value="0" required>
   方法 <select name="method"><option value="sqrt">√t（Taylor）</option><option value="log">lg t（Casagrande）</option></select>
   t₀ <input type="number" step="any" name="t0" value="0" required>
   t₁ <input type="number" step="any" name="t1" value="900" required>
   <button>保存拟合窗</button>
  </form>
 </div>
</section>

<section>
 <h2>原始读数与修正值</h2>
 <details><summary>展开读数表（{{len .View.Points}} 条）</summary>
 <table>
  <tr><th>seq</th><th>级次(原始)</th><th>p (kPa)</th><th>t (s)</th><th>位移计原始 (mm)</th><th>累计位移 (mm)</th><th>高度 (mm)</th><th>e</th><th>分段</th><th class="l">备注</th></tr>
  {{range .View.Points}}
  <tr>
   <td>{{.Raw.Seq}}</td><td>{{.Raw.Stage}}</td><td>{{printf "%g" .Raw.PressureKPa}}</td>
   <td>{{printf "%.0f" .Raw.ElapsedS}}</td><td>{{printf "%.4f" .Raw.DialMM}}</td>
   <td>{{printf "%.4f" .CumDialMM}}</td><td>{{printf "%.3f" .HeightMM}}</td>
   <td>{{printf "%.4f" .VoidRatio}}</td><td>{{.SegmentIdx}}</td>
   <td class="l">{{.Raw.Note}}</td>
  </tr>
  {{end}}
 </table>
 </details>
</section>

<section>
 <h2>运行记录</h2>
 <div class="forms">
  <fieldset><legend>导出</legend><a href="/export" download>下载运行记录 JSON</a></fieldset>
  <fieldset><legend>清空后重新导入复核</legend>
   <form method="post" action="/import" enctype="multipart/form-data">
    <input type="file" name="file" accept=".json" required><button>导入并覆盖</button>
   </form>
  </fieldset>
  <fieldset><legend>重置</legend>
   <form method="post" action="/reset" onsubmit="return confirm('清空数据库并重新载入 fixture？')">
    <button>清空并重新载入 fixture</button>
   </form>
  </fieldset>
 </div>
</section>

</main>
{{if .ELogPScale}}
<script>
(function(){
  var s = {{.ELogPScaleJSON}};
  var wrap = document.getElementById('elogp-wrap');
  var svg = wrap && wrap.querySelector('svg');
  if(!svg) return;
  svg.id = 'elogp';
  svg.addEventListener('click', function(ev){
    var r = svg.getBoundingClientRect();
    var px = (ev.clientX - r.left) * (s.W / r.width);
    var py = (ev.clientY - r.top) * (s.H / r.height);
    if (px < s.PadL || px > s.W - s.PadR || py < s.PadT || py > s.H - s.PadB) return;
    var logp = s.XMin + (px - s.PadL) / (s.W - s.PadL - s.PadR) * (s.XMax - s.XMin);
    var e = s.YMin + (py - s.PadT) / (s.H - s.PadT - s.PadB) * (s.YMax - s.YMin);
    var note = prompt('曲率点候选备注（可留空）','图上拾取');
    if (note === null) return;
    var body = new URLSearchParams({logp: logp.toFixed(5), e: e.toFixed(5), note: note});
    fetch('/candidate/add', {method:'POST', body: body}).then(function(){ location.reload(); });
  });
})();
</script>
{{end}}
</body>
</html>`))
