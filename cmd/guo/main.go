// guo 下载管线 CLI：在 Web UI 之前先提供可实测的二进制入口。
//
//	guo add <seriesID> [集号...]   建任务（空集号=整剧），立即写剧集级产物
//	guo run                        排空下载队列
//	guo list                       列任务与分集状态
//	guo pause|resume|retry <seriesID>
//	guo delete <seriesID> [--keep-video]
//	guo serve                     启动 Web 服务（API + 前端）
//
// 环境变量：GUO_MEDIA_DIR（默认 ./media）、GUO_DB（默认 ./guo.db）、
// GUO_JELLYFIN_URL、GUO_JELLYFIN_KEY、GUO_CONCURRENCY（默认 2）、
// GUO_ADDR（serve 监听地址，默认 :8080）。
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/hongguo/rankings"
	"github.com/2017fighting/guo/internal/jellyfin"
	"github.com/2017fighting/guo/internal/pipeline"
	"github.com/2017fighting/guo/internal/server"
	"github.com/2017fighting/guo/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func normalizeDramaID(id string) string {
	if !strings.HasPrefix(id, "hongguo:") {
		return "hongguo:" + id
	}
	return id
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	st, err := store.Open(env("GUO_DB", "./guo.db"))
	must(err)
	defer st.Close()

	var jf *jellyfin.Client
	if url := os.Getenv("GUO_JELLYFIN_URL"); url != "" {
		jf = &jellyfin.Client{BaseURL: url, APIKey: os.Getenv("GUO_JELLYFIN_KEY")}
	}

	source := hongguo.NewClient()

	engine := &pipeline.Engine{
		Store:     st,
		Source:    source,
		MediaRoot: env("GUO_MEDIA_DIR", "./media"),
		FFMpeg:    &pipeline.ExecRunner{Path: env("GUO_FFMPEG", "ffmpeg")}, // P0 修复：接真实 ffmpeg
		ASSExport: assExportSetting(st),
		Jellyfin:  jf,
		Log:       func(m string) { fmt.Fprintln(os.Stderr, "[guo]", m) },
	}
	if n, err := strconv.Atoi(env("GUO_CONCURRENCY", "2")); err == nil {
		if n < 1 {
			n = 1
		}
		if n > 6 {
			n = 6 // #7 决议：可配 1–6
		}
		engine.Concurrency = n
	}
	// 热读取接缝：每次领取任务/导出分集前重读 settings 表（表值优先，
	// 环境变量兑底——与启动口径一致），设置页保存后无需重启进程。
	engine.SettingsLookup = func() pipeline.HotSettings {
		s := server.LoadSettings(st)
		return pipeline.HotSettings{Concurrency: s.Concurrency, ASSExport: s.AssExport}
	}

	ctx := context.Background()
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "add":
		mustArg(args, 1, "add <seriesID> [-q 画质档] [分集号...]")
		quality := 0
		var eps []int
		prev := ""
		for _, a := range args[1:] {
			if a == "-q" {
				prev = a
				continue
			}
			n, err := strconv.Atoi(a)
			must(err)
			// -q 后的第一个数字是画质档，其余为分集号
			if quality == 0 && prev == "-q" {
				quality = n
			} else {
				eps = append(eps, n)
			}
			prev = a
		}
		job, err := engine.AddJob(ctx, args[0], eps, quality)
		must(err)
		fmt.Printf("任务已建：%s（#%d，画质档 %d）→ %s\n", job.Title, job.ID, job.Quality, engine.MediaRoot)
	case "run":
		must(engine.Run(ctx))
		fmt.Println("队列已排空")
	case "list":
		jobs, err := st.ListJobs()
		must(err)
		for _, j := range jobs {
			eps, _ := st.Episodes(j.ID)
			done := 0
			for _, ep := range eps {
				if ep.Status == store.EpDone {
					done++
				}
			}
			fmt.Printf("#%-3d %-8s %-40s %d/%d 集  %s\n", j.ID, j.Status, j.Title, done, len(eps), j.DramaID)
		}
	case "pause", "resume", "retry":
		mustArg(args, 1, cmd+" <seriesID>")
		job, err := st.GetJob(normalizeDramaID(args[0]))
		must(err)
		switch cmd {
		case "pause":
			must(engine.PauseJob(job.ID))
		case "resume":
			must(engine.ResumeJob(job.ID))
		case "retry":
			must(engine.RetryJob(job.ID))
		}
		fmt.Println(cmd, "OK")
	case "delete":
		mustArg(args, 1, "delete <seriesID> [--keep-video]")
		keep := len(args) > 1 && args[1] == "--keep-video"
		must(engine.DeleteJob(normalizeDramaID(args[0]), keep))
		fmt.Println("deleted")
	case "serve":
		// Web 服务：API + 前端（web/dist 存在则伺服；embed 接线在容器化工单）。
		// 引擎常驻：JobRunner 事件驱动排空队列（含断电重启续传），SSE 推队列事件。
		var static fs.FS
		if _, err := os.Stat("web/dist/index.html"); err == nil {
			static = os.DirFS("web/dist")
		}
		addr := env("GUO_ADDR", ":8080")

		runner := pipeline.NewJobRunner(engine)
		srv := &server.Server{
			Catalog: source, Drama: source, Downloads: runner,
			Streams: source, Danmaku: source,
			Rankings: rankings.NewCache(rankings.NewClient(), st), Search: source,
			Settings: st, Static: static,
		}
		hub := server.NewEventHub(srv.QueueSnapshot)
		srv.Events = hub
		engine.OnEvent = hub.Signal

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		runner.Start(ctx) // 启动即排空遗留队列（含 running 复位续传）

		httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}
		go func() {
			<-ctx.Done()
			fmt.Fprintln(os.Stderr, "[guo] 收到退出信号，正在收尾…")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := httpSrv.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "[guo] HTTP 收尾超时: %v\n", err)
			}
		}()
		fmt.Fprintf(os.Stderr, "[guo] HTTP 服务已启动 %s（API /api/v1，下载引擎常驻）\n", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			must(err)
		}
		runner.Stop() // 等在跑任务到分集边界退出
	default:
		usage()
	}
}

func mustArg(args []string, n int, usageLine string) {
	if len(args) < n {
		fmt.Fprintln(os.Stderr, "用法: guo", usageLine)
		os.Exit(2)
	}
}

// assExportSetting：settings 表优先（键 ass_export），环境变量 GUO_ASS_EXPORT 兜底，默认开。
func assExportSetting(st *store.Store) bool {
	if v, err := st.Setting("ass_export"); err == nil {
		return v == "1" || v == "true"
	}
	return env("GUO_ASS_EXPORT", "1") != "0"
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法: guo <command> [args]
  add <seriesID> [集号...]   建下载任务（空=整剧）
  run                        排空队列
  list                       列任务
  pause|resume|retry <seriesID>
  delete <seriesID> [--keep-video]
  serve                      启动 Web 服务（GUO_ADDR，默认 :8080）`)
	os.Exit(2)
}
