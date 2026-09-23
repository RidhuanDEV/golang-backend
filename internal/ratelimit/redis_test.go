package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/RidhuanDEV/golang-backend/internal/config"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisLimitSharedAcrossInstances(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		t.Skip("REDIS_URL is not set")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	rate := config.Rate{WindowMS: 60000, Max: 2}
	first := New(client)
	second := New(client)
	for index, limiter := range []*Limiter{first, second, first} {
		allowed, e := limiter.Allow(ctx, "auth", key, rate)
		if e != nil {
			t.Fatal(e)
		}
		if allowed != (index < 2) {
			t.Fatalf("attempt %d allowed=%t", index, allowed)
		}
	}
}
