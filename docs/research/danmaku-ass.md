# 弹幕转 ASS 字幕调研（红果 → Jellyfin 外挂字幕）

调研对象与来源说明（均为只读参考，未做任何修改）：

- **DanmuBridge**：`/home/zhao/clone/DanmuBridge`（Python，B 站弹幕 → ASS → Jellyfin）。注意：**DanmuBridge 自身不生成 ASS**，它在首次运行时下载第三方脚本 `danmaku2ass`（m13253/danmaku2ass，GPL-3.0）并以子进程方式调用（见 `src/danmubridge/fetch.py :: ensure_danmaku2ass()`、`convert_xml_to_ass()`）。因此"ASS 头部/事件/防碰撞"的真实实现全部位于 danmaku2ass。本仓库本机缓存中没有该依赖，我按 `ensure_danmaku2ass()` 使用的同一 URL（`https://codeload.github.com/m13253/danmaku2ass/zip/refs/heads/master`）取到 master 快照 `danmaku2ass.py`（1023 行）到 /tmp 只读检视，下文以 `danmaku2ass.py :: 函数名()` 引用（行号以该快照为准，上游更新可能漂移）。
- **guoapp**：`/home/zhao/clone/guoapp`（Flutter + Go native 核心），红果弹幕的拉取、解析与客户端渲染。

---

## 1. DanmuBridge 转换管线拆解

### 1.1 管线总览（DanmuBridge 自身部分）

`sync.py :: run()` 把三步串起来：**fetch（下载+转换）→ attach（复制到 Jellyfin）**。`cli.py :: build_parser()` 暴露 `fetch` / `attach` / `sync` 三个子命令。

fetch 流程（`fetch.py :: generate_danmaku_ass()`）：

1. `ensure_danmaku2ass()`：若缓存目录（`~/.cache/danmubridge/danmaku2ass/`，Windows 为 `%LOCALAPPDATA%`）没有 `danmaku2ass.py`，从 codeload.github.com 下载 master zip 并解压。
2. `fetch_episode_cids(season_id)`：请求 `http://bangumi.bilibili.com/web_api/get_ep_list?season_id=<id>`，按集取出 `cid` 列表。
3. 逐集 `download_xml(cid, path)`：下载 `https://comment.bilibili.com/{cid}.xml`（B 站弹幕 XML，gzip 等编码由 `download_bytes()` 处理）。
4. 逐集 `convert_xml_to_ass()`：子进程执行
   ```
   danmaku2ass.py -o {n}.ass -s 1920x1080 -fn 黑体 -fs 30 -dm 8 -ds 8 {n}.xml
   ```
   参数即 DanmuBridge 的默认值：`DEFAULT_FONT = "黑体"`、`DEFAULT_FONT_SIZE = 30.0`、`DEFAULT_DURATION = 8.0`（`-dm` 滚动时长、`-ds` 静止时长，这里都传 8）。舞台固定 1920×1080。未使用 `-a`（透明度）、`-p`（底部保留）、`-r`（满屏丢弃）、`-fl`（过滤）。
5. 输出到 `generated_danmaku_ass/ss<season_id>/<集序号>.ass`。

attach 流程（`attach.py`）：`list_source_subtitles()` + `list_videos()` 用 `natural_key()` / `episode_key()`（S01E01 优先、数字自然排序）把字幕与视频按序号一一配对（数量不等即报错），`build_destination()` 生成 Jellyfin 外挂字幕命名 `<视频名>.<lang><.ass>`（`--lang` 默认 `jpn`，伪装成日语字幕），`copy_subtitles_to_jellyfin()` 复制（已存在需 `--replace`）。

### 1.2 弹幕格式解析（由 danmaku2ass 完成）

