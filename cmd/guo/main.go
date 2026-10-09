// guo 下载管线 CLI：在 Web UI 之前先提供可实测的二进制入口。
//
//	guo add <seriesID> [集号...]   建任务（空集号=整剧），立即写剧集级产物
//	guo run                        排空下载队列
//	guo list                       列任务与分集状态
//	guo pause|resume|retry <seriesID>
//	guo delete <seriesID> [--keep-video]
//
// 环境变量：GUO_MEDIA_DIR（默认 ./media）、GUO_DB（默认 ./guo.db）、
// GUO_JELLYFIN_URL、GUO_JELLYFIN_KEY、GUO_CONCURRENCY（默认 2）。
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/2017fighting/guo/internal/hongguo"
	"github.com/2017fighting/guo/internal/jellyfin"
	"github.com/2017fighting/guo/internal/pipeline"
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

	engine := &pipeline.Engine{
		Store:     st,
		Source:    hongguo.NewClient(),
		MediaRoot: env("GUO_MEDIA_DIR", "./media"),
		ASSExport: true,
		Jellyfin:  jf,
		Log:       func(m string) { fmt.Fprintln(os.Stderr, "[guo]", m) },
	}
	if n, err := strconv.Atoi(env("GUO_CONCURRENCY", "2")); err == nil {
		engine.Concurrency = n
	}

	ctx := context.Background()
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "add":
		mustArg(args, 1, "add <seriesID> [集号...]")
		var eps []int
		for _, a := range args[1:] {
			n, err := strconv.Atoi(a)
			must(err)
			eps = append(eps, n)
		}
		job, err := engine.AddJob(ctx, args[0], eps)
		must(err)
		fmt.Printf("任务已建：%s（#%d）→ %s\n", job.Title, job.ID, engine.MediaRoot)
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
  delete <seriesID> [--keep-video]`)
	os.Exit(2)
}
