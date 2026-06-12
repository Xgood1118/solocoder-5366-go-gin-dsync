package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"configsync/internal/models"
	"configsync/internal/storage"
)

type Notifier interface {
	Notify(payload *models.NotificationPayload, subscription *models.Subscription)
	Start(ctx context.Context)
	Stop() error
	Wait(timeout time.Duration) bool
	PendingCount() int64
}

type notificationTask struct {
	payload      *models.NotificationPayload
	subscription *models.Subscription
	retry        int
}

type LocalNotifier struct {
	store        *storage.Store
	workerCount  int
	taskQueue    chan notificationTask
	wg           sync.WaitGroup
	activeTasks  int64
	httpClient   *http.Client
	retryDelays  []time.Duration
	stopped      int32
}

func NewLocalNotifier(store *storage.Store, workerCount int) *LocalNotifier {
	if workerCount <= 0 {
		workerCount = 16
	}
	return &LocalNotifier{
		store:       store,
		workerCount: workerCount,
		taskQueue:   make(chan notificationTask, 10000),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		retryDelays: []time.Duration{
			1 * time.Second,
			5 * time.Second,
			30 * time.Second,
		},
	}
}

func (n *LocalNotifier) Notify(payload *models.NotificationPayload, subscription *models.Subscription) {
	if atomic.LoadInt32(&n.stopped) == 1 {
		return
	}
	n.taskQueue <- notificationTask{
		payload:      payload,
		subscription: subscription,
		retry:        0,
	}
}

func (n *LocalNotifier) Start(ctx context.Context) {
	for i := 0; i < n.workerCount; i++ {
		n.wg.Add(1)
		go n.worker(ctx)
	}
}

func (n *LocalNotifier) Stop() error {
	atomic.StoreInt32(&n.stopped, 1)
	close(n.taskQueue)
	return nil
}

func (n *LocalNotifier) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (n *LocalNotifier) PendingCount() int64 {
	return atomic.LoadInt64(&n.activeTasks)
}

func (n *LocalNotifier) worker(ctx context.Context) {
	defer n.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case task, ok := <-n.taskQueue:
			if !ok {
				return
			}
			atomic.AddInt64(&n.activeTasks, 1)
			n.processTask(task)
			atomic.AddInt64(&n.activeTasks, -1)
		}
	}
}

func (n *LocalNotifier) processTask(task notificationTask) {
	if err := n.sendNotification(task.payload, task.subscription); err != nil {
		if task.retry < len(n.retryDelays) {
			time.Sleep(n.retryDelays[task.retry])
			if atomic.LoadInt32(&n.stopped) == 0 {
				task.retry++
				n.processTask(task)
			}
		} else {
			n.store.UpdateSubscriptionStatus(
				task.subscription.TenantID,
				task.subscription.ID,
				models.SubscriptionStatusUnhealthy,
				err.Error(),
			)
		}
	} else {
		if task.subscription.Status == models.SubscriptionStatusUnhealthy {
			n.store.UpdateSubscriptionStatus(
				task.subscription.TenantID,
				task.subscription.ID,
				models.SubscriptionStatusHealthy,
				"",
			)
		}
	}
}

func (n *LocalNotifier) sendNotification(payload *models.NotificationPayload, sub *models.Subscription) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", sub.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-Id", payload.TenantID)
	req.Header.Set("X-Config-Event", payload.Event)

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

type RemoteNotifier struct {
}

func (n *RemoteNotifier) Notify(payload *models.NotificationPayload, subscription *models.Subscription) {
}

func (n *RemoteNotifier) Start(ctx context.Context) {
}

func (n *RemoteNotifier) Stop() error {
	return nil
}

func (n *RemoteNotifier) Wait(timeout time.Duration) bool {
	return true
}

func (n *RemoteNotifier) PendingCount() int64 {
	return 0
}
