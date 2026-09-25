package queue

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Queue names. Asynq processes queues by weighted priority. We keep a single
// "builds" queue for now; separating a "priority" queue later is a config-only
// change on the worker side.
const QueueBuilds = "builds"

// Client is the producer side: the API uses it to enqueue build tasks.
type Client struct {
	inner *asynq.Client
}

// NewClient connects to Redis. RedisAddr/password come from config.
func NewClient(redisAddr, redisPassword string) *Client {
	return &Client{
		inner: asynq.NewClient(asynq.RedisClientOpt{
			Addr:     redisAddr,
			Password: redisPassword,
		}),
	}
}

// Close releases the Redis connection.
func (c *Client) Close() error { return c.inner.Close() }

// EnqueueBuild puts a build task on the queue. We attach options here rather
// than in NewBuildTask so the same task definition could be enqueued with
// different priorities/timeouts by different callers if needed later.
func (c *Client) EnqueueBuild(p BuildPayload) (string, error) {
	task, err := NewBuildTask(p)
	if err != nil {
		return "", err
	}
	info, err := c.inner.Enqueue(task,
		asynq.Queue(QueueBuilds),
		asynq.MaxRetry(2),             // 3 attempts total
		asynq.Timeout(20*time.Minute), // a build should never exceed this
		asynq.Retention(24*time.Hour), // keep completed task info for debugging
		asynq.TaskID(p.DeploymentID),  // dedupe: one build task per deployment
	)
	if err != nil {
		return "", fmt.Errorf("queue: enqueue build: %w", err)
	}
	return info.ID, nil
}

// EnqueueTeardown puts a teardown task on the queue. We use a distinct TaskID
// namespace so it never collides with the build task that shares the deployment
// ID.
func (c *Client) EnqueueTeardown(p TeardownPayload) (string, error) {
	task, err := NewTeardownTask(p)
	if err != nil {
		return "", err
	}
	info, err := c.inner.Enqueue(task,
		asynq.Queue(QueueBuilds),
		asynq.MaxRetry(3),
		asynq.Timeout(10*time.Minute),
		asynq.Retention(24*time.Hour),
		asynq.TaskID("teardown-"+p.DeploymentID),
	)
	if err != nil {
		return "", fmt.Errorf("queue: enqueue teardown: %w", err)
	}
	return info.ID, nil
}
