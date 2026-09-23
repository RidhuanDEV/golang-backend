-- name: FindActiveUserByEmail :one
SELECT id, email, password, role_id, deleted_at, created_at, updated_at
FROM users WHERE email = $1 AND deleted_at IS NULL;

-- name: FindActiveUserByID :one
SELECT id, email, password, role_id, deleted_at, created_at, updated_at
FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: UserHasPermission :one
SELECT EXISTS (
  SELECT 1 FROM users u
  JOIN role_permissions rp ON rp.role_id = u.role_id
  JOIN permissions p ON p.id = rp.permission_id
  WHERE u.id = $1 AND u.deleted_at IS NULL AND p.name = $2
);