- `danmaku2ass.py :: ProbeCommentFormat()`：按文件头魔数嗅探格式（Bilibili/Bilibili2/Acfun/Niconico/Tudou/DanDanPlay/MioMio 等）。
- B 站 XML 由 `ReadCommentsBilibili()` 解析：`<d p="出现秒,模式,字号,颜色,发送unixts,...">文本</d>`。模式映射 `{'1': 0, '4': 2, '5': 1, '6': 3}`：1=滚动→0、4=底部→2、5=顶部→1、6=逆向→3；模式 7=定位弹幕（`WriteCommentBilibiliPositioned()`）、8=脚本弹幕（忽略）。字号按 25 为基准缩放：`size = int(p[2]) * fontsize / 25.0`；颜色 `int(p[3])` 是十进制 `0xRRGGBB`。
- 解析结果统一为元组 `(timeline, timestamp, no, comment, pos, color, size, height, width)`，其中 `height = (行数+1)*size`、`width = CalculateLength(comment)*size`（`CalculateLength()` 取最长行的**字符数**，即 CJK 每字 1×字号、近似估宽）。`ReadComments()` 最后 `comments.sort()`，按 timeline（出现时间）排序——布局算法依赖这个时间序。
- ⚠️ 文件头注释声称 "1 for bottom centered, 2 for top centered"，但代码实际相反（见下），以代码为准。

### 1.3 ASS 头部与样式（`danmaku2ass.py :: WriteASSHead()`）

- `[Script Info]`：`ScriptType: v4.00+`、`PlayResX/PlayResY` = 舞台尺寸（DanmuBridge 场景为 1920/1080）、`Aspect Ratio: 1920:1080`、`Collisions: Normal`、`WrapStyle: 2`（不自动换行）、`ScaledBorderAndShadow: yes`、`YCbCr Matrix: TV.601`。
- `[V4+ Styles]` 仅一条样式，样式名随机 `Danmaku2ASS_%04x`（`ProcessComments()` 生成，防多文件合并时撞名）：
  ```
  Style: <styleid>, <fontface>, <fontsize>.0, &H<AA>FFFFFF, &H<AA>FFFFFF, &H<AA>000000, &H<AA>000000, 0, 0, 0, 0, 100, 100, 0.00, 0.00, 1, <outline>, 0, 7, 0, 0, 0, 0
  ```
  即：主色/次色=白色、描边色/阴影色=黑色，`AA = 255 - round(alpha*255)`（DanmuBridge 不传 `-a`，alpha=1.0 → `00` 全不透明）；`BorderStyle=1`（描边+阴影）；描边宽 `Outline = max(fontsize/25.0, 1)`（fs=30 → 1px）；`Shadow=0`；`Alignment=7`（左上锚点，`\move`/`\pos` 坐标即文本左上角）；页边距全 0；`Encoding=0`。
- 输出文件在 `Danmaku2ASS()` 中以 `encoding='utf-8-sig', newline='\r\n'` 打开：**UTF-8 BOM + CRLF**。

### 1.4 事件写法（`danmaku2ass.py :: WriteComment()`）

统一模板：`Dialogue: 2,<start>,<end>,<styleid>,,0000,0000,0000,,{<styles>}<text>`（Layer=2；Name/MarginL/R/V/Effect 留空；定位弹幕走 Layer=-1 的另一套写法）。按 pos 四种：

| pos | 类型 | 覆盖标签 | 时长 |
| --- | --- | --- | --- |
| 0 | 滚动（右→左） | `\move(1920, row, -ceil(w), row)` | `duration_marquee`（-dm，DanmuBridge=8s） |
| 3 | 逆向（左→右） | `\move(-ceil(w), row, 1920, row)` | `duration_marquee` |
| 1 | 顶部居中 | `\an8\pos(width/2, row)` | `duration_still`（-ds，DanmuBridge=8s） |
| 2 | 底部居中 | `\an2\pos(width/2, height-bottomReserved-row)`（`ConvertType2()`） | `duration_still` |

