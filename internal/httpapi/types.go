package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Empty struct{}
type Success[T any] struct {
	Success bool        `json:"success"`
	Data    T           `json:"data"`
	Meta    *Pagination `json:"meta,omitempty"`
}
type Failure struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}
type Pagination struct {
	Page        int  `json:"page"`
	Limit       int  `json:"limit"`
	TotalItems  int  `json:"totalItems"`
	TotalPages  int  `json:"totalPages"`
	HasNextPage bool `json:"hasNextPage"`
	HasPrevPage bool `json:"hasPrevPage"`
}
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	RoleID    string    `json:"roleId"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Role      *UserRole `json:"role,omitempty"`
}
type AuthUser struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	RoleID    string     `json:"roleId"`
	DeletedAt *time.Time `json:"deletedAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}
type UserRole struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Permissions []NamedPermission `json:"permissions"`
}
type NamedPermission struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Role struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	Permissions []RolePermission `json:"permissions"`
}
type RolePermission struct {
	Permission NamedPermission `json:"permission"`
}
type Permission struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type StoredFile struct {
	ID           string    `json:"id"`
	OriginalName string    `json:"originalName"`
	MIMEType     string    `json:"mimeType"`
	Size         int64     `json:"size"`
	CreatedAt    time.Time `json:"createdAt"`
}
type Token struct {
	Token string `json:"token"`
}
type Status struct {
	Status string `json:"status"`
}
type NameBody struct {
	Name string `json:"name" minLength:"1" maxLength:"128"`
}
type Credentials struct {
	Email    string `json:"email" format:"email"`
	Password string `json:"password" minLength:"1"`
}
type CreateUserBody struct {
	Email    string `json:"email" format:"email"`
	Password string `json:"password" minLength:"6"`
	RoleID   string `json:"roleId" format:"uuid"`
}
type UpdateUserBody struct {
	Email  *string `json:"email,omitempty" format:"email"`
	RoleID *string `json:"roleId,omitempty" format:"uuid"`
}
type PermissionAssignment struct {
	PermissionIDs []string `json:"permissionIds" minItems:"1"`
}
type IDInput struct {
	ID string `path:"id" format:"uuid"`
}
type NamedInput struct{ Body NameBody }
type CredentialsInput struct{ Body Credentials }
type UserInput struct{ Body CreateUserBody }
type UpdateUserInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateUserBody
}
type UpdateNameInput struct {
	ID   string `path:"id" format:"uuid"`
	Body NameBody
}
type AssignmentInput struct {
	ID   string `path:"id" format:"uuid"`
	Body PermissionAssignment
}
type UserListInput struct {
	Page    int    `query:"page" default:"1"`
	Limit   int    `query:"limit" default:"10"`
	SortBy  string `query:"sortBy"`
	OrderBy string `query:"orderBy"`
	Search  string `query:"search"`
	Fields  string `query:"fields"`
}
type UploadInput struct {
	RawBody []byte `contentType:"multipart/form-data"`
}

type APIError struct {
	Status  int
	Message string
	Errors  []string
}

func (e *APIError) Error() string { return e.Message }
func badRequest(message string) error {
	return &APIError{Status: http.StatusBadRequest, Message: message, Errors: []string{}}
}
func notFound() error {
	return &APIError{Status: http.StatusNotFound, Message: "Not found", Errors: []string{}}
}
func conflict(message string) error {
	return &APIError{Status: http.StatusConflict, Message: message, Errors: []string{}}
}
func internal() error {
	return &APIError{Status: http.StatusInternalServerError, Message: "Internal server error", Errors: []string{}}
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func writeError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = &APIError{Status: 500, Message: "Internal server error"}
	}
	if apiErr.Errors == nil {
		apiErr.Errors = []string{}
	}
	writeJSON(w, apiErr.Status, Failure{false, apiErr.Message, apiErr.Errors})
}
func ok[T any](data T) Success[T] { return Success[T]{Success: true, Data: data} }
func validUUID(v string) error {
	if _, err := uuid.Parse(v); err != nil {
		return badRequest("Invalid UUID")
	}
	return nil
}

var emailRE = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func validEmail(v string) error {
	if !emailRE.MatchString(v) || len(v) > 320 {
		return badRequest("Invalid email address")
	}
	return nil
}
func validName(v string, max int) error {
	if strings.TrimSpace(v) == "" || len(v) > max {
		return badRequest(fmt.Sprintf("name must contain 1-%d characters", max))
	}
	return nil
}
