//go:build !race

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bizverse/api/internal/testutil"
	"bizverse/api/internal/util"
)

// CleanupOrphanMedia and PruneSnapshots had no coverage: the two destructive
// paths in the service layer. A bug in either deletes live user files or
// leaderboards, silently, on a nightly timer - so every branch is covered,
// including the 24h upload grace, the 30d soft-delete window, the thumbnail
// rule, and the fail-closed scan error.

func newTestOps(t *testing.T) (*Ops, *testutil.H, string) {
	t.Helper()
	h := testutil.New(t)
	dir := t.TempDir()
	return NewOps(h.Repos, dir), h, dir
}

func opsUser(t *testing.T, h *testutil.H) string {
	t.Helper()
	return testutil.User(t, h).ID
}

func seedMedia(t *testing.T, h *testutil.H, uploader, kind, name, rel string) string {
	t.Helper()
	id := util.NewUUID()
	h.Exec(t, `INSERT INTO media (id, uploader_id, kind, original_name, mime, size, path)
		VALUES ($1, $2, $3, $4, 'image/png', 100, $5)`,
		id, uploader, kind, name, rel)
	return id
}

func writeAged(t *testing.T, path string, age time.Duration, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func mediaRowExists(t *testing.T, h *testutil.H, id string) bool {
	t.Helper()
	var n int
	if err := h.QueryRow(t, `SELECT count(*) FROM media WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPruneSnapshotsKeepsLatestPerPeriod(t *testing.T) {
	h := testutil.New(t)
	ctx := context.Background()
	owner := testutil.User(t, h)
	cat := testutil.Category(t, h, "Cafe")
	biz := testutil.Business(t, h, testutil.BusinessOpts{
		Name: "Snapshot Cafe", Category: cat, Owner: owner,
		City: "Jakarta", Country: "ID", Lat: -6.2, Lng: 106.8,
	})
	seed := func(period string, age time.Duration) {
		t.Helper()
		h.Exec(t, `INSERT INTO trend_snapshots (id, period, business_id, score, taken_at)
			VALUES ($1, $2, $3, 10, now() - ($4 * interval '1 hour'))`,
			util.NewUUID(), period, biz.ID, int(age.Hours()))
	}
	// Old non-latest rows must go, including one inside a widened window.
	seed("24h", 20*24*time.Hour)
	seed("24h", 15*24*time.Hour)
	// The latest row of a period survives no matter its age.
	seed("24h", 1*time.Hour)
	seed("7d", 20*24*time.Hour)

	o := NewOps(h.Repos, t.TempDir())
	if err := o.PruneSnapshots(ctx); err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}
	var day, week int
	if err := h.QueryRow(t, `SELECT count(*) FROM trend_snapshots WHERE period = '24h'`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	if err := h.QueryRow(t, `SELECT count(*) FROM trend_snapshots WHERE period = '7d'`).Scan(&week); err != nil {
		t.Fatal(err)
	}
	if day != 1 || week != 1 {
		t.Errorf("after prune: 24h=%d (want 1, the latest), 7d=%d (want 1, the only row)", day, week)
	}
	var latest time.Time
	if err := h.QueryRow(t, `SELECT max(taken_at) FROM trend_snapshots WHERE period = '24h'`).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if time.Since(latest) > 2*time.Hour {
		t.Errorf("the surviving 24h row is %v old; want the 1-hour-old latest", time.Since(latest).Round(time.Minute))
	}
}

func TestPruneSnapshotsFailsClosed(t *testing.T) {
	h := testutil.New(t)
	o := NewOps(h.Repos, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.PruneSnapshots(ctx); err == nil {
		t.Error("a canceled context must fail, not delete blindly")
	}
}

func TestCleanupKeepsLiveRowsAndFiles(t *testing.T) {
	o, h, dir := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "gallery", "a.png", "img/a.png")
	// Old files on purpose: without the onDisk protection below, the walk
	// would delete them, which is exactly what the guard mutations remove.
	writeAged(t, filepath.Join(dir, "img/a.png"), 48*time.Hour, "png-bytes")
	writeAged(t, filepath.Join(dir, "img/a_thumb.jpg"), 48*time.Hour, "thumb")

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if !mediaRowExists(t, h, id) {
		t.Error("a live row was deleted")
	}
	if !fileExists(filepath.Join(dir, "img/a.png")) {
		t.Error("a live file was deleted")
	}
	// The thumbnail survives only while its row does: the skip is keyed off
	// the base name in onDisk, so a reclaimed row takes its thumbnail with it
	// instead of leaking one file per deleted image forever.
	if !fileExists(filepath.Join(dir, "img/a_thumb.jpg")) {
		t.Error("the thumbnail of a live row was deleted")
	}
}

func TestCleanupDropsRowsWithoutFiles(t *testing.T) {
	o, h, _ := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "gallery", "gone.png", "img/gone.png")

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if mediaRowExists(t, h, id) {
		t.Error("a row whose file is missing was kept")
	}
}

func TestCleanupReclaimsOldSoftDeletes(t *testing.T) {
	o, h, dir := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "gallery", "old.png", "img/old.png")
	writeAged(t, filepath.Join(dir, "img/old.png"), 48*time.Hour, "png-bytes")
	writeAged(t, filepath.Join(dir, "img/old_thumb.jpg"), 48*time.Hour, "thumb")
	h.Exec(t, `UPDATE media SET deleted_at = now() - interval '31 days' WHERE id = $1`, id)

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if mediaRowExists(t, h, id) {
		t.Error("a 31-day soft-deleted row was kept past the 30-day window")
	}
	if fileExists(filepath.Join(dir, "img/old.png")) {
		t.Error("the reclaimed file was kept")
	}
	if fileExists(filepath.Join(dir, "img/old_thumb.jpg")) {
		t.Error("the reclaimed thumbnail was kept")
	}
}

func TestCleanupKeepsRecentSoftDeletes(t *testing.T) {
	o, h, dir := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "gallery", "hidden.png", "img/hidden.png")
	writeAged(t, filepath.Join(dir, "img/hidden.png"), 48*time.Hour, "png-bytes")
	// Hidden an hour ago: still reversible, so the bytes must survive even
	// though the file itself is old. Without this the file would look like an
	// orphan and "hide" would be a one-way door on the very next nightly run.
	h.Exec(t, `UPDATE media SET deleted_at = now() - interval '1 hour' WHERE id = $1`, id)

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if !mediaRowExists(t, h, id) {
		t.Error("a recently hidden row was reclaimed inside the 30-day window")
	}
	if !fileExists(filepath.Join(dir, "img/hidden.png")) {
		t.Error("the file of a recently hidden row was deleted")
	}
}

func TestCleanupOrphanFiles(t *testing.T) {
	o, _, dir := newTestOps(t)

	// A stray file older than the grace: no row will ever claim it.
	writeAged(t, filepath.Join(dir, "stray/dangling.png"), 48*time.Hour, "dangling")
	// A fresh stray file: an in-flight upload whose row commit lags the file
	// write. Deleting it would destroy an upload that is still succeeding.
	writeAged(t, filepath.Join(dir, "stray/uploading.png"), time.Hour, "uploading")
	// A stray thumbnail with no base row: the row is gone, so this goes too.
	writeAged(t, filepath.Join(dir, "stray/gone_thumb.jpg"), 48*time.Hour, "thumb")

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if fileExists(filepath.Join(dir, "stray/dangling.png")) {
		t.Error("an old stray file was kept")
	}
	if !fileExists(filepath.Join(dir, "stray/uploading.png")) {
		t.Error("a fresh stray file was deleted inside the 24h upload grace")
	}
	if fileExists(filepath.Join(dir, "stray/gone_thumb.jpg")) {
		t.Error("an orphaned thumbnail was kept")
	}
}

func TestCleanupKeepsEncryptedDocuments(t *testing.T) {
	o, h, dir := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "document_verification", "id.pdf.enc", "docs/id.pdf.enc")
	writeAged(t, filepath.Join(dir, "docs/id.pdf.enc"), 48*time.Hour, "ciphertext")

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if !mediaRowExists(t, h, id) {
		t.Error("an encrypted document row was deleted")
	}
	if !fileExists(filepath.Join(dir, "docs/id.pdf.enc")) {
		t.Error("an encrypted document file was deleted")
	}
}

func TestCleanupFailsClosedOnScanError(t *testing.T) {
	o, h, dir := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "gallery", "live.png", "img/live.png")
	writeAged(t, filepath.Join(dir, "img/live.png"), time.Hour, "png-bytes")
	writeAged(t, filepath.Join(dir, "stray/old.png"), 48*time.Hour, "stray")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.CleanupOrphanMedia(ctx); err == nil {
		t.Fatal("a failed scan must abort, not clean up from a partial row set")
	}
	// Nothing was touched: a truncated scan would make every missing row look
	// like an orphan and delete files that are still referenced.
	if !mediaRowExists(t, h, id) {
		t.Error("a row was deleted after a failed scan")
	}
	if !fileExists(filepath.Join(dir, "img/live.png")) || !fileExists(filepath.Join(dir, "stray/old.png")) {
		t.Error("files were deleted after a failed scan")
	}
}

func TestCleanupDocumentWithoutFile(t *testing.T) {
	o, h, _ := newTestOps(t)
	u := opsUser(t, h)
	id := seedMedia(t, h, u, "document_verification", "missing.pdf.enc", "docs/missing.pdf.enc")

	if err := o.CleanupOrphanMedia(context.Background()); err != nil {
		t.Fatalf("CleanupOrphanMedia: %v", err)
	}
	if mediaRowExists(t, h, id) {
		t.Error("a document row whose file is missing was kept")
	}
}