- 滚动语义：文本左上角从 x=舞台宽 移到 x=-文本宽，即**每条弹幕恰好用满 D 秒走完 (W+w) 距离**——越长的弹幕速度越快。
- 覆盖字号：`|size - fontsize| ≥ 1` 时追加 `\fs%.0f`；覆盖颜色：非纯白时追加 `\c&H<ConvertColor>&`，纯黑再补 `\3c&HFFFFFF&`（黑字白描边保可见）。
- 文本转义 `ASSEscape()`：`\` → `\<U+200B>`、`{` → `\{`、`}` → `\}`、换行 → `\N`、首尾空格/Tab 前后垫 U+200B（防 libass 吞空白）。
- 颜色换算 `ConvertColor()`：`0xRRGGBB` → ASS 的 **BBGGRR** 顺序；非纯黑/纯白还要做一次 BT.601→BT.709 矩阵换算（注释解释：VobSub 用 601，头部声明 `YCbCr Matrix: TV.601`）。红果场景无颜色字段，可以整个跳过这块。
- 时间戳 `ConvertTimestamp()`：秒 → **四舍五入到厘秒**，格式 `H:MM:SS.CC`（如 `0:01:23.45`）。

### 1.5 防碰撞 / 布局策略（`danmaku2ass.py :: ProcessComments()`）

布局在写文件前离线完成，核心是**逐像素行占用表**：`rows = [[None]*(height-bottomReserved+1) for i in range(4)]`——4 种 pos 各一张表，每个元素代表 1px 高的行、存占用它的弹幕元组。对每条（已按时间排序的）弹幕：

1. 从 row=0 向上扫描，`TestFreeRows()` 数出连续空闲行数；`freerows >= 文本高 height` 即占用（`MarkCommentRow()` 把 `[row, row+ceil(height))` 标记为本弹幕）并 `WriteComment()` 输出；否则 `row += freerows or 1` 跳到下一个候选区。
2. `TestFreeRows()` 的占用判定按类型分两套：
   - 静止（pos 1/2）：旧行弹幕 `old.start + duration_still > new.start` → 仍显示中，占用。
   - 滚动（pos 0/3）：两条判定，任一成立即占用：
     - `old.start > new.start - dm*(1 - W/(w_new+W))`：旧弹幕入屏太晚、还没走出右侧入屏区（阈值用**新**弹幕宽度折算）；
     - `old.start + dm*w_old/(w_old+W) > new.start`：旧弹幕还没完全进入屏幕（尾部仍在右边界外，用**旧**弹幕宽度折算）。
     两式都建立在"每条弹幕以 (W+w)/dm 的匀速跑完全程"的模型上，防的是新弹幕在右缘入屏时撞上旧弹幕、以及后发更快的弹幕追尾。
3. 扫描失败（屏幕满了）：默认走 `FindAlternativeRow()`——优先找完全空闲行，否则挑"最早入屏弹幕"所在行**重叠放置**（视觉上双层，libass 的 `Collisions: Normal` 也会兜底堆叠）；若命令行开了 `-r/--reduce` 则直接丢弃该条（DanmuBridge 未开启）。

另：guoapp 的 Flutter 端播放器里有一套**运行时**泳道调度可对照参考——`guoapp/lib/danmaku_overlay.dart :: planFlights()`：1–4 条泳道、近 8s 内超 24 条即丢、文本截 100 字、追尾判定 `(width + prev.width) * gap / lifetime - prev.width >= 24 && ...`（同样是 (W+w)/lifetime 速度模型，多留 24px 安全边距）；渲染白字黑影（`danmaku_overlay.dart` 第 113–116 行）。

---

## 2. 红果弹幕数据结构（guoapp 侧）

### 2.1 拉取入口与请求

- UI 路由：`native/core/app_runtime.go` 中 `case "danmaku"` → `nativeDanmaku()`；播放身份在 `nativeOpenPlayback` 时由 `ui_playback_danmaku.go :: hongguoPlaybackIDs(task)` 确定——要求 `DramaID` 为 `hongguo:<seriesID>`、`Chapter.Source == hongguo` 且 `Chapter.VideoURL` 形如 `hongguo-cenc://<纯数字 videoID>`，返回 `(seriesID, videoID)` 存入播放会话（`app_runtime.go:794`），并经 `app_playback_routes.go:77` 的 `DanmakuID` 下发。
- 会话校验：`native/core/app_danmaku.go :: nativeDanmaku()`——`0 ≤ StartMS < DurationMS ≤ 24h`（`danmakuMaxDurationMS`），播放会话须存在且未超 12h、本集有弹幕 ID。
- 请求：`ui_playback_danmaku.go :: (Downloader).hongguoDanmaku()`，`POST /novel/commentapi/comment/list/<videoID>/v1/`（经 `hongguoAppRequest()`，需 X-Argus/X-Ladon/X-Gorgon/X-Khronos 等签名头与 `Comment-Source: 601`、`Server-Channel: 1000` 头，匿名无 Cookie、后台低优先级；见 `app_danmaku_test.go` 的断言）。请求体：
  ```json
  {"comment_source":601,"server_channel":1000,"group_id":"<videoID>","group_type":30,
   "comment_type":20,"sort":1,"count":90,"cursor":"","aid":8662,"compliance_status":0,
   "business_param":{"book_id":"<seriesID>","start_offset_time":<start ms>,
                     "playlet_item_duration":<duration ms>,"need_danmaku_guide_type":[1,3,4,2]}}
  ```

