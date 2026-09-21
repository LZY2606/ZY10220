# 固结路径查验台

固结试验路径查验本地服务：保留真实荷载路径（首次加压 → 卸载 → 再加载），
按级次计算固结参数，并用 Casagrande 几何法给出先期固结压力候选。

技术栈：Go + SQLite（`modernc.org/sqlite`，纯 Go）+ HTTP + 服务端渲染 SVG。

## 运行

```sh
go mod download
go test ./... -count=1 && go run ./cmd/server --listen 127.0.0.1:5560
```

访问 <http://127.0.0.1:5560>，页面标题为“固结路径查验台”。

可选参数：

- `--db consolidation.db`：SQLite 文件路径（默认 `consolidation.db`）。
- `--import run-record.json`：启动时先导入一份导出的运行记录再服务。

数据库为空时自动载入固定 fixture（`FIX-01`，见下文），因此清空数据库
文件后重启即可重放初始状态。

## 数据口径

四类数据严格分开，页面分区展示：

| 类别 | 内容 | 可否修改 |
| --- | --- | --- |
| 原始读数 | `readings` 表：seq、级次、压力、时钟、位移计读数、备注 | 不可变，只增不改 |
| 修正值 | 由原始读数派生：累计位移（重置补偿）、试样高度、孔隙比 e | 只读，随算随得 |
| 绘图几何 | 荷载路径、e-log(p) 曲线、首次加载分支、Casagrande 辅助线 | 只读，随算随得 |
| 人工选择 | 级次边界修正、拟合窗、曲率点候选 | 用户可改，单独存表 |

关键约定：

- **真实路径**：荷载路径严格按记录序号 seq 连接，绝不按压力重排；
  卸载/再加载分支因此不会混入首次加载曲线。e-log(p) 全路径同样按
  seq 顺序连线，滞回环可见；首次加载分支（`kind=load`）单独高亮并
  按压力排序，用于拟合 virgin 压缩线。
- **对数轴**：只有 p > 0 的点进入 e-log(p) 对数轴；零荷载初始状态
  （p = 0）保留在荷载路径图与读数表中，并在 e-log(p) 图下注明被跳过的
  seq。
- **分段诊断**：级次内检测到时钟回退（`elapsed_s` 变小）或位移计重置
  （读数突降超过 1 mm 或备注 `RESET`）时自动分段。各段独立拟合、独立
  成线，不拼接成虚假趋势；重置点前后可通过读数表反查原始位移计读数。
- **修正公式**：重置处累计位移连续补偿；孔隙比由厚度修正得到
  `e = e0 - (1+e0)·ΔH/H0`。
- **固结参数**：√t 法（Taylor，含 1.15 系数，Tv90=0.848）与 lg t 法
  （Casagrande，Tv50=0.197），拟合窗由用户在秒级时间轴上指定，
  窗外读数不参与拟合。排水路径取分段平均高度（双面排水取半）。
- **先期固结压力**：Casagrande 几何法——候选曲率点处作水平线与切线的
  角平分线，与 virgin 线（候选点以上首次加载分支拟合）求交得 pc。
  可保留多个曲率点候选；候选不唯一时页面显示 pc 范围（min–max）并
  在图上以色带标出。

## 固定 fixture

`internal/fixture` 生成确定性演示 run `FIX-01`（H0=20 mm，e0=1.0，
双面排水，cv=2.0 m²/yr，Cc=0.35，Cs=0.05）：

- 荷载路径：0 → 12.5 → 25 → 50 → 100 → 200 → 400（首次加载）
  → 100 → 25（卸载）→ 100 → 200 → 400 → 800（再加载并延伸 virgin 线）。
- 400 kPa 首次加载级中段（30 min 读数处）位移计重置归零，备注 `RESET`。
- 50 kPa 级内 30 min 读数处时钟回退 20 min。

所有读数由闭式公式生成，重放结果逐字节稳定。

## 重放与复核

- **导出**：页面“运行记录”区或 `GET /export` 下载运行记录 JSON
  （原始读数 + 级次修正 + 拟合窗 + 曲率点候选）。
- **清空重导**：页面“清空并重新载入 fixture”按钮（`POST /reset`）清空
  数据库并重新播种；随后用“导入并覆盖”（`POST /import`）或
  `go run ./cmd/server --import run-record.json` 重新导入导出文件复核。
  导入是整库替换，导入后再次导出应与原记录一致（时间戳除外）。
- **命令行复核**：

  ```sh
  curl -s http://127.0.0.1:5560/export -o run-record.json
  # 清空数据库后重新导入
  curl -s -X POST http://127.0.0.1:5560/reset
  curl -s -X POST --data-binary @run-record.json \
       -H 'Content-Type: application/json' http://127.0.0.1:5560/import
  ```

## 自动化测试

`go test ./... -count=1` 覆盖：

- `internal/consol`：分段（重置/回退）、重置补偿、路径保序、对数轴
  正压过滤、√t/lg t 拟合反演已知 cv、Casagrande pc 范围。
- `internal/fixture`：fixture 异常点可检测、路径顺序、首次加载分支纯净。
- `internal/store`：播种幂等、级次修正不动原始读数、导出/导入往返一致。
- `internal/web`：页面要素、候选→pc 范围、拟合窗、HTTP 导出/重置/导入。

## 目录结构

```
cmd/server/        入口（--listen/--db/--import）
internal/consol/   领域逻辑（纯函数，无 IO）
internal/fixture/  固定演示数据
internal/store/    SQLite 持久化
internal/web/      HTTP 处理、SVG 渲染、页面模板
```
