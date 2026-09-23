package httpapi

import (
	"context"
	"errors"
	"strings"

	"github.com/RidhuanDEV/golang-backend/internal/db"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Role(ctx context.Context, id string) (Role, error) {
	var result Role
	if err := validUUID(id); err != nil {
		return result, err
	}
	role, err := scanRole(s.DB.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM roles WHERE id=$1::uuid`, id))
	if err != nil {
		return result, dbError(err)
	}
	rows, err := s.DB.Query(ctx, `SELECT p.id::text,p.name FROM permissions p JOIN role_permissions rp ON rp.permission_id=p.id WHERE rp.role_id=$1::uuid ORDER BY p.name`, id)
	if err != nil {
		return result, internal()
	}
	defer rows.Close()
	for rows.Next() {
		var p NamedPermission
		if err = rows.Scan(&p.ID, &p.Name); err != nil {
			return result, internal()
		}
		role.Permissions = append(role.Permissions, RolePermission{p})
	}
	if rows.Err() != nil {
		return result, internal()
	}
	return role, nil
}
func (s *Service) Roles(ctx context.Context) ([]Role, error) {
	rows, err := s.DB.Query(ctx, `SELECT id::text,name,created_at,updated_at FROM roles ORDER BY name`)
	if err != nil {
		return nil, internal()
	}
	defer rows.Close()
	result := []Role{}
	for rows.Next() {
		r, e := scanRole(rows)
		if e != nil {
			return nil, internal()
		}
		result = append(result, r)
	}
	if rows.Err() != nil {
		return nil, internal()
	}
	for i := range result {
		r, e := s.Role(ctx, result[i].ID)
		if e != nil {
			return nil, e
		}
		result[i] = r
	}
	return result, nil
}
func (s *Service) CreateRole(ctx context.Context, ep Endpoint, actor *Actor, name string) (Role, error) {
	var out Role
	if err := validName(name, 64); err != nil {
		return out, err
	}
	err := s.Mutate(ctx, ep, actor, "CREATE", func(tx pgx.Tx) (string, any, any, error) {
		r, e := scanRole(tx.QueryRow(ctx, `INSERT INTO roles(name) VALUES($1) RETURNING id::text,name,created_at,updated_at`, strings.TrimSpace(name)))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = r
		return r.ID, nil, r, nil
	})
	return out, err
}
func (s *Service) UpdateRole(ctx context.Context, ep Endpoint, actor *Actor, id, name string) (Role, error) {
	var out Role
	if err := validUUID(id); err != nil {
		return out, err
	}
	if err := validName(name, 64); err != nil {
		return out, err
	}
	err := s.Mutate(ctx, ep, actor, "UPDATE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanRole(tx.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM roles WHERE id=$1::uuid FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		after, e := scanRole(tx.QueryRow(ctx, `UPDATE roles SET name=$2,updated_at=now() WHERE id=$1::uuid RETURNING id::text,name,created_at,updated_at`, id, strings.TrimSpace(name)))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = after
		return id, before, after, nil
	})
	return out, err
}
func (s *Service) DeleteRole(ctx context.Context, ep Endpoint, actor *Actor, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	return s.Mutate(ctx, ep, actor, "DELETE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanRole(tx.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM roles WHERE id=$1::uuid FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		_, e = tx.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, id)
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		return id, before, nil, nil
	})
}
func (s *Service) AssignPermissions(ctx context.Context, ep Endpoint, actor *Actor, id string, permissionIDs []string) (Role, error) {
	var result Role
	if err := validUUID(id); err != nil {
		return result, err
	}
	if len(permissionIDs) == 0 {
		return result, badRequest("permissionIds is required")
	}
	for _, p := range permissionIDs {
		if err := validUUID(p); err != nil {
			return result, err
		}
	}
	err := s.Mutate(ctx, ep, actor, "UPDATE", func(tx pgx.Tx) (string, any, any, error) {
		var name string
		if e := tx.QueryRow(ctx, `SELECT name FROM roles WHERE id=$1::uuid FOR UPDATE`, id).Scan(&name); e != nil {
			return "", nil, nil, dbError(e)
		}
		rows, e := tx.Query(ctx, `SELECT permission_id::text FROM role_permissions WHERE role_id=$1::uuid`, id)
		if e != nil {
			return "", nil, nil, internal()
		}
		before := []string{}
		for rows.Next() {
			var p string
			if e = rows.Scan(&p); e != nil {
				rows.Close()
				return "", nil, nil, internal()
			}
			before = append(before, p)
		}
		rows.Close()
		if _, e = tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1::uuid`, id); e != nil {
			return "", nil, nil, internal()
		}
		for _, permissionID := range permissionIDs {
			if _, e = tx.Exec(ctx, `INSERT INTO role_permissions(role_id,permission_id) VALUES($1::uuid,$2::uuid) ON CONFLICT DO NOTHING`, id, permissionID); e != nil {
				return "", nil, nil, dbError(e)
			}
		}
		return id, map[string]any{"name": name, "permissionIds": before}, map[string]any{"name": name, "permissionIds": permissionIDs}, nil
	})
	if err != nil {
		return result, err
	}
	return s.Role(ctx, id)
}