### 2.2 响应与归一化结构（`parseHongguoDanmaku()`）

响应取 `data.data_list[]`，每行的有效字段路径：

| 字段 | 路径 | 含义 |
| --- | --- | --- |
| 弹幕 ID | `data_list[].comment.comment_id` | 去重键（≤120 字符） |
| 文本 | `data_list[].comment.common.content.text` | 原文 |
| 出现时间 | `data_list[].comment.expand.offset_time` | **毫秒字符串**，距本集片头的偏移 |
| 归属校验 | `data_list[].comment.common.group_id` | 必须 == 本集 videoID，否则是串集数据，丢弃 |
| 可见性 | `data_list[].comment.common.status` | 必须等于 `"1"`；`mapString()` 会把数字 1 也转成 `"1"` 放行，其余值（如 2=隐藏）丢弃 |
| 下页游标 | `data.extra.next_query_danmaku_list_time` | 下一 30s 窗口的 `start_offset_time`（毫秒） |
| 总数 | `data.common_list_info.cursor`（JSON 串）→ `danmaku_count` | 全集弹幕总数 |

归一化输出（`ui_playback_danmaku.go` 第 19–30 行）：

```go
type hongguoDanmakuItem struct { ID string; Text string; TimeMS int64 }   // json: id / text / timeMs
type hongguoDanmakuPage  struct { EpisodeID string; Items []hongguoDanmakuItem;
                                  StartMS, NextMS, Total int64 }           // json: episodeId/items/startMs/nextMs/total
```

净化与收敛规则（`parseHongguoDanmaku()`）：控制字符与 U+2028/U+2029 → 空格后 `TrimSpace`；超 180 runes 截断加 `…`；`TimeMS ∈ [start, NextMS)` 越界丢弃；`comment_id` 去重；每页最多 90 条；最终按 `TimeMS` 稳定排序。Dart 侧 `lib/danmaku_models.dart :: DanmakuPage.fromJson()` 对 bridge 返回做同规则二次校验（含 `[\x00-\x1f\x7f-\x9f\u2028\u2029]` 清洗、181 runes 截断），双端一致。

### 2.3 时间基准与分页模型

