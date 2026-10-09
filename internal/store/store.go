// Package store 以 SQLite（modernc.org/sqlite 纯 Go，WAL 模式）持久化
// 下载任务队列与设置——#7 决议第 8 项。
//
// 领域模型见 CONTEXT.md：下载任务 = 剧 + 选集；本包只管状态与元数据，
// 不触碰网络与文件产物。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// 任务状态机：queued → running ⇄ paused；running → done / failed；
// failed → queued（重试）；删除为墓碑（delete 保留视频由引擎处理）。
const (
	JobQueued   = "queued"
	JobRunning  = "running"
	JobPaused   = "paused"
	JobDone     = "done"
	JobFailed   = "failed"
	JobDeleting = "deleting"
)

// 分集状态机：pending → downloading → merging → done | failed。
const (
	EpPending     = "pending"
	EpDownloading = "downloading"
	EpMerging     = "merging"
	EpDone        = "done"
	EpFailed      = "failed"
)

var ErrNotFound = errors.New("store: not found")

// Job 一个下载任务（剧级）。
type Job struct {
	ID        int64
	DramaID   string // hongguo:<seriesID>
	Title     string
	Year      string
	Quality   int // 期望画质档（0 = 最高）
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Episode 任务内的一个分集。
type Episode struct {
	JobID     int64
	Index     int // 分集序号（1 起）
	VID       string
	Status    string
	Retries   int
	UpdatedAt time.Time
}

// Store SQLite 存储；所有方法并发安全（单连接 + WAL 足够，队列写频极低）。
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）数据库并迁移 schema。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc sqlite 单写者，避免 SQLITE_BUSY
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  drama_id   TEXT NOT NULL UNIQUE,
  title      TEXT NOT NULL,
  year       TEXT NOT NULL DEFAULT '',
  quality    INTEGER NOT NULL DEFAULT 0,
  status     TEXT NOT NULL DEFAULT 'queued',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS episodes (
  job_id    INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  idx       INTEGER NOT NULL,
  vid       TEXT NOT NULL,
  status    TEXT NOT NULL DEFAULT 'pending',
  retries   INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (job_id, idx)
);
CREATE TABLE IF NOT EXISTS episode_meta (
  job_id INTEGER NOT NULL,
  idx    INTEGER NOT NULL,
  key    TEXT NOT NULL,
  value  TEXT NOT NULL,
  PRIMARY KEY (job_id, idx, key),
  FOREIGN KEY(job_id, idx) REFERENCES episodes(job_id, idx) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);`)
	return err
}

func now() int64 { return time.Now().Unix() }

// CreateJob 新建任务（幂等：同剧已存在时更新选集并返回既有任务）。
// episodes 为选中的分集序号→videoID。
func (s *Store) CreateJob(dramaID, title, year string, quality int, episodes map[int]string) (*Job, error) {
	var job Job
	err := s.withTx(func(tx *sql.Tx) error {
		row := tx.QueryRow(`SELECT id, status FROM jobs WHERE drama_id=?`, dramaID)
		var status string
		err := row.Scan(&job.ID, &status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.Exec(`INSERT INTO jobs(drama_id,title,year,quality,status,created_at,updated_at)
				VALUES(?,?,?,?,?,?,?)`, dramaID, title, year, quality, JobQueued, now(), now())
			if err != nil {
				return err
			}
			job.ID, err = res.LastInsertId()
			if err != nil {
				return err
			}
			job.Status = JobQueued
		case err != nil:
			return err
		default:
			if _, err := tx.Exec(`UPDATE jobs SET title=?, year=?, quality=CASE WHEN ?!=0 THEN ? ELSE quality END, updated_at=? WHERE id=?`,
				title, year, quality, quality, now(), job.ID); err != nil {
				return err
			}
		}
		for idx, vid := range episodes {
			if _, err := tx.Exec(`INSERT INTO episodes(job_id,idx,vid,status,updated_at)
				VALUES(?,?,?,'pending',?)
				ON CONFLICT(job_id,idx) DO UPDATE SET vid=excluded.vid`,
				job.ID, idx, vid, now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetJob(dramaID)
}

func (s *Store) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// GetJob 按剧 ID 取任务。
func (s *Store) GetJob(dramaID string) (*Job, error) {
	row := s.db.QueryRow(`SELECT id, drama_id, title, year, quality, status, created_at, updated_at
		FROM jobs WHERE drama_id=?`, dramaID)
	var j Job
	var c, u int64
	if err := row.Scan(&j.ID, &j.DramaID, &j.Title, &j.Year, &j.Quality, &j.Status, &c, &u); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	j.CreatedAt, j.UpdatedAt = time.Unix(c, 0), time.Unix(u, 0)
	return &j, nil
}

// ListJobs 全量任务（新到旧）。
func (s *Store) ListJobs() ([]Job, error) {
	rows, err := s.db.Query(`SELECT id, drama_id, title, year, quality, status, created_at, updated_at
		FROM jobs ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var c, u int64
		if err := rows.Scan(&j.ID, &j.DramaID, &j.Title, &j.Year, &j.Quality, &j.Status, &c, &u); err != nil {
			return nil, err
		}
		j.CreatedAt, j.UpdatedAt = time.Unix(c, 0), time.Unix(u, 0)
		out = append(out, j)
	}
	return out, rows.Err()
}

