package reliability

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/platform/logging"
)

type FailureObserver interface {
	ObserveFailure(context.Context, FailureObservation) (FailureIncident, error)
}

type CapturedFailure struct {
	Observation FailureObservation `json:"observation"`
	Diagnostics map[string]string  `json:"diagnostics,omitempty"`
	IncidentID  string             `json:"incident_id,omitempty"`
}

type CaptureStatus struct {
	Quarantined     int       `json:"quarantined"`
	Pending         int       `json:"pending"`
	Delivered       int       `json:"delivered_this_process"`
	LastError       string    `json:"last_error,omitempty"`
	LastDeliveredAt time.Time `json:"last_delivered_at,omitempty"`
}

// CaptureInbox durably accepts diagnostics before acknowledging them. Delivery
// reuses observation IDs, so a crash between commit and acknowledgement cannot
// increment an incident twice. No LLM calls are made by this queue.
type CaptureInbox struct {
	dir      string
	observer FailureObserver
	mu       sync.Mutex
	drain    sync.Mutex
	status   CaptureStatus
}

func NewCaptureInbox(dir string, observer FailureObserver) (*CaptureInbox, error) {
	if strings.TrimSpace(dir) == "" || observer == nil {
		return nil, fmt.Errorf("capture inbox requires directory and observer")
	}
	for _, part := range []string{"pending", "history", "quarantine", "repairs"} {
		if err := os.MkdirAll(filepath.Join(dir, part), 0700); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "pending"))
	if err != nil {
		return nil, err
	}
	q := &CaptureInbox{dir: dir, observer: observer}
	quarantine, err := os.ReadDir(filepath.Join(dir, "quarantine"))
	if err != nil {
		return nil, err
	}
	for _, e := range quarantine {
		if strings.HasSuffix(e.Name(), ".json") {
			q.status.Quarantined++
		}
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			q.status.Pending++
		}
	}
	return q, nil
}

func NewCaptureID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("capture-%d", time.Now().UnixNano())
	}
	return "capture-" + hex.EncodeToString(b[:])
}
func captureKey(tenant, id string) string {
	h := sha256.Sum256([]byte(tenant + "\x00" + id))
	return hex.EncodeToString(h[:]) + ".json"
}

func (q *CaptureInbox) Enqueue(ctx context.Context, record CapturedFailure) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	record.Observation = normalizeObservation(record.Observation, time.Now())
	if record.Observation.ID == "" {
		record.Observation.ID = NewCaptureID()
	}
	if err := validateObservation(record.Observation); err != nil {
		return err
	}
	record.Observation.Message = logging.RedactDiagnostic(record.Observation.Message, 4096)
	clean := map[string]string{}
	for _, key := range []string{"cause", "stack", "path", "request_id", "run_id", "task_id", "runtime_kind", "operation", "error_kind", "sdk_status_code", "account_id", "storage_capacity"} {
		if value := record.Diagnostics[key]; value != "" {
			clean[key] = logging.RedactDiagnostic(value, 4096)
		}
	}
	record.Diagnostics = clean
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > 48*1024 {
		return fmt.Errorf("capture diagnostic exceeds size limit")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	path := filepath.Join(q.dir, "pending", captureKey(record.Observation.TenantID, record.Observation.ID))
	if _, err := os.Stat(filepath.Join(q.dir, "history", filepath.Base(path))); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if q.status.Pending >= 10000 {
		return fmt.Errorf("capture inbox is full; source logs remain available")
	}
	if err := atomicCaptureWrite(path, data); err != nil {
		q.status.LastError = redactFailureSummary(err.Error())
		return err
	}
	q.status.Pending++
	return nil
}

