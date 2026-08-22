package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bizverse/api/internal/repo"
)

// Ops — housekeeping jobs (ARCHITECTURE §4): snapshot retention and
// orphaned-media cleanup. Runs daily; every operation is idempotent.
type Ops struct {
	repos    *repo.Repos
	mediaDir string
}

func NewOps(repos *repo.Repos, mediaDir string) *Ops {
	return &Ops{repos: repos, mediaDir: mediaDir}
}

// PruneSnapshots keeps 14 days of trend_snapshots per period (the leaderboard
// only ever reads the latest); the table otherwise grows 3 rows/business/10min.
func (o *Ops) PruneSnapshots(ctx context.Context) error {
	_, err := o.repos.Exec(ctx, `
		DELETE FROM trend_snapshots s
		WHERE s.taken_at < now() - interval '14 days'
		  AND s.taken_at NOT IN (
			SELECT max(taken_at) FROM trend_snapshots WHERE period = s.period)`)
	return err
}

// CleanupOrphanMedia deletes media rows whose file is missing, and files in
// the media directory with no matching row. Documents are skipped when
// encrypted (.enc) or missing the row is still removed. Best-effort.
func (o *Ops) CleanupOrphanMedia(ctx context.Context) error {
	rows, err := o.repos.Query(ctx, `SELECT id, path FROM media`)
	if err != nil {
		return err
	}
	type mrow struct{ id, path string }
	var all []mrow
	for rows.Next() {
		var r mrow
		if err := rows.Scan(&r.id, &r.path); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()

	onDisk := map[string]bool{}
	for _, r := range all {
		full := filepath.Join(o.mediaDir, r.path)
		if _, err := os.Stat(full); err != nil {
			// Row without a file → drop the row.
			_, _ = o.repos.Exec(ctx, `DELETE FROM media WHERE id = $1`, r.id)
			continue
		}
		onDisk[r.path] = true
		// Encrypted documents (stored with a .enc suffix) map to the row path.
		onDisk[r.path+".enc"] = true
	}

	// Files without rows → delete, but only after a 24h grace so an
	// in-flight upload whose row commit lags is never destroyed
	// (ARCHITECTURE §4).
	err = filepath.Walk(o.mediaDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(o.mediaDir, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, "_thumb.jpg") {
			return nil // thumbnail of a valid row
		}
		if !onDisk[rel] && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(path)
		}
		return nil
	})
	return err
}
