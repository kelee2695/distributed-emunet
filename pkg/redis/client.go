package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client *redis.Client
}

func NewClient(addr string, password string, db int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     100,
		MinIdleConns: 10,
	})

	return &Client{
		client: rdb,
	}
}

func (c *Client) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.client.Close()
}

func (c *Client) GetClient() *redis.Client {
	return c.client
}
