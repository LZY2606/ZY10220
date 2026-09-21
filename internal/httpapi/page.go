package httpapi

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>固结路径查验台</title>
<style>
 body{font-family:ui-sans-serif,system-ui,"PingFang SC","Microsoft YaHei",sans-serif;
   margin:0;background:#f6f8fa;color:#1f2328}
 header{background:#0d1117;color:#fff;padding:14px 22px}
 header h1{margin:0;font-size:20px}
 main{max-width:1000px;margin:18px auto;padding:0 14px}
 section{background:#fff;border:1px solid #d0d7de;border-radius:8px;padding:14px 16px;margin:14px 0}
 h2{font-size:15px;margin:0 0 10px}
 table{border-collapse:collapse;width:100%;font-size:12px}
 th,td{border:1px solid #e3e7eb;padding:4px 7px;text-align:right;white-space:nowrap}
 td.wrap{white-space:normal;min-width:170px;max-width:210px;text-align:left;word-break:break-word}
 th{background:#f0f3f6}
 td.l,th.l{text-align:left}
 .muted{color:#656d76;font-size:12px}
 .tag{border-radius:10px;padding:1px 8px;font-size:11px;color:#fff}
 .load{background:#1f6feb}.unload{background:#d1242f}.reload{background:#1a7f37}.initial{background:#8c8c8c}
 .warn{color:#cf222e;font-weight:600}.ok{color:#1a7f37}
 input[type=number]{width:64px}
 button{background:#1f6feb;color:#fff;border:0;border-radius:6px;padding:5px 12px;cursor:pointer;font-size:12px}
 button.ghost{background:#eaeef2;color:#1f2328}
 .row{display:flex;gap:10px;align-items:center;flex-wrap:wrap}
 .notice{background:#ddf4ff;border:1px solid #54aeff;border-radius:6px;padding:6px 10px;font-size:12px}
 svg{width:100%;height:auto;border:1px solid #eee;border-radius:6px;background:#fff}
</style>
</head>
<body>
<header><h1>固结路径查验台</h1>
<div class="muted" style="color:#9ba6b2">原始读数 / 修正值 / 绘图几何 / 人工选择 四层分离 · 零荷载保留为初始状态，不进入对数轴</div></header>
<main>
{{if .Notice}}<div class="notice">{{.Notice}}</div>{{end}}

<section>
 <div class="row" style="justify-content:space-between">
  <div><b>试样口径</b>：H0={{printf "%.2f" .Run.H0Cm}} cm，e0={{printf "%.2f" .Run.E0}}，
   面积={{printf "%.0f" .Run.AreaCm2}} cm²，排水因子={{.Run.DrainFactor}}（排水距离=H/因子）</div>
  <div class="row">
   <a class="ghost" href="/api/export" style="text-decoration:none;border-radius:6px;padding:5px 12px;background:#eaeef2">导出运行记录(JSON)</a>
   <form method="post" action="/api/reset" style="margin:0">
     <button class="ghost" type="submit" onclick="return confirm('清空数据库并用固定 fixture 重新导入？')">清空并重放 fixture</button>
   </form>
  </div>
 </div>
 <div class="muted">重放导入：选择此前导出的 JSON 文件 →
   <input type="file" id="imp" accept="application/json">
   <button class="ghost" type="button" onclick="doImport()">导入并清空复核</button></div>
</section>

<section>
 <h2>时间 - 位移（含归零续接与时钟回退分段）</h2>
 {{chartTime .}}
</section>

<section>
 <h2>孔隙比 - 对数压力（首次加载 / 卸载 / 再加载分路径保留）</h2>
 {{chartE .}}
 <p class="muted" style="margin:8px 0 0">
  Casagrande：自动先期固结压力 <b>pc≈{{printf "%.0f" .Result.Casa.PcAuto}} kPa</b>；
  几何有效候选范围 <b>{{printf "%.0f" .Result.Casa.PcLow}}–{{printf "%.0f" .Result.Casa.PcHigh}} kPa</b>
  （{{.Result.Casa.Note}}）。当前采用 <b>{{printf "%.0f" .Result.Casa.PcChosen}} kPa</b>。</p>
 <form method="post" action="/api/choose-curve" style="margin-top:8px">
  <table>
   <tr><th>候选曲率点(级)</th><th>压力 kPa</th><th>转角°</th><th>曲率</th><th>局部最大</th><th>构造 pc kPa</th><th>选用</th></tr>
   {{range .Result.Casa.Candidates}}
   <tr>
    <td class="l">#{{.Step}}（点序 {{.Index}}）</td>
    <td>{{printf "%.0f" .Pressure}}</td>
    <td>{{printf "%.1f" .TurnDeg}}</td>
    <td>{{printf "%.4f" .Curvature}}</td>
    <td>{{if .LocalMaximum}}<span class="ok">是</span>{{else}}否{{end}}</td>
    <td>{{if .GeometryOK}}{{printf "%.0f" .PcKPa}}{{else}}<span class="warn">交点无效</span>{{end}}</td>
    <td><button name="index" value="{{.Index}}" type="submit"
      class="{{if eq .Index $.Result.Casa.ChosenIndex}}ok{{else}}ghost{{end}}">选此候选</button></td>
   </tr>
   {{end}}
  </table>
 </form>
 <form method="post" action="/api/choose-curve" style="margin-top:6px">
   <button class="ghost" name="index" value="-1" type="submit">恢复自动选择</button></form>
</section>

<section>
 <h2>荷载路径（按真实序列，拒绝按压力排序）</h2>
 {{chartPath .}}
</section>

<section>
 <h2>各级固结参数与拟合窗（可切换根号时间 / 对数时间）</h2>
 <table>
  <tr><th>级</th><th>阶段</th><th>压力 kPa</th><th>本级压缩 mm</th>
  <th>e(起)</th><th>e(止)</th><th>排水距离 cm</th><th>cv cm²/min</th><th>cv cm²/s</th>
  <th class="l">分段诊断</th><th class="l wrap">拟合设置</th></tr>
  {{$configs := .Configs}}
  {{range .Result.StepResults}}
  {{$cfg := index $configs .Step}}
  <tr>
   <td>{{.Step}}</td>
   <td><span class="tag {{.Phase}}">{{.Phase}}</span></td>
   <td>{{printf "%.0f" .Pressure}}</td>
   <td>{{printf "%.3f" .DeltaSmm}}</td>
   <td>{{printf "%.3f" .EStart}}</td>
   <td>{{printf "%.3f" .EEnd}}</td>
   <td>{{printf "%.3f" .DrainCm}}</td>
   <td>{{if .CvCm2Min}}{{printf "%.4f" .CvCm2Min}}{{else}}—{{end}}</td>
   <td>{{if .CvCm2Sec}}{{printf "%.2e" .CvCm2Sec}}{{else}}—{{end}}</td>
   <td class="l wrap">
    {{len .Segments}} 段{{if .Diagnostic}}；<span class="muted">{{.Diagnostic}}</span>{{end}}
    {{range .Segments}}{{if .Rollback}}<div class="warn">段{{.Index}}: {{.Note}}</div>{{else if .HasReset}}<div style="color:#bc4c00">段{{.Index}}: {{.Note}}</div>{{end}}{{end}}
    {{if and .Fit .Fit.R2}}<div class="muted">R²={{printf "%.3f" .Fit.R2}}{{if .Fit.T90Min}}；t90={{printf "%.1f" .Fit.T90Min}}min{{end}}{{if .Fit.T50Min}}；t50={{printf "%.1f" .Fit.T50Min}}min{{end}}</div>{{end}}
   </td>
   <td class="l wrap">
    <form method="post" action="/api/config" class="row" style="margin:0">
     <input type="hidden" name="step" value="{{.Step}}">
     <select name="method">
       <option value="sqrt" {{if eq $cfg.Method "sqrt"}}selected{{end}}>根号时间</option>
       <option value="log" {{if eq $cfg.Method "log"}}selected{{end}}>对数时间</option>
     </select>
     <input type="number" name="start" step="0.1" min="0" value="{{printf "%.1f" $cfg.WindowStartMin}}">
     <span>~</span>
     <input type="number" name="end" step="0.1" min="0.1" value="{{printf "%.1f" $cfg.WindowEndMin}}">
     <button type="submit">应用</button>
    </form>
    {{if .Fit}}<div class="muted">{{.Fit.Note}}</div>{{end}}
   </td>
  </tr>
  {{end}}
 </table>
</section>

<section>
 <h2>原始读数与修正值对照（边界改派；原读数永不改写，可反查）</h2>
 <div class="muted" style="margin-bottom:6px">“现级”与“原级”不同即为边界修正；归零/回退标记来自原始记录。</div>
 <table>
  <tr><th>读数ID</th><th>原级</th><th>现级</th><th>阶段</th><th>时钟 min</th>
  <th>原始表读数 mm</th><th>修正累计压缩 mm</th><th>重置</th><th>回退</th><th>片段</th>
  <th class="l">诊断/备注</th><th class="l">级次边界修正</th></tr>
  {{range .Result.Points}}
  <tr>
   <td>#{{.ReadingID}}</td>
   <td>{{.OrigStep}}</td>
   <td>{{.Step}}</td>
   <td><span class="tag {{.Phase}}">{{.Phase}}</span></td>
   <td>{{printf "%.2f" .ClockMin}}</td>
   <td>{{printf "%.3f" .GaugeMm}}</td>
   <td>{{printf "%.3f" .Settlement}}</td>
   <td>{{if .Reset}}<span style="color:#bc4c00">归零</span>{{else}}—{{end}}</td>
   <td>{{if .Rollback}}<span class="warn">回退</span>{{else}}—{{end}}</td>
   <td>{{.Segment}}</td>
   <td class="l">{{if .Diagnostic}}{{.Diagnostic}}{{else}}—{{end}}</td>
   <td class="l">
    <form method="post" action="/api/override" class="row" style="margin:0">
     <input type="hidden" name="reading_id" value="{{.ReadingID}}">
     <select name="new_step">
      <option value="0">原级 {{.OrigStep}}</option>
      {{$self := .}}
      {{range $.Steps}}<option value="{{.Seq}}" {{if and (ne $self.OrigStep .Seq) (eq $self.Step .Seq)}}selected{{end}}>{{.Seq}} ({{printf "%.0f" .PressureKPa}}kPa {{.Phase}})</option>{{end}}
     </select>
     <button class="ghost" type="submit">改派</button>
    </form>
   </td>
  </tr>
  {{end}}
 </table>
</section>
</main>
<script>
async function doImport(){
  const f=document.getElementById('imp').files[0];
  if(!f){alert('请先选择导出的 JSON 文件');return;}
  const text=await f.text();
  JSON.parse(text);
  const r=await fetch('/api/import',{method:'POST',
    headers:{'Content-Type':'application/json'},body:text});
  if(!r.ok){alert('导入失败：'+(await r.text()));return;}
  location.href='/?notice='+encodeURIComponent('已重放导入并复核');
}
</script>
</body></html>`
