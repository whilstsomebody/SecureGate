package ratelimit

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()
var Rdb *redis.Client

func InitRedis() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr ==  "" {
		fmt.Println("Redis address not set.")
	}

	Rdb = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	_, err := Rdb.Ping(Ctx).Result()
	if err != nil {
		log.Fatal("Failed to connect to redis: ", err)
	}

	log.Println("Successfully connected to Redis at PORT :", redisAddr)
}