package httpapi

import (
	"context"
)

func (s *Server) mountAuth() {
	register[RegisterInput, AuthUser](s, "auth.register", func(ctx context.Context, input *RegisterInput, _ *Actor) (Success[AuthUser], error) {
		b := input.Body
		user, err := s.Auth.Register(ctx, s.policy(ctx, "auth.register"), Credentials{Email: b.Email, Password: b.Password})
		return ok(user), err
	})
	register[CredentialsInput, Token](s, "auth.login", func(ctx context.Context, input *CredentialsInput, _ *Actor) (Success[Token], error) {
		b := input.Body
		token, err := s.Auth.Login(ctx, s.policy(ctx, "auth.login"), b)
		return ok(token), err
	})
	register[Empty, AuthUser](s, "auth.me", func(ctx context.Context, input *Empty, actor *Actor) (Success[AuthUser], error) {
		user, err := s.Auth.Me(ctx, actor)
		return ok(user), err
	})

}
