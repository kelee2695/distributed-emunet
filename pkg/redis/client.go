package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client *redis.Client
}

type ClientOptions struct {
	PoolSize     int
	MinIdleConns int
}

func NewClient(addr string, password string, db int) *Client {
	return NewClientWithOptions(addr, password, db, ClientOptions{})
}

func NewClientWithOptions(addr string, password string, db int, opts ClientOptions) *Client {
	if opts.PoolSize <= 0 {
		opts.PoolSize = 100
	}
	if opts.MinIdleConns < 0 {
		opts.MinIdleConns = 0
	}
	if opts.MinIdleConns == 0 && opts.PoolSize >= 10 {
		opts.MinIdleConns = 10
	}
	if opts.MinIdleConns > opts.PoolSize {
		opts.MinIdleConns = opts.PoolSize
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     opts.PoolSize,
		MinIdleConns: opts.MinIdleConns,
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
