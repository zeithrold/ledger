-- name: LockInstance :one
SELECT * FROM instance_state WHERE singleton = true FOR UPDATE;
-- name: GetInstance :one
SELECT * FROM instance_state WHERE singleton = true;
-- name: ClaimInstance :exec
UPDATE instance_state SET admin_user_id = $1, initialized_at = now() WHERE singleton = true AND admin_user_id IS NULL;
-- name: FindIdentity :one
SELECT user_id FROM user_identities WHERE issuer = $1 AND subject = $2;
-- name: CreateUser :exec
INSERT INTO users (id) VALUES ($1);
-- name: CreateIdentity :exec
INSERT INTO user_identities (issuer, subject, user_id) VALUES ($1, $2, $3);
-- name: CreateTenant :exec
INSERT INTO tenants (id, name) VALUES ($1, 'Personal');
-- name: CreateOwner :exec
INSERT INTO tenant_members (tenant_id, user_id) VALUES ($1, $2);
-- name: CreateBook :exec
INSERT INTO books (id, tenant_id, name, base_currency) VALUES ($1, $2, 'Main', $3);
-- name: BindPersonalTenant :exec
INSERT INTO personal_tenant_bindings (user_id, tenant_id, default_book_id) VALUES ($1, $2, $3);
-- name: CreatePreferences :exec
INSERT INTO user_preferences (user_id, locale, timezone) VALUES ($1, $2, $3);
-- name: GetUserContext :one
SELECT u.id, u.display_name, u.status, p.tenant_id, t.name AS tenant_name, t.status AS tenant_status,
 m.status AS member_status, m.role, p.default_book_id, b.name AS book_name, b.base_currency,
 pref.locale, pref.timezone, pref.theme, (s.admin_user_id = u.id)::boolean AS is_admin
FROM users u
JOIN personal_tenant_bindings p ON p.user_id = u.id
JOIN tenants t ON t.id = p.tenant_id
JOIN tenant_members m ON m.tenant_id = t.id AND m.user_id = u.id
JOIN books b ON b.tenant_id = t.id AND b.id = p.default_book_id
JOIN user_preferences pref ON pref.user_id = u.id
CROSS JOIN instance_state s
WHERE u.id = $1;
-- name: ListBooks :many
SELECT * FROM books WHERE tenant_id = $1 ORDER BY id;
-- name: GetBook :one
SELECT * FROM books WHERE tenant_id = $1 AND id = $2;
-- name: UpdatePreferences :one
UPDATE user_preferences SET locale = COALESCE(sqlc.narg(locale)::text, locale),
 timezone = COALESCE(sqlc.narg(timezone)::text, timezone), theme = COALESCE(sqlc.narg(theme)::text, theme)
WHERE user_id = $1 RETURNING *;
-- name: ListUsers :many
SELECT id, display_name, status, created_at FROM users
WHERE (sqlc.narg(after_id)::uuid IS NULL OR id > sqlc.narg(after_id)::uuid)
ORDER BY id LIMIT $1;
-- name: LockUser :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;
-- name: UpdateUserStatus :exec
UPDATE users SET status = $2, updated_at = now() WHERE id = $1;
-- name: CreateAdminAudit :exec
INSERT INTO admin_audit_events (id, actor_id, target_user_id, previous_status, new_status, reason)
VALUES ($1, $2, $3, $4, $5, $6);
