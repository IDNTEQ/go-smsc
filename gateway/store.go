package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

// SubmitRecord represents a submitted message stored in the Pebble KV store.
// Both GwMsgID (returned to engine) and SmscMsgID (from downstream SMSC)
// are persisted so crash recovery can reconstruct DLR correlation correctly.
type SubmitRecord struct {
	GwMsgID     string    `json:"gw_msg_id"`      // Gateway-generated ID (sent to engine)
	SmscMsgID   string    `json:"smsc_msg_id"`     // Downstream SMSC ID (for DLR correlation)
	NorthConnID string    `json:"north_conn_id"`
	OrigSeqNum  uint32    `json:"orig_seq_num"`
	MSISDN      string    `json:"msisdn"`
	SourceAddr  string    `json:"source_addr"`
	Payload     []byte    `json:"payload"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// PendingSubmit represents a southbound submit_sm that failed and is queued
// for retry. After MaxSubmitRetries, a synthetic failure DLR is sent to the
// engine so the card doesn't stay stuck in awaiting_dlr.
type PendingSubmit struct {
	GwMsgID    string    `json:"gw_msg_id"`
	ConnID     string    `json:"conn_id"`
	MSISDN     string    `json:"msisdn"`
	SourceAddr string    `json:"source_addr"`
	RawBody    []byte    `json:"raw_body"` // full submit_sm body, forwarded byte-for-byte
	RetryCount int       `json:"retry_count"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// PendingDeliver represents a DLR/MO that couldn't be delivered to an engine
// (e.g. disconnected) and is queued for retry.
type PendingDeliver struct {
	TargetConnID string    `json:"target_conn_id"`
	MSISDN       string    `json:"msisdn"`
	PDUBody      []byte    `json:"pdu_body"`
	ESMClass     byte      `json:"esm_class"`
	SourceAddr   string    `json:"source_addr"`
	DestAddr     string    `json:"dest_addr"`
	EnqueuedAt   time.Time `json:"enqueued_at"`
}

// MessageStore provides disk-backed message storage using Pebble.
// Key namespaces:
//   - "msg:{smppMsgID}"                  -> SubmitRecord (message cache)
//   - "gw:{gwMsgID}"                     -> SubmitRecord (pre-ACK crash recovery)
//   - "retry:{timestamp}:{id}"          -> PendingDeliver (northbound retry queue)
//   - "submit-retry:{timestamp}:{id}"   -> PendingSubmit (southbound retry queue)
//
// Counts are approximate; atomic counters track entry counts so
// MessageCount()/PendingRetryCount() are O(1) instead of full prefix scans.
type MessageStore struct {
	db     *pebble.DB
	logger *zap.Logger

	// Incremental counters -- updated on write/delete, read via Load().
	msgCount         atomic.Int64 // msg: + gw: entries (approximate)
	retryCount       atomic.Int64 // retry: entries (approximate)
	submitRetryCount atomic.Int64 // submit-retry: entries (approximate)

	// Async batch writer channel. Writes are buffered and flushed periodically.
	writeCh chan writeOp
	stopCh  chan struct{}

	// closed is set to true before stopCh is closed, so that StoreSubmit
	// can avoid sending on writeCh after the batch writer has exited.
	closed atomic.Bool

	// writerWg tracks the batchWriteLoop goroutine so Close() can wait
	// for a clean drain before closing the database.
	writerWg sync.WaitGroup
}

// writeOp represents a single key-value write for the async batch writer.
type writeOp struct {
	key  []byte
	data []byte
}

// NewMessageStore opens a Pebble database at the given directory and starts
// the background batch writer goroutine.
func NewMessageStore(dataDir string, logger *zap.Logger) (*MessageStore, error) {
	opts := &pebble.Options{
		// Use modest cache; most reads are hot-path lookups that hit block cache
		Cache: pebble.NewCache(64 << 20), // 64MB
	}
	defer opts.Cache.Unref()

	db, err := pebble.Open(dataDir, opts)
	if err != nil {
		return nil, fmt.Errorf("open pebble store at %s: %w", dataDir, err)
	}

	s := &MessageStore{
		db:      db,
		logger:  logger,
		writeCh: make(chan writeOp, 4096),
		stopCh:  make(chan struct{}),
	}
	s.writerWg.Add(1)
	go s.batchWriteLoop()
	return s, nil
}

// StoreSubmit persists a submit record asynchronously via the batch writer.
// The write is buffered in a channel and flushed in batches by the background
// goroutine, removing Pebble WAL writes from the submit hot path.
//
// If the store is shutting down (closed flag set), the write falls back to a
// synchronous Pebble Set to avoid sending on the closed writeCh.
func (s *MessageStore) StoreSubmit(record *SubmitRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal submit record: %w", err)
	}
	// Key by SMSC message ID when known (for DLR lookup), otherwise by
	// gateway message ID (pre-downstream-ACK crash recovery).
	var key []byte
	if record.SmscMsgID != "" {
		key = []byte("msg:" + record.SmscMsgID)
	} else {
		key = []byte("gw:" + record.GwMsgID)
	}

	// If the store is shutting down, write synchronously to avoid sending
	// on the (possibly closed) writeCh.
	if s.closed.Load() {
		if err := s.db.Set(key, data, pebble.NoSync); err != nil {
			return err
		}
		s.msgCount.Add(1)
		return nil
	}

	select {
	case s.writeCh <- writeOp{key: key, data: data}:
		s.msgCount.Add(1)
		return nil
	default:
		// Channel full -- fall back to synchronous write.
		if err := s.db.Set(key, data, pebble.NoSync); err != nil {
			return err
		}
		s.msgCount.Add(1)
		return nil
	}
}

