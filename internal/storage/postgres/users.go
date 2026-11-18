package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/subtotalstew/meeting-assistant/internal/domain"
)

// qUpsertUser создаёт пользователя или возвращает уже существующего.
//
// Один запрос вместо пары "select, затем insert": при одновременном первом
// обращении двух процессов такая пара даёт гонку и ошибку уникальности.
// ON CONFLICT DO NOTHING не переписывает строку, поэтому нет лишних версий кортежа.
const qUpsertUser = `
WITH inserted AS (
    INSERT INTO users (id, external_id)
    VALUES ($1, $2)
    ON CONFLICT (external_id) DO NOTHING
    RETURNING id, external_id, created_at
)
SELECT id, external_id, created_at FROM inserted
UNION ALL
SELECT id, external_id, created_at FROM users WHERE external_id = $2
LIMIT 1`

const qGetUserByID = `
SELECT id, external_id, created_at FROM users WHERE id = $1`

const qGetUserByExternalID = `
SELECT id, external_id, created_at FROM users WHERE external_id = $1`

// UpsertUser сохраняет пользователя и возвращает актуальную запись из базы.
func (s *Store) UpsertUser(ctx context.Context, user domain.User) (domain.User, error) {
	row := s.pool.QueryRow(ctx, qUpsertUser, user.ID, user.ExternalID)

	stored, err := scanUser(row)
	if err != nil {
		return domain.User{}, fmt.Errorf("сохранение пользователя: %w", err)
	}
	return stored, nil
}

// GetUserByID возвращает пользователя по внутреннему идентификатору.
func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, qGetUserByID, id))
	if err != nil {
		return domain.User{}, wrapNotFound(err, "пользователь")
	}
	return user, nil
}

// GetUserByExternalID возвращает пользователя по внешнему идентификатору
// (для CLI это значение флага --user).
func (s *Store) GetUserByExternalID(ctx context.Context, externalID string) (domain.User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, qGetUserByExternalID, externalID))
	if err != nil {
		return domain.User{}, wrapNotFound(err, "пользователь")
	}
	return user, nil
}

func scanUser(row rowScanner) (domain.User, error) {
	var user domain.User
	if err := row.Scan(&user.ID, &user.ExternalID, &user.CreatedAt); err != nil {
		return domain.User{}, err
	}
	return user, nil
}