- **时间基准**：`offset_time` 是"距本集开始"的毫秒偏移，不是窗口相对时间——分页只是按 30s 窗口（`danmakuWindowMS = 30_000`，Dart `danmakuWindowMs = 30000`）切片拉取，条目的 `TimeMS` 始终可直接用作全集时间轴。
- 窗口推进：`NextMS = min(next_query_danmaku_list_time, duration)`；调用方拿 `NextMS` 作为下一页的 `start_offset_time`，直到 `next ≥ duration`（最后一窗被钳到片长）。`next <= start` 或 `next > 24h` 视为响应损坏直接报错。
- **弹幕类型标记：没有**。红果接口在 guoapp 的用法里不产出模式/颜色/字号/用户等任何样式字段，全部条目按"普通白色滚动弹幕"对待；客户端渲染（`danmaku_overlay.dart`）也只画白字黑影的单向滚动，生命周期 `danmakuLifetimeMs = 8000`（与 DanmuBridge 的 `-dm 8` 恰好一致）。
- 缓存与并发（`hongguoDanmaku()`）：键 `series:video:start:duration`；成功缓存 5 分钟、失败 15 秒、容量 128 条清空重建；同键请求单飞合并（pending 上限 16）；返回深拷贝（`cloneDanmakuPage()`）防调用方污染缓存。

---

## 3. 两者对接：红果 → ASS 的映射与 Go 实现要点

### 3.1 字段映射表

前提：红果弹幕无类型/颜色/字号，因此映射是"全量滚动弹幕"，比 B 站场景简单得多——只需要 pos=0 一条路径。

