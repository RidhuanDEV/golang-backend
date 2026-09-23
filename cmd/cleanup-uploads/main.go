package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/RidhuanDEV/golang-backend/internal/config"
	"github.com/RidhuanDEV/golang-backend/internal/db"
	"github.com/RidhuanDEV/golang-backend/internal/storage"
	"github.com/joho/godotenv"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	apply := flag.Bool("apply", false, "delete orphan objects; default only lists them")
	flag.Parse()
	_ = godotenv.Load()
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := db.Connect(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	before := time.Now().Add(-time.Duration(c.UploadOrphanGraceHours) * time.Hour)
	var keys []string
	var store storage.Storage
	if c.UploadStorage == "s3" {
		s3store, e := storage.NewS3(ctx, c)
		if e != nil {
			return e
		}
		store = s3store
		keys, err = s3store.ListOlder(ctx, before)
		if err != nil {
			return err
		}
	} else {
		local, e := storage.NewLocal(c.UploadLocalDir)
		if e != nil {
			return e
		}
		store = local
		entries, e := os.ReadDir(c.UploadLocalDir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if info.ModTime().Before(before) {
				keys = append(keys, entry.Name())
			}
		}
	}
	for _, key := range keys {
		var exists bool
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stored_files WHERE object_key=$1 AND storage=$2)`, key, c.UploadStorage).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		display := key
		if c.UploadStorage == "local" {
			display = filepath.Join(c.UploadLocalDir, key)
		}
		fmt.Println(display)
		if *apply {
			if err = store.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	return nil
}
