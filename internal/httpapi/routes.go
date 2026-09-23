package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) mount() {
	for _, id := range []EndpointID{"health.get", "live.get"} {
		register[Empty, Status](s, id, func(context.Context, *http.Request, *Actor) (Success[Status], error) { return ok(Status{"ok"}), nil })
	}
	register[Empty, Status](s, "ready.get", func(ctx context.Context, _ *http.Request, _ *Actor) (Success[Status], error) {
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := s.DB.Ping(ctx); err != nil {
			return Success[Status]{}, &APIError{Status: 503, Message: "Service unavailable", Errors: []string{}}
		}
		if s.Config.RateStore == "redis" {
			if s.Redis == nil || s.Redis.Ping(ctx).Err() != nil {
				return Success[Status]{}, &APIError{Status: 503, Message: "Service unavailable", Errors: []string{}}
			}
		}
		return ok(Status{"ok"}), nil
	})
	manual[Empty, struct {
		Body string `contentType:"text/html"`
	}](s, "docs.ui", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Go Backend API</title><meta charset="utf-8"></head><body><redoc spec-url="/docs/openapi.json"></redoc><script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script></body></html>`)
	})
	manual[Empty, struct{ Body json.RawMessage }](s, "docs.spec", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Docs.OpenAPI()) })
	manual[struct {
		Module string `path:"module"`
	}, struct{ Body json.RawMessage }](s, "docs.moduleSpec", func(w http.ResponseWriter, r *http.Request) {
		module := strings.TrimSuffix(chi.URLParam(r, "module"), ".json")
		if module == "" {
			writeError(w, notFound())
			return
		}
		raw, err := json.Marshal(s.Docs.OpenAPI())
		if err != nil {
			writeError(w, internal())
			return
		}
		var spec map[string]any
		if err = json.Unmarshal(raw, &spec); err != nil {
			writeError(w, internal())
			return
		}
		paths, _ := spec["paths"].(map[string]any)
		selected := map[string]any{}
		for path, value := range paths {
			operations, _ := value.(map[string]any)
			matched := map[string]any{}
			for method, operation := range operations {
				op, _ := operation.(map[string]any)
				tags, _ := op["tags"].([]any)
				for _, tag := range tags {
					if tag == module {
						matched[method] = operation
					}
				}
			}
			if len(matched) > 0 {
				selected[path] = matched
			}
		}
		if len(selected) == 0 {
			writeError(w, notFound())
			return
		}
		spec["paths"] = selected
		writeJSON(w, 200, spec)
	})

	register[CredentialsInput, AuthUser](s, "auth.register", func(ctx context.Context, r *http.Request, _ *Actor) (Success[AuthUser], error) {
		var b Credentials
		if err := decodeJSON(r, &b); err != nil {
			return Success[AuthUser]{}, err
		}
		user, err := s.Service.Register(ctx, s.Policies["auth.register"], b)
		return ok(user), err
	})
	register[CredentialsInput, Token](s, "auth.login", func(ctx context.Context, r *http.Request, _ *Actor) (Success[Token], error) {
		var b Credentials
		if err := decodeJSON(r, &b); err != nil {
			return Success[Token]{}, err
		}
		token, err := s.Service.Login(ctx, s.Policies["auth.login"], b)
		return ok(token), err
	})
	register[Empty, AuthUser](s, "auth.me", func(ctx context.Context, _ *http.Request, actor *Actor) (Success[AuthUser], error) {
		user, err := s.Service.Me(ctx, actor)
		return ok(user), err
	})

	register[UserListInput, []User](s, "user.list", func(ctx context.Context, r *http.Request, _ *Actor) (Success[[]User], error) {
		q := r.URL.Query()
		page := 1
		limit := 10
		var err error
		if q.Has("page") {
			page, err = strconv.Atoi(q.Get("page"))
			if err != nil {
				return Success[[]User]{}, badRequest("Invalid page")
			}
		}
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil {
				return Success[[]User]{}, badRequest("Invalid limit")
			}
		}
		if fields := q.Get("fields"); fields != "" {
			for _, f := range strings.Split(fields, ",") {
				switch strings.TrimSpace(f) {
				case "id", "email", "roleId", "createdAt", "updatedAt", "role":
				default:
					return Success[[]User]{}, badRequest("Invalid fields")
				}
			}
		}
		users, meta, err := s.Service.Users(ctx, page, limit, q.Get("search"), q.Get("sortBy"), q.Get("orderBy"))
		out := ok(users)
		out.Meta = &meta
		return out, err
	})
	register[IDInput, User](s, "user.get", func(ctx context.Context, r *http.Request, _ *Actor) (Success[User], error) {
		user, err := s.Service.User(ctx, chi.URLParam(r, "id"))
		return ok(user), err
	})
	register[UserInput, User](s, "user.create", func(ctx context.Context, r *http.Request, actor *Actor) (Success[User], error) {
		var b CreateUserBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[User]{}, err
		}
		user, err := s.Service.CreateUser(ctx, s.Policies["user.create"], actor, b)
		return ok(user), err
	})
	register[UpdateUserInput, User](s, "user.update", func(ctx context.Context, r *http.Request, actor *Actor) (Success[User], error) {
		var b UpdateUserBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[User]{}, err
		}
		user, err := s.Service.UpdateUser(ctx, s.Policies["user.update"], actor, chi.URLParam(r, "id"), b)
		return ok(user), err
	})
	register[IDInput, Empty](s, "user.delete", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Empty], error) {
		return ok(Empty{}), s.Service.DeleteUser(ctx, s.Policies["user.delete"], actor, chi.URLParam(r, "id"))
	})

	register[Empty, []Role](s, "role.list", func(ctx context.Context, _ *http.Request, _ *Actor) (Success[[]Role], error) {
		roles, err := s.Service.Roles(ctx)
		return ok(roles), err
	})
	register[IDInput, Role](s, "role.get", func(ctx context.Context, r *http.Request, _ *Actor) (Success[Role], error) {
		role, err := s.Service.Role(ctx, chi.URLParam(r, "id"))
		return ok(role), err
	})
	register[NamedInput, Role](s, "role.create", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Role], error) {
		var b NameBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[Role]{}, err
		}
		role, err := s.Service.CreateRole(ctx, s.Policies["role.create"], actor, b.Name)
		return ok(role), err
	})
	register[UpdateNameInput, Role](s, "role.update", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Role], error) {
		var b NameBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[Role]{}, err
		}
		role, err := s.Service.UpdateRole(ctx, s.Policies["role.update"], actor, chi.URLParam(r, "id"), b.Name)
		return ok(role), err
	})
	register[IDInput, Empty](s, "role.delete", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Empty], error) {
		return ok(Empty{}), s.Service.DeleteRole(ctx, s.Policies["role.delete"], actor, chi.URLParam(r, "id"))
	})
	register[AssignmentInput, Role](s, "role.assignPermissions", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Role], error) {
		var b PermissionAssignment
		if err := decodeJSON(r, &b); err != nil {
			return Success[Role]{}, err
		}
		role, err := s.Service.AssignPermissions(ctx, s.Policies["role.assignPermissions"], actor, chi.URLParam(r, "id"), b.PermissionIDs)
		return ok(role), err
	})

	register[Empty, []Permission](s, "permission.list", func(ctx context.Context, _ *http.Request, _ *Actor) (Success[[]Permission], error) {
		permissions, err := s.Service.Permissions(ctx)
		return ok(permissions), err
	})
	register[IDInput, Permission](s, "permission.get", func(ctx context.Context, r *http.Request, _ *Actor) (Success[Permission], error) {
		permission, err := s.Service.Permission(ctx, chi.URLParam(r, "id"))
		return ok(permission), err
	})
	register[NamedInput, Permission](s, "permission.create", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Permission], error) {
		var b NameBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[Permission]{}, err
		}
		permission, err := s.Service.CreatePermission(ctx, s.Policies["permission.create"], actor, b.Name)
		return ok(permission), err
	})
	register[UpdateNameInput, Permission](s, "permission.update", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Permission], error) {
		var b NameBody
		if err := decodeJSON(r, &b); err != nil {
			return Success[Permission]{}, err
		}
		permission, err := s.Service.UpdatePermission(ctx, s.Policies["permission.update"], actor, chi.URLParam(r, "id"), b.Name)
		return ok(permission), err
	})
	register[IDInput, Empty](s, "permission.delete", func(ctx context.Context, r *http.Request, actor *Actor) (Success[Empty], error) {
		return ok(Empty{}), s.Service.DeletePermission(ctx, s.Policies["permission.delete"], actor, chi.URLParam(r, "id"))
	})

	register[UploadInput, StoredFile](s, "upload.create", func(ctx context.Context, r *http.Request, actor *Actor) (Success[StoredFile], error) {
		var zero Success[StoredFile]
		if !s.Config.UploadEnabled {
			return zero, &APIError{Status: 503, Message: "Upload disabled", Errors: []string{}}
		}
		r.Body = http.MaxBytesReader(nil, r.Body, s.Config.UploadMaxBytes+1048576)
		if err := r.ParseMultipartForm(s.Config.UploadMaxBytes + 1048576); err != nil {
			return zero, badRequest("Invalid multipart upload")
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		source, header, err := r.FormFile("file")
		if err != nil {
			return zero, badRequest("file is required")
		}
		defer source.Close()
		if header.Size > s.Config.UploadMaxBytes {
			return zero, &APIError{Status: 413, Message: "File too large", Errors: []string{}}
		}
		prefix := make([]byte, 512)
		n, err := io.ReadFull(source, prefix)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return zero, badRequest("Invalid file")
		}
		mime := http.DetectContentType(prefix[:n])
		if _, ok := s.Config.UploadAllowedMIME[mime]; !ok {
			return zero, badRequest("File type is not allowed")
		}
		if _, err = source.Seek(0, io.SeekStart); err != nil {
			return zero, internal()
		}
		key := uuid.NewString()
		if err = s.Storage.Put(ctx, key, io.NewSectionReader(source, 0, header.Size)); err != nil {
			s.Logger.Error("upload storage write failed", "storage", s.Config.UploadStorage, "error", err)
			return zero, internal()
		}
		file, err := s.Service.CreateFile(ctx, s.Policies["upload.create"], actor, s.Config.UploadStorage, key, header.Filename, mime, header.Size)
		if err != nil {
			if cleanupErr := s.Storage.Delete(ctx, key); cleanupErr != nil {
				s.Logger.Warn("upload cleanup failed", "key", key, "error", cleanupErr)
			}
			return zero, err
		}
		return ok(file), nil
	})
	register[IDInput, StoredFile](s, "upload.get", func(ctx context.Context, r *http.Request, _ *Actor) (Success[StoredFile], error) {
		file, err := s.Service.File(ctx, chi.URLParam(r, "id"))
		return ok(file), err
	})
}
