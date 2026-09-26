package httpapi

import (
	"strings"
	"time"
)

type UserProjection struct {
	ID        *string    `json:"id,omitempty"`
	Email     *string    `json:"email,omitempty" format:"email"`
	RoleID    *string    `json:"roleId,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	Role      *UserRole  `json:"role,omitempty"`
}

func projectUsers(users []User, fields string) ([]UserProjection, error) {
	selected := map[string]bool{}
	if fields == "" {
		for _, name := range []string{"id", "email", "roleId", "createdAt", "updatedAt", "role"} {
			selected[name] = true
		}
	} else {
		for _, name := range strings.Split(fields, ",") {
			name = strings.TrimSpace(name)
			switch name {
			case "id", "email", "roleId", "createdAt", "updatedAt", "role":
				selected[name] = true
			default:
				return nil, badRequest("Invalid fields")
			}
		}
	}
	out := make([]UserProjection, 0, len(users))
	for _, value := range users {
		var row UserProjection
		if selected["id"] {
			row.ID = &value.ID
		}
		if selected["email"] {
			row.Email = &value.Email
		}
		if selected["roleId"] {
			row.RoleID = &value.RoleID
		}
		if selected["createdAt"] {
			row.CreatedAt = &value.CreatedAt
		}
		if selected["updatedAt"] {
			row.UpdatedAt = &value.UpdatedAt
		}
		if selected["role"] {
			row.Role = value.Role
		}
		out = append(out, row)
	}
	return out, nil
}

type BaseRole struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func baseRole(value Role) BaseRole {
	return BaseRole{value.ID, value.Name, value.CreatedAt, value.UpdatedAt}
}