// GetSubmit retrieves a submit record by SMPP message ID.
func (s *MessageStore) GetSubmit(smppMsgID string) (*SubmitRecord, error) {
	key := []byte("msg:" + smppMsgID)
	data, closer, err := s.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get submit record: %w", err)
	}
	defer func() { _ = closer.Close() }()

	var record SubmitRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("unmarshal submit record: %w", err)
	}
	return &record, nil
}

// GetSubmitByGwID retrieves a submit record by gateway message ID.
// Used for crash recovery when the downstream SMSC ID is not yet known.
func (s *MessageStore) GetSubmitByGwID(gwMsgID string) (*SubmitRecord, error) {
	key := []byte("gw:" + gwMsgID)
	data, closer, err := s.db.Get(key)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get submit record by gw ID: %w", err)
	}
	defer func() { _ = closer.Close() }()

	var record SubmitRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("unmarshal submit record: %w", err)
	}
	return &record, nil
}

// DeleteSubmit removes a submit record keyed by SMSC message ID.
func (s *MessageStore) DeleteSubmit(smscMsgID string) error {
	key := []byte("msg:" + smscMsgID)
	if err := s.db.Delete(key, pebble.NoSync); err != nil {
		return err
	}
	s.msgCount.Add(-1)
	return nil
}

// DeleteSubmitByGwID removes a submit record keyed by gateway message ID.
func (s *MessageStore) DeleteSubmitByGwID(gwMsgID string) error {
	key := []byte("gw:" + gwMsgID)
	if err := s.db.Delete(key, pebble.NoSync); err != nil {
		return err
	}
	s.msgCount.Add(-1)
	return nil
}

// EnqueueRetry stores a pending deliver for later retry.
func (s *MessageStore) EnqueueRetry(id string, pending *PendingDeliver) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("marshal pending deliver: %w", err)
	}
	// Key sorts by time for ordered iteration
	key := []byte(fmt.Sprintf("retry:%020d:%s", pending.EnqueuedAt.UnixNano(), id))
	if err := s.db.Set(key, data, pebble.NoSync); err != nil {
		return err
	}
	s.retryCount.Add(1)
	return nil
}

// DrainRetries returns up to limit pending delivers, deletes them from the
// store using a Pebble batch, and discards entries older than maxAge.
// Pass limit <= 0 for unlimited (not recommended at scale).
func (s *MessageStore) DrainRetries(maxAge time.Duration, limit int) ([]*PendingDeliver, error) {
	upperKey := []byte(fmt.Sprintf("retry:%020d:", time.Now().UnixNano()))
	staleThreshold := time.Now().Add(-maxAge)

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("retry:"),
		UpperBound: upperKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create retry iterator: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var results []*PendingDeliver
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	deleted := 0

	for iter.First(); iter.Valid(); iter.Next() {
		if limit > 0 && deleted >= limit {
			break
		}

		var pending PendingDeliver
		val, err := iter.ValueAndErr()
		if err != nil {
			continue
		}
		if err := json.Unmarshal(val, &pending); err != nil {
			continue
		}
		keyCopy := make([]byte, len(iter.Key()))
		copy(keyCopy, iter.Key())
		_ = batch.Delete(keyCopy, pebble.NoSync)
		deleted++

		// Discard entries that are too old to retry.
		if pending.EnqueuedAt.Before(staleThreshold) {
			continue
		}
		results = append(results, &pending)
	}

	if err := batch.Commit(pebble.NoSync); err != nil {
		return nil, fmt.Errorf("commit retry drain batch: %w", err)
	}
	s.retryCount.Add(int64(-deleted))

	return results, nil
}

