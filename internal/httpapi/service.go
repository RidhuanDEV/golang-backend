package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/RidhuanDEV/golang-backend/internal/db"
	"github.com/RidhuanDEV/golang-backend/internal/db/sqlc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Actor struct{ ID, Email, RoleID string }
type Service struct {
	DB     *pgxpool.Pool
	Secret []byte
	Log    *slog.Logger
}
type rowScanner interface{ Scan(...any) error }

func scanUser(row rowScanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.RoleID, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
func scanAuthUser(row rowScanner) (AuthUser, error) {
	var u AuthUser
	err := row.Scan(&u.ID, &u.Email, &u.RoleID, &u.DeletedAt, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}
func scanRole(row rowScanner) (Role, error) {
	var r Role
	err := row.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.UpdatedAt)
	r.Permissions = []RolePermission{}
	return r, err
}
func scanPermission(row rowScanner) (Permission, error) {
	var p Permission
	err := row.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}
func scanFile(row rowScanner) (StoredFile, error) {
	var f StoredFile
	err := row.Scan(&f.ID, &f.OriginalName, &f.MIMEType, &f.Size, &f.CreatedAt)
	return f, err
}
func dbError(err error) error {
	if db.IsNotFound(err) {
		return notFound()
	}
	if db.IsUniqueViolation(err) {
		return conflict("Record already exists")
	}
	if db.IsForeignKeyViolation(err) {
		return badRequest("Referenced record does not exist or is in use")
	}
	return internal()
}

func (s *Service) Audit(ctx context.Context, tx pgx.Tx, endpoint Endpoint, actor *Actor, behavior, entityID string, before, after any) error {
	if endpoint.Audit == AuditNone {
		return nil
	}
	var actorID any
	var actorSnapshot any
	if actor != nil {
		actorID = actor.ID
		actorSnapshot = actor.ID
	}
	if endpoint.ID == "auth.register" {
		actorID = entityID
		actorSnapshot = entityID
		endpoint.Module = "user"
		behavior = "REGISTER"
	}
	var beforeJSON, afterJSON []byte
	var err error
	if before != nil {
		beforeJSON, err = json.Marshal(before)
		if err != nil {
			return err
		}
	}
	if after != nil {
		afterJSON, err = json.Marshal(after)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO activity_logs (behavior,module,entity_id,user_id,actor_id_snapshot,before,after,request_id,endpoint_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, behavior, endpoint.Module, entityID, actorID, actorSnapshot, beforeJSON, afterJSON, requestID(ctx), string(endpoint.ID))
	return err
}
func (s *Service) Mutate(ctx context.Context, ep Endpoint, actor *Actor, behavior string, fn func(pgx.Tx) (string, any, any, error)) error {
	var entity string
	var before, after any
	err := db.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		entity, before, after, err = fn(tx)
		if err != nil {
			return err
		}
		if ep.Audit == AuditRequired {
			return s.Audit(ctx, tx, ep, actor, behavior, entity, before, after)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if ep.Audit == AuditOptional {
		if err = db.InTx(ctx, s.DB, func(tx pgx.Tx) error { return s.Audit(ctx, tx, ep, actor, behavior, entity, before, after) }); err != nil {
			s.Log.Warn("optional audit failed", "endpoint", ep.ID, "error", err)
		}
	}
	return nil
}
func (s *Service) ReadAudit(ctx context.Context, ep Endpoint, actor *Actor, entityID string) {
	if ep.Audit != AuditOptional {
		return
	}
	err := db.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		return s.Audit(ctx, tx, ep, actor, "READ", entityID, nil, map[string]string{"endpoint": string(ep.ID)})
	})
	if err != nil {
		s.Log.Warn("optional read audit failed", "endpoint", ep.ID, "error", err)
	}
}

func (s *Service) Register(ctx context.Context, ep Endpoint, body Credentials) (AuthUser, error) {
	var result AuthUser
	if err := validEmail(body.Email); err != nil {
		return result, err
	}
	if len(body.Password) < 6 {
		return result, badRequest("Password must be at least 6 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		return result, internal()
	}
	err = s.Mutate(ctx, ep, nil, "CREATE", func(tx pgx.Tx) (string, any, any, error) {
		var roleID string
		if e := tx.QueryRow(ctx, "SELECT id::text FROM roles WHERE name='user'").Scan(&roleID); e != nil {
			return "", nil, nil, dbError(e)
		}
		u, e := scanAuthUser(tx.QueryRow(ctx, `INSERT INTO users(email,password,role_id) VALUES(lower($1),$2,$3::uuid) RETURNING id::text,email,role_id::text,deleted_at,created_at,updated_at`, body.Email, string(hash), roleID))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		result = u
		return u.ID, nil, u, nil
	})
	return result, err
}
func (s *Service) Login(ctx context.Context, ep Endpoint, body Credentials) (Token, error) {
	var out Token
	row, err := sqlc.New(s.DB).FindActiveUserByEmail(ctx, strings.ToLower(body.Email))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(row.Password), []byte(body.Password)) != nil {
		return out, &APIError{Status: 401, Message: "Invalid credentials", Errors: []string{}}
	}
	id, email, roleID := uuid.UUID(row.ID.Bytes).String(), row.Email, uuid.UUID(row.RoleID.Bytes).String()
	claims := jwt.MapClaims{"id": id, "email": email, "roleId": roleID, "exp": time.Now().Add(24 * time.Hour).Unix(), "iat": time.Now().Unix()}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.Secret)
	if err != nil {
		return out, internal()
	}
	out.Token = token
	if ep.Audit != AuditNone {
		actor := &Actor{ID: id, Email: email, RoleID: roleID}
		auditErr := db.InTx(ctx, s.DB, func(tx pgx.Tx) error {
			return s.Audit(ctx, tx, ep, actor, "LOGIN", id, nil, map[string]string{"email": email})
		})
		if auditErr != nil {
			if ep.Audit == AuditRequired {
				return Token{}, internal()
			}
			s.Log.Warn("optional login audit failed", "error", auditErr)
		}
	}
	return out, nil
}
func (s *Service) Authenticate(ctx context.Context, raw string) (*Actor, error) {
	if !strings.HasPrefix(raw, "Bearer ") {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	token, err := jwt.Parse(strings.TrimPrefix(raw, "Bearer "), func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected JWT algorithm")
		}
		return s.Secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	id, ok := claims["id"].(string)
	if !ok {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	parsedID, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	row, err := sqlc.New(s.DB).FindActiveUserByID(ctx, pgtype.UUID{Bytes: parsedID, Valid: true})
	if err != nil {
		return nil, &APIError{Status: 401, Message: "Unauthorized", Errors: []string{}}
	}
	return &Actor{ID: uuid.UUID(row.ID.Bytes).String(), Email: row.Email, RoleID: uuid.UUID(row.RoleID.Bytes).String()}, nil
}
func (s *Service) Authorize(ctx context.Context, actor *Actor, permission string) error {
	if permission == "" {
		return nil
	}
	parsedID, parseErr := uuid.Parse(actor.ID)
	if parseErr != nil {
		return internal()
	}
	allowed, err := sqlc.New(s.DB).UserHasPermission(ctx, sqlc.UserHasPermissionParams{ID: pgtype.UUID{Bytes: parsedID, Valid: true}, Name: permission})
	if err != nil {
		return internal()
	}
	if !allowed {
		return &APIError{Status: 403, Message: "Forbidden", Errors: []string{}}
	}
	return nil
}
func (s *Service) Me(ctx context.Context, actor *Actor) (AuthUser, error) {
	u, err := scanAuthUser(s.DB.QueryRow(ctx, `SELECT id::text,email,role_id::text,deleted_at,created_at,updated_at FROM users WHERE id=$1::uuid AND deleted_at IS NULL`, actor.ID))
	if err != nil {
		return u, dbError(err)
	}
	return u, nil
}

func (s *Service) enrichUser(ctx context.Context, u *User) error {
	role, err := s.Role(ctx, u.RoleID)
	if err != nil {
		return err
	}
	permissions := make([]NamedPermission, 0, len(role.Permissions))
	for _, p := range role.Permissions {
		permissions = append(permissions, p.Permission)
	}
	u.Role = &UserRole{ID: role.ID, Name: role.Name, Permissions: permissions}
	return nil
}
func (s *Service) User(ctx context.Context, id string) (User, error) {
	var zero User
	if err := validUUID(id); err != nil {
		return zero, err
	}
	u, err := scanUser(s.DB.QueryRow(ctx, `SELECT id::text,email,role_id::text,created_at,updated_at FROM users WHERE id=$1::uuid AND deleted_at IS NULL`, id))
	if err != nil {
		return u, dbError(err)
	}
	if err = s.enrichUser(ctx, &u); err != nil {
		return zero, err
	}
	return u, nil
}
func (s *Service) Users(ctx context.Context, page, limit int, search, sortBy, orderBy string) ([]User, Pagination, error) {
	if page < 1 || limit < 1 || limit > 100 {
		return nil, Pagination{}, badRequest("Invalid pagination")
	}
	if sortBy == "" {
		sortBy = "createdAt"
	}
	sortColumns := map[string]string{"createdAt": "created_at", "updatedAt": "updated_at", "email": "email", "created_at": "created_at"}
	column, ok := sortColumns[sortBy]
	if !ok {
		return nil, Pagination{}, badRequest("Invalid sortBy")
	}
	direction := "ASC"
	if orderBy == "desc" {
		direction = "DESC"
	} else if orderBy != "" && orderBy != "asc" {
		return nil, Pagination{}, badRequest("Invalid orderBy")
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL AND ($1='' OR email ILIKE '%'||$1||'%')`, search).Scan(&total); err != nil {
		return nil, Pagination{}, internal()
	}
	query := fmt.Sprintf(`SELECT id::text,email,role_id::text,created_at,updated_at FROM users WHERE deleted_at IS NULL AND ($1='' OR email ILIKE '%%'||$1||'%%') ORDER BY %s %s,id LIMIT $2 OFFSET $3`, column, direction)
	rows, err := s.DB.Query(ctx, query, search, limit, (page-1)*limit)
	if err != nil {
		return nil, Pagination{}, internal()
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, Pagination{}, internal()
		}
		result = append(result, u)
	}
	if rows.Err() != nil {
		return nil, Pagination{}, internal()
	}
	for i := range result {
		if err = s.enrichUser(ctx, &result[i]); err != nil {
			return nil, Pagination{}, err
		}
	}
	pages := (total + limit - 1) / limit
	return result, Pagination{page, limit, total, pages, page < pages, page > 1}, nil
}
func (s *Service) CreateUser(ctx context.Context, ep Endpoint, actor *Actor, b CreateUserBody) (User, error) {
	var out User
	if err := validEmail(b.Email); err != nil {
		return out, err
	}
	if len(b.Password) < 6 {
		return out, badRequest("Password too short")
	}
	if err := validUUID(b.RoleID); err != nil {
		return out, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
	if err != nil {
		return out, internal()
	}
	err = s.Mutate(ctx, ep, actor, "CREATE", func(tx pgx.Tx) (string, any, any, error) {
		u, e := scanUser(tx.QueryRow(ctx, `INSERT INTO users(email,password,role_id) VALUES(lower($1),$2,$3::uuid) RETURNING id::text,email,role_id::text,created_at,updated_at`, b.Email, string(hash), b.RoleID))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = u
		return u.ID, nil, u, nil
	})
	if err != nil {
		return out, err
	}
	return s.User(ctx, out.ID)
}
func (s *Service) UpdateUser(ctx context.Context, ep Endpoint, actor *Actor, id string, b UpdateUserBody) (User, error) {
	var out User
	if err := validUUID(id); err != nil {
		return out, err
	}
	if b.Email == nil && b.RoleID == nil {
		return out, badRequest("No fields to update")
	}
	if b.Email != nil {
		if err := validEmail(*b.Email); err != nil {
			return out, err
		}
	}
	if b.RoleID != nil {
		if err := validUUID(*b.RoleID); err != nil {
			return out, err
		}
	}
	err := s.Mutate(ctx, ep, actor, "UPDATE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanUser(tx.QueryRow(ctx, `SELECT id::text,email,role_id::text,created_at,updated_at FROM users WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		u, e := scanUser(tx.QueryRow(ctx, `UPDATE users SET email=coalesce(lower($2),email),role_id=coalesce($3::uuid,role_id),updated_at=now() WHERE id=$1::uuid RETURNING id::text,email,role_id::text,created_at,updated_at`, id, b.Email, b.RoleID))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		out = u
		return id, before, u, nil
	})
	if err != nil {
		return out, err
	}
	return s.User(ctx, out.ID)
}
func (s *Service) DeleteUser(ctx context.Context, ep Endpoint, actor *Actor, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	if actor.ID == id {
		return &APIError{Status: 403, Message: "You cannot delete your own account.", Errors: []string{}}
	}
	var roleName string
	if err := s.DB.QueryRow(ctx, `SELECT name FROM roles WHERE id=$1::uuid`, actor.RoleID).Scan(&roleName); err != nil {
		return internal()
	}
	if roleName != "admin" {
		return &APIError{Status: 403, Message: "Only administrators are allowed to delete user accounts.", Errors: []string{}}
	}
	return s.Mutate(ctx, ep, actor, "DELETE", func(tx pgx.Tx) (string, any, any, error) {
		before, e := scanUser(tx.QueryRow(ctx, `SELECT id::text,email,role_id::text,created_at,updated_at FROM users WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, id))
		if e != nil {
			return "", nil, nil, dbError(e)
		}
		_, e = tx.Exec(ctx, `UPDATE users SET deleted_at=now(),updated_at=now() WHERE id=$1::uuid`, id)
		return id, before, nil, e
	})
}
