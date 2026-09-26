package auth

import (
	"context"
	"errors"
	"github.com/RidhuanDEV/golang-backend/internal/audit"
	"github.com/RidhuanDEV/golang-backend/internal/db/projection"
	"github.com/RidhuanDEV/golang-backend/internal/db/sqlc"
	"github.com/RidhuanDEV/golang-backend/internal/fault"
	"github.com/RidhuanDEV/golang-backend/internal/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

type Actor = audit.Actor
type Claims struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	RoleID string `json:"roleId"`
	jwt.RegisteredClaims
}
type Service struct {
	Queries          *sqlc.Queries
	Audit            *audit.Writer
	Secret           []byte
	Issuer, Audience string
}

func (s *Service) Register(ctx context.Context, p audit.Policy, b model.Credentials) (model.AuthUser, error) {
	if err := fault.Email(b.Email); err != nil {
		return model.AuthUser{}, err
	}
	if len(b.Password) < 6 || len(b.Password) > 72 {
		return model.AuthUser{}, fault.New(fault.Invalid, "Password must contain 6-72 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
	if err != nil {
		return model.AuthUser{}, fault.DB(err)
	}
	return audit.Mutate(ctx, s.Audit, p, nil, "CREATE", func(q *sqlc.Queries) (model.AuthUser, audit.Change, error) {
		r, err := q.FindRoleByName(ctx, "user")
		if err != nil {
			return model.AuthUser{}, audit.Change{}, fault.DB(err)
		}
		row, err := q.CreateUser(ctx, sqlc.CreateUserParams{Lower: b.Email, Password: string(hash), RoleID: r.ID})
		if err != nil {
			return model.AuthUser{}, audit.Change{}, fault.DB(err)
		}
		out := projection.AuthUser(row)
		change, err := audit.Capture(out.ID, (*model.AuthUser)(nil), &out)
		return out, change, err
	})
}
func (s *Service) Login(ctx context.Context, p audit.Policy, b model.Credentials) (model.Token, error) {
	row, err := s.Queries.FindActiveUserByEmail(ctx, strings.ToLower(b.Email))
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return model.Token{}, fault.DB(err)
		}
		return model.Token{}, fault.New(fault.Unauthorized, "Invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(row.Password), []byte(b.Password)) != nil {
		return model.Token{}, fault.New(fault.Unauthorized, "Invalid credentials")
	}
	now := time.Now().UTC()
	claims := Claims{ID: row.ID, Email: row.Email, RoleID: row.RoleID, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.Issuer, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour))}}
	if s.Audience != "" {
		claims.Audience = jwt.ClaimStrings{s.Audience}
	}
	actor := &Actor{ID: row.ID, Email: row.Email, RoleID: row.RoleID}
	payload := struct {
		Email string `json:"email"`
	}{row.Email}
	change, err := audit.Capture(row.ID, (*struct {
		Email string `json:"email"`
	})(nil), &payload)
	if err != nil {
		return model.Token{}, fault.DB(err)
	}
	if p.Mode != audit.None {
		if p.Mode == audit.Optional {
			detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			err = s.Audit.Write(detached, s.Queries, p, actor, "LOGIN", change)
		} else {
			err = s.Audit.Write(ctx, s.Queries, p, actor, "LOGIN", change)
		}
		if err != nil {
			if p.Mode == audit.Required {
				return model.Token{}, fault.DB(err)
			}
			s.Audit.Log.Warn("optional login audit failed", "error", err)
		}
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.Secret)
	return model.Token{Token: token}, fault.DB(err)
}
func (s *Service) Authenticate(ctx context.Context, raw string) (*Actor, error) {
	deny := func() (*Actor, error) { return nil, fault.New(fault.Unauthorized, "Unauthorized") }
	if !strings.HasPrefix(raw, "Bearer ") {
		return deny()
	}
	claims := new(Claims)
	options := []jwt.ParserOption{jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired()}
	if s.Issuer != "" {
		options = append(options, jwt.WithIssuer(s.Issuer))
	}
	if s.Audience != "" {
		options = append(options, jwt.WithAudience(s.Audience))
	}
	token, err := jwt.ParseWithClaims(strings.TrimPrefix(raw, "Bearer "), claims, func(*jwt.Token) (any, error) { return s.Secret, nil }, options...)
	if err != nil || !token.Valid || fault.UUID(claims.ID) != nil {
		return deny()
	}
	row, err := s.Queries.FindActiveUserByID(ctx, claims.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return deny()
		}
		return nil, fault.DB(err)
	}
	return &Actor{ID: row.ID, Email: row.Email, RoleID: row.RoleID}, nil
}
func (s *Service) Authorize(ctx context.Context, actor *Actor, permission string) error {
	if permission == "" {
		return nil
	}
	allowed, err := s.Queries.UserHasPermission(ctx, sqlc.UserHasPermissionParams{ID: actor.ID, Name: permission})
	if err != nil {
		return fault.DB(err)
	}
	if !allowed {
		return fault.New(fault.Forbidden, "Forbidden")
	}
	return nil
}
func (s *Service) Me(ctx context.Context, actor *Actor) (model.AuthUser, error) {
	r, err := s.Queries.FindUser(ctx, actor.ID)
	return projection.AuthUser(r), fault.DB(err)
}