// EnqueueSubmitRetry stores a failed southbound submit for later retry.
func (s *MessageStore) EnqueueSubmitRetry(pending *PendingSubmit) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("marshal pending submit: %w", err)
	}
	key := []byte(fmt.Sprintf("submit-retry:%020d:%s", pending.EnqueuedAt.UnixNano(), pending.GwMsgID))
	if err := s.db.Set(key, data, pebble.NoSync); err != nil {
		return err
	}
	s.submitRetryCount.Add(1)
	return nil
}

// DrainSubmitRetries returns up to limit pending submit retries and deletes
// them using a Pebble batch. Pass limit <= 0 for unlimited.
func (s *MessageStore) DrainSubmitRetries(limit int) ([]*PendingSubmit, error) {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("submit-retry:"),
		UpperBound: []byte("submit-retry:\xff"),
	})
	if err != nil {
		return nil, fmt.Errorf("create submit retry iterator: %w", err)
	}
	defer func() { _ = iter.Close() }()

	var results []*PendingSubmit
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	deleted := 0

	for iter.First(); iter.Valid(); iter.Next() {
		if limit > 0 && deleted >= limit {
			break
		}

		var pending PendingSubmit
		val, err := iter.ValueAndErr()
		if err != nil {
			continue
		}
		if err := json.Unmarshal(val, &pending); err != nil {
			continue
		}
		results = append(results, &pending)
		keyCopy := make([]byte, len(iter.Key()))
		copy(keyCopy, iter.Key())
		_ = batch.Delete(keyCopy, pebble.NoSync)
		deleted++
	}

	if err := batch.Commit(pebble.NoSync); err != nil {
		return nil, fmt.Errorf("commit submit retry drain batch: %w", err)
	}
	s.submitRetryCount.Add(int64(-deleted))

	return results, nil
}

// PendingRetryCount returns the approximate number of entries in the retry queue. O(1).
func (s *MessageStore) PendingRetryCount() int {
	return int(s.retryCount.Load())
}

// PendingSubmitRetryCount returns the approximate number of entries in the submit retry queue. O(1).
func (s *MessageStore) PendingSubmitRetryCount() int {
	return int(s.submitRetryCount.Load())
}

// Cleanup removes submit records older than ttl from both msg: and gw: prefixes
// using a Pebble batch for efficient deletion.
func (s *MessageStore) Cleanup(ttl time.Duration) (int, error) {
	cutoff := time.Now().Add(-ttl)
	deleted := 0
	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()

	// Scan key prefixes: msg:{smscMsgID}, gw:{gwMsgID}, and status:{gwMsgID}.
	for _, prefix := range []string{"msg:", "gw:", "status:"} {
		iter, err := s.db.NewIter(&pebble.IterOptions{
			LowerBound: []byte(prefix),
			UpperBound: []byte(prefix + "\xff"),
		})
		if err != nil {
			return deleted, fmt.Errorf("create cleanup iterator for %s: %w", prefix, err)
		}

		for iter.First(); iter.Valid(); iter.Next() {
			val, err := iter.ValueAndErr()
			if err != nil {
				continue
			}
			// Extract timestamp from either record type.
			var ts struct {
				SubmittedAt time.Time `json:"submitted_at"`
				UpdatedAt   time.Time `json:"updated_at"`
			}
			if err := json.Unmarshal(val, &ts); err != nil {
				continue
			}
			t := ts.SubmittedAt
			if !ts.UpdatedAt.IsZero() {
				t = ts.UpdatedAt
			}
			if t.Before(cutoff) {
				keyCopy := make([]byte, len(iter.Key()))
				copy(keyCopy, iter.Key())
				_ = batch.Delete(keyCopy, pebble.NoSync)
				deleted++
			}
		}
		_ = iter.Close()
	}

	if deleted > 0 {
		if err := batch.Commit(pebble.NoSync); err != nil {
			return 0, fmt.Errorf("commit cleanup batch: %w", err)
		}
		s.msgCount.Add(int64(-deleted))
	}
	return deleted, nil
}

// RunCleanup periodically purges expired messages and retry entries.
func (s *MessageStore) RunCleanup(ctx context.Context, interval, ttl time.Duration, logger *zap.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			deleted, err := s.Cleanup(ttl)
			if err != nil {
				logger.Warn("store cleanup error", zap.Error(err))
			} else if deleted > 0 {
				logger.Info("store cleanup", zap.Int("deleted", deleted))
			}
		case <-ctx.Done():
			return
		}
	}
}

// MessageCount returns the approximate number of stored messages. O(1) via atomic counter.
func (s *MessageStore) MessageCount() int {
	return int(s.msgCount.Load())
}

