package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateJobIdempotent(t *testing.T) {
	s := open(t)
	j1, err := s.CreateJob("hongguo:123", "剧名", "2025", 0, map[int]string{1: "v1", 2: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	// 同剧再来：更新选集（补第 3 集），不新建任务
	j2, err := s.CreateJob("hongguo:123", "剧名", "2025", 0, map[int]string{1: "v1", 3: "v3"})
	if err != nil {
		t.Fatal(err)
	}
	if j1.ID != j2.ID {
		t.Fatalf("same drama should reuse job: %d vs %d", j1.ID, j2.ID)
	}
	eps, err := s.Episodes(j2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 || eps[0].VID != "v1" || eps[2].VID != "v3" {
		t.Fatalf("episodes = %+v", eps)
	}
}

func TestJobStatusFlow(t *testing.T) {
	s := open(t)
	j, _ := s.CreateJob("hongguo:1", "t", "", 0, map[int]string{1: "v"})
	if err := s.SetJobStatus(j.ID, JobRunning); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobStatus(j.ID, "bogus"); err == nil {
		t.Fatal("invalid status accepted")
	}
	got, _ := s.GetJob("hongguo:1")
	if got.Status != JobRunning {
		t.Fatalf("status = %s", got.Status)
	}
	if err := s.SetJobStatus(9999, JobDone); err == nil {
		t.Fatal("missing job accepted")
	}
}

func TestEpisodeStatusAndRetries(t *testing.T) {
	s := open(t)
	j, _ := s.CreateJob("hongguo:2", "t", "", 0, map[int]string{1: "v"})
	for _, st := range []string{EpDownloading, EpMerging, EpFailed, EpPending, EpFailed, EpDone} {
		if err := s.SetEpisodeStatus(j.ID, 1, st); err != nil {
			t.Fatal(err)
		}
	}
	eps, _ := s.Episodes(j.ID)
	if eps[0].Retries != 2 || eps[0].Status != EpDone {
		t.Fatalf("retries/status = %d/%s", eps[0].Retries, eps[0].Status)
	}
}

func TestEpisodeMeta(t *testing.T) {
	s := open(t)
	j, _ := s.CreateJob("hongguo:3", "t", "", 0, map[int]string{1: "v"})
	if err := s.SaveEpisodeMeta(j.ID, 1, "cenc_key", "aabb"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEpisodeMeta(j.ID, 1, "cenc_key", "ccdd"); err != nil {
		t.Fatal(err)
	}
	v, err := s.EpisodeMeta(j.ID, 1, "cenc_key")
	if err != nil || v != "ccdd" {
		t.Fatalf("meta = %q err=%v", v, err)
	}
	if _, err := s.EpisodeMeta(j.ID, 1, "missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := s.DeleteEpisodeMeta(j.ID, 1, "cenc_key"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EpisodeMeta(j.ID, 1, "cenc_key"); err != ErrNotFound {
		t.Fatal("delete failed")
	}
}

func TestSettingsAndDeleteCascade(t *testing.T) {
	s := open(t)
	if err := s.SetSetting("media_dir", "/media"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Setting("media_dir"); v != "/media" {
		t.Fatalf("setting = %q", v)
	}
	j, _ := s.CreateJob("hongguo:4", "t", "", 0, map[int]string{1: "v"})
	_ = s.SaveEpisodeMeta(j.ID, 1, "k", "v")
	if err := s.DeleteJob("hongguo:4"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetJob("hongguo:4"); err != ErrNotFound {
		t.Fatal("job not deleted")
	}
	if _, err := s.EpisodeMeta(j.ID, 1, "k"); err != ErrNotFound {
		t.Fatal("meta should cascade")
	}
}

func TestListJobsOrder(t *testing.T) {
	s := open(t)
	for _, id := range []string{"hongguo:a", "hongguo:b", "hongguo:c"} {
		s.CreateJob(id, "t", "", 0, map[int]string{1: "v"})
	}
	jobs, _ := s.ListJobs()
	if len(jobs) != 3 || jobs[0].DramaID != "hongguo:c" {
		t.Fatalf("order wrong: %+v", jobs)
	}
}