// SetJobStatus 更新任务状态（状态机校验：只允许合法迁移）。
func (s *Store) SetJobStatus(id int64, status string) error {
	valid := map[string]bool{
		JobQueued: true, JobRunning: true, JobPaused: true,
		JobDone: true, JobFailed: true, JobDeleting: true,
	}
	if !valid[status] {
		return fmt.Errorf("store: invalid job status %q", status)
	}
	res, err := s.db.Exec(`UPDATE jobs SET status=?, updated_at=? WHERE id=?`, status, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Episodes 列任务分集（按序号）。
func (s *Store) Episodes(jobID int64) ([]Episode, error) {
	rows, err := s.db.Query(`SELECT job_id, idx, vid, status, retries, updated_at
		FROM episodes WHERE job_id=? ORDER BY idx`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		var u int64
		if err := rows.Scan(&e.JobID, &e.Index, &e.VID, &e.Status, &e.Retries, &u); err != nil {
			return nil, err
		}
		e.UpdatedAt = time.Unix(u, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetEpisodeStatus 更新分集状态；进入 failed 时递增重试计数。
func (s *Store) SetEpisodeStatus(jobID int64, idx int, status string) error {
	q := `UPDATE episodes SET status=?, updated_at=? WHERE job_id=? AND idx=?`
	if status == EpFailed {
		q = `UPDATE episodes SET status=?, retries=retries+1, updated_at=? WHERE job_id=? AND idx=?`
	}
	res, err := s.db.Exec(q, status, now(), jobID, idx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveEpisodeMeta / EpisodeMeta / DeleteEpisodeMeta 存取分集私有元数据
// （如 CENC 密钥 hex、断点字节数）。
func (s *Store) SaveEpisodeMeta(jobID int64, idx int, key, value string) error {
	_, err := s.db.Exec(`INSERT INTO episode_meta(job_id,idx,key,value) VALUES(?,?,?,?)
		ON CONFLICT(job_id,idx,key) DO UPDATE SET value=excluded.value`, jobID, idx, key, value)
	return err
}

func (s *Store) EpisodeMeta(jobID int64, idx int, key string) (string, error) {
	row := s.db.QueryRow(`SELECT value FROM episode_meta WHERE job_id=? AND idx=? AND key=?`,
		jobID, idx, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

func (s *Store) DeleteEpisodeMeta(jobID int64, idx int, key string) error {
	_, err := s.db.Exec(`DELETE FROM episode_meta WHERE job_id=? AND idx=? AND key=?`, jobID, idx, key)
	return err
}

// Setting / SetSetting 配置键值。
func (s *Store) Setting(key string) (string, error) {
	row := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// DeleteJob 删除任务及其分集/元数据（级联）。保留视频与否由引擎在调用前处理。
func (s *Store) DeleteJob(dramaID string) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE drama_id=?`, dramaID)
	return err
}