// batchWriteLoop is the background goroutine that drains the writeCh and
// commits writes in batches. Flushes every 500 items or every 10ms,
// whichever comes first.
//
// On shutdown (stopCh closed), the loop drains all remaining items from
// writeCh, closes writeCh itself, flushes the final batch, and signals
// completion via writerWg.
func (s *MessageStore) batchWriteLoop() {
	defer s.writerWg.Done()

	const maxBatch = 500
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	batch := s.db.NewBatch()
	count := 0

	flush := func() {
		if count == 0 {
			return
		}
		if err := batch.Commit(pebble.NoSync); err != nil {
			s.logger.Error("batch write commit failed", zap.Error(err))
		}
		batch.Reset()
		count = 0
	}

	for {
		select {
		case op, ok := <-s.writeCh:
			if !ok {
				flush()
				_ = batch.Close()
				return
			}
			_ = batch.Set(op.key, op.data, pebble.NoSync)
			count++
			if count >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stopCh:
			// Drain remaining writes from the channel, then close it.
			// At this point s.closed is true, so no new sends will arrive.
			for {
				select {
				case op, ok := <-s.writeCh:
					if !ok {
						// Channel was already closed (should not happen, but handle gracefully).
						flush()
						_ = batch.Close()
						return
					}
					_ = batch.Set(op.key, op.data, pebble.NoSync)
					count++
				default:
					// Channel drained. Close it and flush the final batch.
					close(s.writeCh)
					flush()
					_ = batch.Close()
					return
				}
			}
		}
	}
}

// Close stops the batch writer and closes the Pebble database.
//
// Shutdown sequence:
//  1. Set closed flag so new StoreSubmit calls fall back to synchronous writes.
//  2. Close stopCh to tell batchWriteLoop to drain and exit.
//  3. Wait for batchWriteLoop to finish (writerWg) -- no sleep hack needed.
//  4. Close the Pebble database.
func (s *MessageStore) Close() error {
	// Mark as closed first so StoreSubmit stops sending to writeCh.
	s.closed.Store(true)

	select {
	case <-s.stopCh:
		// Already closed.
	default:
		close(s.stopCh)
	}

	// Wait for batchWriteLoop to drain and exit cleanly.
	s.writerWg.Wait()

	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// SetJSON stores a JSON-serialized value under the given key.
func (s *MessageStore) SetJSON(key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return s.db.Set([]byte(key), data, pebble.Sync)
}

// GetJSON retrieves and deserializes a JSON value by key.
func (s *MessageStore) GetJSON(key string, v any) error {
	data, closer, err := s.db.Get([]byte(key))
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	return json.Unmarshal(data, v)
}

// DeleteKey removes a key from the store.
func (s *MessageStore) DeleteKey(key string) error {
	return s.db.Delete([]byte(key), pebble.Sync)
}

// ScanPrefix iterates all keys with the given prefix, calling fn for each.
func (s *MessageStore) ScanPrefix(prefix string, fn func(key string, data []byte) error) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: prefixUpperBound([]byte(prefix)),
	})
	if err != nil {
		return err
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		val, err := iter.ValueAndErr()
		if err != nil {
			return err
		}
		if err := fn(string(iter.Key()), val); err != nil {
			return err
		}
	}
	return nil
}

// prefixUpperBound returns the upper bound for prefix iteration.
func prefixUpperBound(prefix []byte) []byte {
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	for i := len(upper) - 1; i >= 0; i-- {
		upper[i]++
		if upper[i] != 0 {
			return upper
		}
	}
	return nil
}

// MessageStatus tracks a message's lifecycle for REST API query.
// Stored under key "status:{gwMsgID}".
type MessageStatus struct {
	GwMsgID    string    `json:"gw_msg_id"`
	To         string    `json:"to"`
	From       string    `json:"from"`
	Reference  string    `json:"reference,omitempty"`
	Status     string    `json:"status"`     // accepted, forwarded, delivered, failed
	DLRStatus  string    `json:"dlr_status,omitempty"` // DELIVRD, UNDELIV, etc.
	SmscMsgID  string    `json:"smsc_msg_id,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SetMessageStatus persists a message lifecycle status record.
func (s *MessageStore) SetMessageStatus(st *MessageStatus) error {
	return s.SetJSON("status:"+st.GwMsgID, st)
}

// GetMessageStatus retrieves a message lifecycle status record.
func (s *MessageStore) GetMessageStatus(gwMsgID string) (*MessageStatus, error) {
	var st MessageStatus
	if err := s.GetJSON("status:"+gwMsgID, &st); err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &st, nil
}

// Ensure MessageStore implements io.Closer.
var _ io.Closer = (*MessageStore)(nil)
