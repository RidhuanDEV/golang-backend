package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/RidhuanDEV/golang-backend/internal/db"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	_ = godotenv.Load()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"admin", "user"} {
		if _, err = pool.Exec(ctx, `INSERT INTO roles(name) VALUES($1) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			log.Fatal(err)
		}
	}
	for _, name := range []string{"manage_users", "manage_roles", "manage_permissions"} {
		if _, err = pool.Exec(ctx, `INSERT INTO permissions(name) VALUES($1) ON CONFLICT(name) DO NOTHING`, name); err != nil {
			log.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO role_permissions(role_id,permission_id) SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.name='admin' ON CONFLICT DO NOTHING`)
	if err != nil {
		log.Fatal(err)
	}
	for _, entry := range []struct{ email, password, role string }{{os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD"), "admin"}, {os.Getenv("USER_EMAIL"), os.Getenv("USER_PASSWORD"), "user"}} {
		if entry.email == "" || entry.password == "" {
			continue
		}
		if len(entry.password) < 6 {
			log.Fatal("bootstrap password must contain at least six characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(entry.password), 12)
		if err != nil {
			log.Fatal(err)
		}
		_, err = pool.Exec(ctx, `INSERT INTO users(email,password,role_id) SELECT lower($1),$2,id FROM roles WHERE name=$3 ON CONFLICT(email) DO UPDATE SET role_id=EXCLUDED.role_id,deleted_at=NULL,updated_at=now()`, strings.TrimSpace(entry.email), string(hash), entry.role)
		if err != nil {
			log.Fatal(err)
		}
	}
	log.Println("seed completed")
}