| 红果侧（guoapp 结构） | ASS Dialogue 侧 | 说明 |
| --- | --- | --- |
| `hongguoDanmakuItem.TimeMS`（ms） | `Start = ConvertTimestamp(TimeMS/1000)`；`End = ConvertTimestamp(TimeMS/1000 + 8)` | 厘秒四舍五入、格式 `H:MM:SS.CC`；8s 可配，建议默认 8（对齐 guoapp `danmakuLifetimeMs` 与 DanmuBridge `-dm`） |
| `Text`（已净化） | Dialogue Text，经 Go 版 `ASSEscape()` | `{`/`}`/`\`/换行必须转义；guoapp 只清洗了控制字符，`{}` 原样保留（ASS 标签注入风险要在转换器处理） |
| `ID` | 不写入 ASS | 用于跨窗口去重（服务端分页可能重发） |
| （无类型字段） | 固定 pos=0：`{\move(PlayResX, row, -ceil(w), row)}` | 右→左滚动；PlayResX 建议 1920 |
| （无颜色字段） | 不写 `\c`，用 Style 白色 | 顺带绕开 `ConvertColor()` 的 BGR 倒序与 601→709 换算两个坑 |
| （无字号字段） | 不写 `\fs`，用 Style 字号（如 30） | 描边 `Outline = max(fs/25, 1)` |
| （需自行计算） | `row`（泳道 y 坐标） | danmaku2ass 的占用表逻辑必须移植（见 3.2） |
| — | `Layer=2`，Name/Margins/Effect 留空，Style 指向头部样式 | 照抄 `WriteComment()` 模板 |
| — | 头部整段照抄 `WriteASSHead()` | `Collisions: Normal`、`WrapStyle: 2`、`ScaledBorderAndShadow: yes`、`YCbCr Matrix: TV.601`、`Alignment=7` |

### 3.2 Go 实现要点与坑

**管线**：`for start := 0; start < duration; start = page.NextMS` 循环调 `hongguoDanmaku`（复用 guoapp 的签名请求客户端）→ 合并各窗口 Items、按 `comment_id` 去重、按 `TimeMS` 排序 → 逐条估宽/占行/写 Dialogue。guoapp 的在线播放器是"边播边拉 30s 窗口"，离线生成 ASS 必须自己把整集窗口拉完（`NextMS` 就是游标，注意最后一窗钳制在片长）。

1. **文本估宽**：danmaku2ass `CalculateLength()` 用"最长行字符数 × 字号"。Go 对应 `utf8.RuneCountInString`（切到最长行）× 字号，CJK 场景够用；千万别用 `len()`（字节数）。文本高度 = 行数 × 字号（红果文本已无换行，通常就是 1×字号）。混排大量英文时会偏窄（danmaku2ass 同样偏窄），可接受。
2. **占用表**：只需滚动一套。`rows []int32`（或存弹幕索引），长度 PlayResY+1；对每条弹幕从 row=0 扫描，把 `TestFreeRows()` 的两条滚动判定原样移植：
   `old.start > new.start - dm*w_new/(w_new+W)` 或 `old.start + dm*w_old/(w_old+W) > new.start` 即占用（单位统一为秒，速度模型 (W+w)/dm px/s）。屏幕满时选 `FindAlternativeRow()`（放最早弹幕的行，重叠）或丢弃——guoapp Dart 端 `planFlights()` 的策略是丢，DanmuBridge 默认是重叠；二选一即可，建议先重叠（不丢数据）。
3. **排序先行**：占用表依赖时间序，先全局 `sort.SliceStable(byTimeMS)` 再布局（`ReadComments()` 同样先排序）。
4. **文件格式**：UTF-8 **带 BOM**（`utf-8-sig`）+ **CRLF** 行尾——`Danmaku2ASS()` 打开文件的方式；Jellyfin 走 ffmpeg/libass，照抄最稳。
5. **时间戳精度**：厘秒且**四舍五入**（`ConvertTimestamp()` 的 `round(ts*100)`），不是截断；小时位不补零（`0:00:05.00`）。
6. **转义**：`\`、`{`、`}`、换行；红果文本里可能出现 `<b>` 之类 HTML 片段（guoapp 测试确认不剥离、原样保留），在 ASS 里无害，但 `{}` 会被 libass 当标签——`ASSEscape()` 必须先行。
7. **不能直接套子进程方案**：danmaku2ass 没有 hongguo JSON 读取器（`CommentFormatMap` 里只有 B 站 XML 等）；要么先转成 B 站 XML 中转（会丢字段且多一跳，不推荐），要么用 Go 移植——核心只需 `WriteASSHead` / `WriteComment`(pos=0 分支) / `TestFreeRows` / `FindAlternativeRow` / `ConvertTimestamp` / `ASSEscape` 六个纯函数，约两百行，且 GPL-3.0 传染性需要法务确认（见风险）。
8. **服务端每页 90 条上限**（请求体 `count: 90`）：密集弹幕段落一页可能装不下同一窗口的弹幕，guoapp 在线播放可容忍（窗口实时滚过去），离线整集导出时这是**潜在的完整性缺口**——guoapp 未尝试更大的 `count` 或同窗多次分页（`cursor` 字段始终传 `""`），对接时需实测红果接口是否支持调大 `count` 或用 `cursor` 翻页。
9. **duration 必须=本集真实时长**：`playlet_item_duration` 既用于窗口钳制也是合法性校验（`start < duration`），拿不到准确时长时整集分页会错。
10. **净化规则对齐**：离线转换器应复刻 `parseHongguoDanmaku()` 的规则（控制符→空格、Trim、180 runes+`…`、`status=="1"`、`group_id` 匹配、去重），避免两套口径。
11. **Jellyfin 侧**：外挂命名 `<视频文件名>.<lang>.ass`（DanmuBridge `attach.py :: build_destination()`，默认 lang=`jpn`）；`PlayResX/Y=1920x1080` + `ScaledBorderAndShadow: yes` 让 libass 自适应任意分辨率视频；Style 字体名（如 `黑体`）需要 Jellyfin 服务器装有对应中文字体，否则 libass 回退字体可能显示异常。
12. **头部细节**：样式名建议沿用随机后缀（或至少每文件唯一）；`Alignment=7`（左上锚点）使 `\move`/`\pos` 坐标即文本左上角，与滚动公式配套，不要改成剧中对白常用的 2。

### 3.3 残留风险 / 待确认

- **许可证**：danmaku2ass 为 GPL-3.0。Go 移植其布局算法属于衍生实现，guo 后端若非 GPL 兼容需合规评估（DanmuBridge 以子进程调用同样受其约束）。
- 红果接口 `count` 上限、`cursor` 翻页语义、`sort=1` 的排序含义：guoapp 源码中无更多信息，需实测。
- 上游 danmaku2ass master 快照（本次检视版本）与未来版本可能有差异；移植以函数语义为准而非行号。