func atomicCaptureWrite(path string, data []byte) error {
	if previous, err := os.ReadFile(path); err == nil {
		if bytes.Equal(previous, data) {
			return nil
		}
		return fmt.Errorf("capture ID already contains a different diagnostic")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".capture-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (q *CaptureInbox) Status() CaptureStatus { q.mu.Lock(); defer q.mu.Unlock(); return q.status }
func (q *CaptureInbox) Sweep(ctx context.Context) (result error) {
	q.drain.Lock()
	defer q.drain.Unlock()
	defer func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if result != nil {
			q.status.LastError = redactFailureSummary(result.Error())
		} else {
			q.status.LastError = ""
		}
	}()
	defer func() { result = errors.Join(result, q.reconcileAutomaticRepairs(ctx)) }()
	entries, err := os.ReadDir(filepath.Join(q.dir, "pending"))
	if err != nil {
		return err
	}
	delivered := 0
	var invalidErr error
	for _, entry := range entries {
		if delivered >= 100 {
			break
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(q.dir, "pending", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var record CapturedFailure
		err = json.Unmarshal(data, &record)
		if err == nil {
			err = validateObservation(record.Observation)
		}
		if err != nil {
			if moveErr := os.Rename(path, filepath.Join(q.dir, "quarantine", entry.Name())); moveErr != nil {
				return moveErr
			}
			q.mu.Lock()
			q.status.Pending--
			q.status.Quarantined++
			q.mu.Unlock()
			invalidErr = fmt.Errorf("invalid capture quarantined: %s", entry.Name())
			continue
		}
		incident, err := q.observer.ObserveFailure(logging.WithoutObservation(ctx), record.Observation)
		if err != nil {
			return err
		}
		record.IncidentID = incident.ID
		data, err = json.Marshal(record)
		if err != nil {
			return err
		}
		if err = atomicCaptureWrite(filepath.Join(q.dir, "history", entry.Name()), data); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		q.mu.Lock()
		q.status.Pending--
		q.status.Delivered++
		q.status.LastDeliveredAt = time.Now().UTC()
		q.mu.Unlock()
		delivered++
	}
	if delivered > 0 {
		return errors.Join(invalidErr, q.pruneHistory())
	}
	return invalidErr
}

func (q *CaptureInbox) pruneHistory() error {
	entries, err := os.ReadDir(filepath.Join(q.dir, "history"))
	if err != nil {
		return err
	}
	type item struct {
		name string
		at   time.Time
	}
	items := make([]item, 0, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		items = append(items, item{e.Name(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })
	for len(items) > 2048 {
		if err := os.Remove(filepath.Join(q.dir, "history", items[0].name)); err != nil {
			return err
		}
		items = items[1:]
	}
	return nil
}

func (q *CaptureInbox) Diagnostics(tenantID, incidentID string) ([]CapturedFailure, error) {
	entries, err := os.ReadDir(filepath.Join(q.dir, "history"))
	if err != nil {
		return nil, err
	}
	out := []CapturedFailure{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(q.dir, "history", e.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var record CapturedFailure
		if err = json.Unmarshal(data, &record); err != nil {
			return nil, err
		}
		if record.Observation.TenantID == tenantID && record.IncidentID == incidentID {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Observation.OccurredAt.After(out[j].Observation.OccurredAt) })
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}

// HasPendingRecurrence fences recovery while a new observation for this exact
// incident binding is waiting to reach the ledger. An unrelated provider or
// business failure must not prevent a healthy dependency from recovering.
func (q *CaptureInbox) HasPendingRecurrence(incident FailureIncident) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(q.dir, "pending"))
	if err != nil {
		return false, err
	}
	if len(entries) > 1000 {
		return true, nil
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if info.Size() > 48*1024 {
			return true, nil
		}
		data, err := os.ReadFile(filepath.Join(q.dir, "pending", entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		var record CapturedFailure
		if err := json.Unmarshal(data, &record); err != nil {
			return false, err
		}
		o := record.Observation
		if o.TenantID == incident.TenantID && o.Source == incident.Source && o.Component == incident.Component && o.ErrorCode == incident.ErrorCode && !o.OccurredAt.Before(incident.LastSeenAt) {
			return true, nil
		}
	}
	return false, nil
}