func (s *Service) Permission(ctx context.Context, id string) (Permission, error) {
	var result Permission
	if err := validUUID(id); err != nil {
		return result, err
	}
	p, err := scanPermission(s.DB.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM permissions WHERE id=$1::uuid`, id))
	if err != nil {
		return result, dbError(err)
	}
	return p, nil
}
func (s *Service) Permissions(ctx context.Context) ([]Permission, error) {
	rows, err := s.DB.Query(ctx, `SELECT id::text,name,created_at,updated_at FROM permissions ORDER BY name`)
	if err != nil {
		return nil, internal()
	}
	defer rows.Close()
	result := []Permission{}
	for rows.Next() {
		p, e := scanPermission(rows)
		if e != nil {
			return nil, internal()
		}
		result = append(result, p)
	}
	if rows.Err() != nil {
		return nil, internal()
	}
	return result, nil
}
func (s *Service) CreatePermission(ctx context.Context, ep Endpoint, actor *Actor, name string) (Permission, error) {
	var out Permission
	if err := validName(name, 128); err != nil {
		return out, err
	}
	err := s.Mutate(ctx, ep, actor, "CREATE", func(tx pgx.Tx) (string, any, any, error) {
		p, e := scanPermission(tx.QueryRow(ctx, `INSERT INTO permissions(name) VALUES($1) RETURNING id::text,name,created_at,updated_at`, strings.TrimSpace(name)))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = p
		return p.ID, nil, p, nil
	})
	return out, err
}
func (s *Service) UpdatePermission(ctx context.Context, ep Endpoint, actor *Actor, id, name string) (Permission, error) {
	var out Permission
	if err := validUUID(id); err != nil {
		return out, err
	}
	if err := validName(name, 128); err != nil {
		return out, err
	}
	err := s.Mutate(ctx, ep, actor, "UPDATE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanPermission(tx.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM permissions WHERE id=$1::uuid FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		after, e := scanPermission(tx.QueryRow(ctx, `UPDATE permissions SET name=$2,updated_at=now() WHERE id=$1::uuid RETURNING id::text,name,created_at,updated_at`, id, strings.TrimSpace(name)))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = after
		return id, before, after, nil
	})
	return out, err
}
func (s *Service) DeletePermission(ctx context.Context, ep Endpoint, actor *Actor, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	return s.Mutate(ctx, ep, actor, "DELETE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanPermission(tx.QueryRow(ctx, `SELECT id::text,name,created_at,updated_at FROM permissions WHERE id=$1::uuid FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		_, e = tx.Exec(ctx, `DELETE FROM permissions WHERE id=$1::uuid`, id)
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		return id, before, nil, nil
	})
}

func (s *Service) File(ctx context.Context, id string) (StoredFile, error) {
	var f StoredFile
	if err := validUUID(id); err != nil {
		return f, err
	}
	f, err := scanFile(s.DB.QueryRow(ctx, `SELECT id::text,original_name,mime_type,size,created_at FROM stored_files WHERE id=$1::uuid AND status='READY'`, id))
	if err != nil {
		return f, dbError(err)
	}
	return f, nil
}
func (s *Service) CreateFile(ctx context.Context, ep Endpoint, actor *Actor, storage, key, name, mime string, size int64) (StoredFile, error) {
	var out StoredFile
	err := s.Mutate(ctx, ep, actor, "CREATE", func(tx pgx.Tx) (string, any, any, error) {
		f, e := scanFile(tx.QueryRow(ctx, `INSERT INTO stored_files(storage,object_key,original_name,mime_type,size,uploader_id) VALUES($1,$2,$3,$4,$5,$6::uuid) RETURNING id::text,original_name,mime_type,size,created_at`, storage, key, name, mime, size, actor.ID))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = f
		return f.ID, nil, f, nil
	})
	return out, err
}

func ignoreNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || db.IsNotFound(err) {
		return nil
	}
	return err
}
