package repo

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"bizverse/api/internal/domain"
)

type MediaRepo struct{ pool pooler }

const mediaCols = `id, uploader_id, kind, original_name, mime, size, width, height, path, created_at`

func (r *MediaRepo) Create(ctx context.Context, m *domain.MediaItem) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO media (id, uploader_id, kind, original_name, mime, size, width, height, path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		m.ID, m.UploaderID, string(m.Kind), m.OriginalName, m.Mime, m.Size, m.Width, m.Height, m.Path)
	return err
}

func (r *MediaRepo) GetByID(ctx context.Context, id string) (*domain.MediaItem, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+mediaCols+` FROM media WHERE id = $1`, id)
	var m domain.MediaItem
	if err := row.Scan(&m.ID, &m.UploaderID, &m.Kind, &m.OriginalName, &m.Mime,
		&m.Size, &m.Width, &m.Height, &m.Path, &m.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}
