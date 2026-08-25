package repo

import (
	"context"
	"encoding/json"

	"bizverse/api/internal/security"
)

type PushRepo struct{ pool pooler }

func (r *PushRepo) Subscribe(ctx context.Context, userID string, sub security.PushSubscription, ua string) error {
	keys, err := json.Marshal(sub.Keys)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO push_subscriptions (id, user_id, endpoint, keys, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET last_seen_at = now()`,
		newUUID(), userID, sub.Endpoint, keys, ua)
	return err
}

func (r *PushRepo) Unsubscribe(ctx context.Context, userID, endpoint string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE user_id=$1 AND endpoint=$2`, userID, endpoint)
	return err
}

// DeleteByEndpoint prunes a dead subscription discovered during delivery
// (push services answer 404/410 once the browser drops it).
func (r *PushRepo) DeleteByEndpoint(ctx context.Context, endpoint string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM push_subscriptions WHERE endpoint=$1`, endpoint)
	return err
}

func (r *PushRepo) ByUser(ctx context.Context, userID string) ([]security.PushSubscription, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT endpoint, keys FROM push_subscriptions WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []security.PushSubscription
	for rows.Next() {
		var sub security.PushSubscription
		var keys []byte
		if err := rows.Scan(&sub.Endpoint, &keys); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(keys, &sub.Keys); err != nil {
			continue
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

// ByUsers fetches subscriptions for many users in one round trip
// (thread fan-out batching).
func (r *PushRepo) ByUsers(ctx context.Context, userIDs []string) (map[string][]security.PushSubscription, error) {
	if len(userIDs) == 0 {
		return map[string][]security.PushSubscription{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, endpoint, keys FROM push_subscriptions WHERE user_id = ANY($1)`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]security.PushSubscription{}
	for rows.Next() {
		var uid string
		var sub security.PushSubscription
		var keys []byte
		if err := rows.Scan(&uid, &sub.Endpoint, &keys); err != nil {
			continue
		}
		if err := json.Unmarshal(keys, &sub.Keys); err != nil {
			continue
		}
		out[uid] = append(out[uid], sub)
	}
	return out, rows.Err()
}


