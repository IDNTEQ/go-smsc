package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// CallbackRecord tracks a pending DLR/MO callback.
type CallbackRecord struct {
	GwMsgID     string    `json:"gw_msg_id"`
	CallbackURL string    `json:"callback_url"`
	Reference   string    `json:"reference"`
	Retries     int       `json:"retries"`
	NextAttempt time.Time `json:"next_attempt"`
}

// DLRCallbackPayload is the JSON body POSTed to callback URLs for DLRs.
type DLRCallbackPayload struct {
	Event     string    `json:"event"`
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	To        string    `json:"to"`
	From      string    `json:"from"`
	Reference string    `json:"reference,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// MOCallbackPayload is the JSON body POSTed to callback URLs for MO messages.
type MOCallbackPayload struct {
	Event     string    `json:"event"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Body      string    `json:"body"`
	Timestamp time.Time `json:"timestamp"`
}

// deliverDLRCallback sends a DLR notification to the registered callback URL.
func (r *Router) deliverDLRCallback(gwMsgID, status, destAddr, sourceAddr string) {
	if r.store == nil {
		return
	}

	var cb CallbackRecord
	if err := r.store.GetJSON("callback:"+gwMsgID, &cb); err != nil {
		return // No callback registered for this message
	}

	payload := DLRCallbackPayload{
		Event:     "dlr",
		ID:        gwMsgID,
		Status:    status,
		To:        destAddr,
		From:      sourceAddr,
		Reference: cb.Reference,
		Timestamp: time.Now(),
	}

	if err := r.postCallback(cb.CallbackURL, payload); err != nil {
		r.logger.Warn("DLR callback failed, enqueueing retry",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("url", cb.CallbackURL),
			zap.Error(err),
		)
		r.enqueueCallbackRetry(gwMsgID, &cb)
	} else {
		r.metrics.CallbackTotal.WithLabelValues("success").Inc()
		// Callback delivered — clean up
		_ = r.store.DeleteKey("callback:" + gwMsgID)
	}
}

// deliverMOCallback sends an MO notification to the callback URL from MO route.
func (r *Router) deliverMOCallback(callbackURL, sourceAddr, destAddr string, payload []byte) {
	moPayload := MOCallbackPayload{
		Event:     "mo",
		From:      sourceAddr,
		To:        destAddr,
		Body:      string(payload),
		Timestamp: time.Now(),
	}

	if err := r.postCallback(callbackURL, moPayload); err != nil {
		r.logger.Warn("MO callback failed",
			zap.String("url", callbackURL),
			zap.Error(err),
		)
		// MO callbacks: best effort, no retry for now
	}
}

// postCallback POSTs a JSON payload to the given URL.
func (r *Router) postCallback(url string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("callback returned status %d", resp.StatusCode)
}

// enqueueCallbackRetry schedules a callback for retry with exponential backoff.
func (r *Router) enqueueCallbackRetry(gwMsgID string, cb *CallbackRecord) {
	if cb.Retries >= 3 {
		r.logger.Warn("callback retries exhausted",
			zap.String("gw_msg_id", gwMsgID),
			zap.String("url", cb.CallbackURL),
		)
		r.metrics.CallbackTotal.WithLabelValues("failed").Inc()
		_ = r.store.DeleteKey("callback:" + gwMsgID)
		return
	}
	r.metrics.CallbackTotal.WithLabelValues("retry").Inc()

	// Exponential backoff: 5s, 30s, 180s
	delays := []time.Duration{5 * time.Second, 30 * time.Second, 180 * time.Second}
	delay := delays[cb.Retries]

	cb.Retries++
	cb.NextAttempt = time.Now().Add(delay)

	retryKey := fmt.Sprintf("callback-retry:%d:%s", cb.NextAttempt.UnixNano(), gwMsgID)
	_ = r.store.SetJSON(retryKey, cb)
	// Keep the callback: record for DLR info lookup
	_ = r.store.SetJSON("callback:"+gwMsgID, cb)
}

// RunCallbackRetryLoop drains callback retries periodically.
func (r *Router) RunCallbackRetryLoop(ctx context.Context, interval time.Duration) {
	if r.store == nil {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.drainCallbackRetries()
		case <-ctx.Done():
			return
		}
	}
}

func (r *Router) drainCallbackRetries() {
	now := time.Now()
	var toProcess []struct {
		key string
		cb  CallbackRecord
	}

	_ = r.store.ScanPrefix("callback-retry:", func(key string, data []byte) error {
		var cb CallbackRecord
		if err := json.Unmarshal(data, &cb); err != nil {
			return nil
		}
		if cb.NextAttempt.Before(now) {
			toProcess = append(toProcess, struct {
				key string
				cb  CallbackRecord
			}{key: key, cb: cb})
		}
		return nil
	})

	for _, item := range toProcess {
		_ = r.store.DeleteKey(item.key) // Remove retry marker

		// Load the DLR callback info
		var cb CallbackRecord
		if err := r.store.GetJSON("callback:"+item.cb.GwMsgID, &cb); err != nil {
			continue
		}

		payload := DLRCallbackPayload{
			Event:     "dlr",
			ID:        cb.GwMsgID,
			Status:    "DELIVRD", // Best we can do for retry
			Reference: cb.Reference,
			Timestamp: time.Now(),
		}

		if err := r.postCallback(cb.CallbackURL, payload); err != nil {
			r.enqueueCallbackRetry(cb.GwMsgID, &cb)
		} else {
			r.metrics.CallbackTotal.WithLabelValues("success").Inc()
			_ = r.store.DeleteKey("callback:" + cb.GwMsgID)
		}
	}
}
