package service

import (
	"context"
	"log/slog"
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

// softDeleteReclaimWindow is how long a moderation-hidden file is kept before
// its bytes are reclaimed. Soft delete exists to make "hide" reversible, so the
// window has to be long enough for an operator to notice a mistake and restore
// the content; the row is hard-deleted at the end of it so the table does not
// accumulate dead references.
const softDeleteReclaimWindow = 30 * 24 * time.Hour

// CleanupOrphanMedia deletes media rows whose file is missing, and files in
// the media directory with no matching row. Soft-deleted rows (moderation
// hide) keep their bytes for softDeleteReclaimWindow and are then reclaimed.
// Documents are skipped when encrypted (.enc) or missing the row is still
// removed. Best-effort.
func (o *Ops) CleanupOrphanMedia(ctx context.Context) error {
	rows, err := o.repos.Query(ctx, `SELECT id, path, deleted_at FROM media`)
	if err != nil {
		return err
	}
	type mrow struct {
		id        string
		path      string
		deletedAt *time.Time
	}
	var all []mrow
	for rows.Next() {
		var r mrow
		if err := rows.Scan(&r.id, &r.path, &r.deletedAt); err != nil {
			rows.Close()
			// A truncated/errored scan must never feed the destructive walk below:
			// missing rows would look like orphans and their files would be deleted.
			if err := rows.Err(); err != nil {
				slog.Warn("orphan media scan aborted; skipping cleanup", "err", err)
				return err
			}
			return err
		}
		all = append(all, r)
	}
	rows.Close()

	now := time.Now()
	onDisk := map[string]bool{}
	for _, r := range all {
		full := filepath.Join(o.mediaDir, r.path)
		if _, err := os.Stat(full); err != nil {
			// Row without a file → drop the row.
			_, _ = o.repos.Exec(ctx, `DELETE FROM media WHERE id = $1`, r.id)
			continue
		}
		// A hidden-but-still-reversible row protects its own bytes: without this
		// the file would look like an orphan and be deleted on the very next
		// nightly run, which would make "hide" a one-way door.
		if r.deletedAt != nil && now.Sub(*r.deletedAt) > softDeleteReclaimWindow {
			if _, err := o.repos.Exec(ctx, `DELETE FROM media WHERE id = $1`, r.id); err != nil {
				slog.Warn("soft-deleted media row not reclaimed", "id", r.id, "err", err)
				continue
			}
			// Row is gone, so protect nothing: the walk below reclaims the file
			// and its thumbnail once the 24h file grace has passed.
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
		// A thumbnail survives only while its row does. Keying the skip off
		// onDisk (rather than the suffix alone) is what lets a reclaimed row
		// take its thumbnail with it; skipping every _thumb.jpg unconditionally
		// leaked one file per deleted image forever.
		if strings.HasSuffix(rel, "_thumb.jpg") {
			if onDisk[strings.TrimSuffix(rel, "_thumb.jpg")] {
				return nil
			}
		}
		if !onDisk[rel] && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(path)
		}
		return nil
	})
	return err
}
