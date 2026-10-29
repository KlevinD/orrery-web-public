package device

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type SQLRepository struct {
	store *db.Store
}

func NewSQLRepository(ctx context.Context, store *db.Store) (*SQLRepository, error) {
	if store == nil {
		return nil, errors.New("device work order repository requires runtime database")
	}
	repository := &SQLRepository{store: store}
	for _, statement := range deviceSchemaStatements(store.Dialect()) {
		if _, err := store.Exec(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize device work order repository: %w", err)
		}
	}
	return repository, nil
}

func (r *SQLRepository) Close() error { return nil }

func (r *SQLRepository) CreateOffer(ctx context.Context, record WorkOrderRecord, outbound queuedOutbound) (WorkOrderRecord, bool, error) {
	var stored WorkOrderRecord
	created := false
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		existing, found, err := getWorkOrderTx(ctx, tx, record.Order.WorkOrderID)
		if err != nil {
			return err
		}
		if found {
			if existing.TenantID != record.TenantID || existing.DeviceID != record.DeviceID {
				return &ProtocolError{Code: ProtocolTenantMismatch, Message: "work order identity belongs to another tenant or device"}
			}
			if existing.SessionID != record.SessionID || existing.Fingerprint != record.Fingerprint {
				return &ProtocolError{Code: ProtocolDuplicateConflict, Message: "work order ID was reused with different content"}
			}
			stored = existing
			return nil
		}
		var existingID string
		err = tx.QueryRowContext(ctx, `SELECT work_order_id FROM runtime_device_work_orders WHERE tenant_id = ? AND device_id = ? AND idempotency_key = ?`, record.TenantID, record.DeviceID, record.Order.IdempotencyKey).Scan(&existingID)
		if err == nil {
			return &ProtocolError{Code: ProtocolDuplicateConflict, Message: "idempotency key belongs to another work order"}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query device idempotency key: %w", err)
		}
		payload, err := json.Marshal(record.Order)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO runtime_device_work_orders (
 work_order_id, tenant_id, device_id, session_id, idempotency_key, fingerprint, payload_json,
 state, last_sequence, terminal_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?)`,
			record.Order.WorkOrderID, record.TenantID, record.DeviceID, record.SessionID, record.Order.IdempotencyKey, record.Fingerprint, payload,
			string(WorkOrderOffered), record.CreatedAt.UnixNano(), record.UpdatedAt.UnixNano())
		if err != nil {
			return fmt.Errorf("insert device work order: %w", err)
		}
		if err := queueOutboundTx(ctx, tx, record.TenantID, record.DeviceID, outbound); err != nil {
			return err
		}
		stored = record
		created = true
		return nil
	})
	return stored, created, err
}

func (r *SQLRepository) GetWorkOrder(ctx context.Context, tenantID, deviceID, workOrderID string) (WorkOrderRecord, bool, error) {
	record, found, err := getWorkOrderStore(ctx, r.store, workOrderID)
	if err != nil || !found {
		return record, found, err
	}
	if record.TenantID != tenantID || record.DeviceID != deviceID {
		return WorkOrderRecord{}, false, nil
	}
	return record, true, nil
}

func (r *SQLRepository) QueueCancel(ctx context.Context, tenantID, deviceID, workOrderID string, outbound queuedOutbound) (WorkOrderRecord, error) {
	var stored WorkOrderRecord
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		record, found, err := getWorkOrderTx(ctx, tx, workOrderID)
		if err != nil {
			return err
		}
		if !found || record.TenantID != tenantID || record.DeviceID != deviceID {
			return &ProtocolError{Code: ProtocolDeviceMismatch, Message: "work order is unavailable for device"}
		}
		stored = record
		if record.State.Terminal() {
			return nil
		}
		var existing int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM runtime_device_outbox WHERE tenant_id = ? AND device_id = ? AND work_order_id = ? AND message_type = ? LIMIT 1`, tenantID, deviceID, workOrderID, string(MessageWorkOrderCancel)).Scan(&existing)
		if err == nil {
			stored.State = WorkOrderCancelling
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query device cancel outbox: %w", err)
		}
		now := outbound.SentAt.UTC()
		result, err := tx.ExecContext(ctx, `UPDATE runtime_device_work_orders SET state = ?, updated_at = ? WHERE work_order_id = ? AND tenant_id = ? AND device_id = ?`, string(WorkOrderCancelling), now.UnixNano(), workOrderID, tenantID, deviceID)
		if err != nil {
			return fmt.Errorf("mark device work order cancelling: %w", err)
		}
		if err := requireAffected(result, "mark device work order cancelling"); err != nil {
			return err
		}
		if err := queueOutboundTx(ctx, tx, tenantID, deviceID, outbound); err != nil {
			return err
		}
		stored.State, stored.UpdatedAt = WorkOrderCancelling, now
		return nil
	})
	return stored, err
}

func (r *SQLRepository) ApplyInbound(ctx context.Context, tenantID, deviceID string, envelope Envelope) (applyInboundResult, error) {
	var result applyInboundResult
	err := r.store.WithTx(ctx, func(tx db.Tx) error {
		payload, err := json.Marshal(envelope.Payload)
		if err != nil {
			return err
		}
		insert, err := tx.ExecContext(ctx, `
INSERT INTO runtime_device_inbound_messages (tenant_id, device_id, connection_epoch, message_id, message_type, payload_json, received_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tenant_id, device_id, message_id) DO NOTHING`, tenantID, deviceID, envelope.ConnectionEpoch, envelope.MessageID, string(envelope.Type), payload, envelope.SentAt.UnixNano())
		if err != nil {
			return fmt.Errorf("record device inbound message: %w", err)
		}
		affected, err := insert.RowsAffected()
		if err != nil {
			return fmt.Errorf("read device inbound insert count: %w", err)
		}
		if affected == 0 {
			var existingType string
			var existingPayload []byte
			if err := tx.QueryRowContext(ctx, `SELECT message_type, payload_json FROM runtime_device_inbound_messages WHERE tenant_id = ? AND device_id = ? AND message_id = ?`, tenantID, deviceID, envelope.MessageID).Scan(&existingType, &existingPayload); err != nil {
				return err
			}
			if existingType != string(envelope.Type) || string(existingPayload) != string(payload) {
				return &ProtocolError{Code: ProtocolDuplicateConflict, Message: "message ID was reused with different content"}
			}
			result.Duplicate = true
			return nil
		}

		switch envelope.Type {
		case MessageDeviceHello:
			hello := envelope.Payload.(DeviceHelloPayload)
			active, _ := json.Marshal(hello.ActiveWorkOrders)
			_, err := tx.ExecContext(ctx, `
INSERT INTO runtime_device_leases (tenant_id, device_id, connection_epoch, client_version, aioncore_version, capability_digest, active_work_orders_json, replay_cursor, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?, '', ?)
ON CONFLICT(tenant_id, device_id) DO UPDATE SET connection_epoch=excluded.connection_epoch, client_version=excluded.client_version, aioncore_version=excluded.aioncore_version, capability_digest=excluded.capability_digest, active_work_orders_json=excluded.active_work_orders_json, last_seen_at=excluded.last_seen_at`, tenantID, deviceID, envelope.ConnectionEpoch, hello.ClientVersion, hello.AionCoreVersion, hello.CapabilityDigest, active, envelope.SentAt.UnixNano())
			return err
		case MessageDeviceHeartbeat:
			heartbeat := envelope.Payload.(DeviceHeartbeatPayload)
			active, _ := json.Marshal(heartbeat.ActiveWorkOrders)
			update, err := tx.ExecContext(ctx, `UPDATE runtime_device_leases SET active_work_orders_json = ?, replay_cursor = ?, last_seen_at = ? WHERE tenant_id = ? AND device_id = ? AND connection_epoch = ?`, active, heartbeat.LastReplayCursor, envelope.SentAt.UnixNano(), tenantID, deviceID, envelope.ConnectionEpoch)
			if err != nil {
				return err
			}
			return requireAffected(update, "update device heartbeat lease")
		}

		workOrderID, err := envelopeWorkOrderID(envelope)
		if err != nil {
			return err
		}
		record, found, err := getWorkOrderTx(ctx, tx, workOrderID)
		if err != nil {
			return err
		}
		if !found || record.TenantID != tenantID || record.DeviceID != deviceID {
			return &ProtocolError{Code: ProtocolDeviceMismatch, Message: "device message references an unavailable work order"}
		}
		result.Record = &record
		switch envelope.Type {
		case MessageWorkOrderAccepted:
			accepted := envelope.Payload.(WorkOrderAcceptedPayload)
			if record.State != WorkOrderOffered && record.State != WorkOrderCancelling {
				if record.State == WorkOrderAccepted || record.State == WorkOrderRunning || record.State == WorkOrderWaitingLocalApproval || record.State == WorkOrderDetached {
					return deleteOutboxTx(ctx, tx, tenantID, deviceID, workOrderID, MessageWorkOrderOffer)
				}
				return &ProtocolError{Code: ProtocolTerminalConflict, Message: "accepted message arrived after terminal work order state"}
			}
			if record.State == WorkOrderCancelling {
				alreadyAccepted, err := workOrderEventExistsTx(ctx, tx, tenantID, deviceID, workOrderID, BrokerAccepted)
				if err != nil {
					return err
				}
				if alreadyAccepted {
					return deleteOutboxTx(ctx, tx, tenantID, deviceID, workOrderID, MessageWorkOrderOffer)
				}
			}
			cursor, err := appendEventTx(ctx, tx, record, envelope.MessageID, BrokerAccepted, accepted, envelope.SentAt)
			if err != nil {
				return err
			}
			nextState := WorkOrderAccepted
			if record.State == WorkOrderCancelling {
				nextState = WorkOrderCancelling
			}
			if _, err := tx.ExecContext(ctx, `UPDATE runtime_device_work_orders SET state = ?, updated_at = ? WHERE work_order_id = ?`, string(nextState), envelope.SentAt.UnixNano(), workOrderID); err != nil {
				return err
			}
			if err := deleteOutboxTx(ctx, tx, tenantID, deviceID, workOrderID, MessageWorkOrderOffer); err != nil {
				return err
			}
			record.State, record.UpdatedAt = nextState, envelope.SentAt
			result.Record = &record
			result.Event = &BrokerEvent{Cursor: cursor, MessageID: envelope.MessageID, Kind: BrokerAccepted, Accepted: &accepted, OccurredAt: envelope.SentAt}
		case MessageWorkOrderRejected:
			rejected := envelope.Payload.(WorkOrderRejectedPayload)
			if record.State == WorkOrderCancelling {
				alreadyAccepted, err := workOrderEventExistsTx(ctx, tx, tenantID, deviceID, workOrderID, BrokerAccepted)
				if err != nil {
					return err
				}
				if alreadyAccepted {
					return &ProtocolError{Code: ProtocolTerminalConflict, Message: "rejected message conflicts with accepted work order"}
				}
			} else if record.State != WorkOrderOffered {
				return &ProtocolError{Code: ProtocolTerminalConflict, Message: "rejected message conflicts with current work order state"}
			}
			cursor, err := appendEventTx(ctx, tx, record, envelope.MessageID, BrokerRejected, rejected, envelope.SentAt)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE runtime_device_work_orders SET state = ?, updated_at = ? WHERE work_order_id = ?`, string(WorkOrderRejected), envelope.SentAt.UnixNano(), workOrderID); err != nil {
				return err
			}
			if err := deleteAllOutboxTx(ctx, tx, tenantID, deviceID, workOrderID); err != nil {
				return err
			}
			record.State, record.UpdatedAt = WorkOrderRejected, envelope.SentAt
			result.Record = &record
			result.Event = &BrokerEvent{Cursor: cursor, MessageID: envelope.MessageID, Kind: BrokerRejected, Rejected: &rejected, OccurredAt: envelope.SentAt}
		case MessageWorkOrderFrame:
			frame := envelope.Payload.(WorkOrderFramePayload)
			if record.State.Terminal() {
				return &ProtocolError{Code: ProtocolTerminalConflict, Message: "frame arrived after terminal work order state"}
			}
			if record.State == WorkOrderOffered {
				return &ProtocolError{Code: ProtocolSequenceGap, Message: "frame arrived before work order acceptance"}
			}
			if frame.Sequence != record.LastSequence+1 {
				return &ProtocolError{Code: ProtocolSequenceGap, Message: fmt.Sprintf("expected work order sequence %d, received %d", record.LastSequence+1, frame.Sequence)}
			}
			nextState := frameState(record.State, frame.Frame)
			cursor, err := appendEventTx(ctx, tx, record, envelope.MessageID, BrokerFrame, frame, envelope.SentAt)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE runtime_device_work_orders SET state = ?, last_sequence = ?, updated_at = ? WHERE work_order_id = ?`, string(nextState), frame.Sequence, envelope.SentAt.UnixNano(), workOrderID); err != nil {
				return err
			}
			record.State, record.LastSequence, record.UpdatedAt = nextState, frame.Sequence, envelope.SentAt
			result.Record = &record
			result.Event = &BrokerEvent{Cursor: cursor, MessageID: envelope.MessageID, Kind: BrokerFrame, Frame: &frame, OccurredAt: envelope.SentAt}
		case MessageWorkOrderTerminal:
			terminal := envelope.Payload.(WorkOrderTerminalPayload)
			if record.State.Terminal() {
				return &ProtocolError{Code: ProtocolTerminalConflict, Message: "terminal message conflicts with an existing terminal"}
			}
			if record.State == WorkOrderOffered {
				return &ProtocolError{Code: ProtocolSequenceGap, Message: "terminal arrived before work order acceptance"}
			}
			if terminal.Sequence != record.LastSequence+1 {
				return &ProtocolError{Code: ProtocolSequenceGap, Message: fmt.Sprintf("expected terminal sequence %d, received %d", record.LastSequence+1, terminal.Sequence)}
			}
			nextState := terminalState(terminal.Status)
			cursor, err := appendEventTx(ctx, tx, record, envelope.MessageID, BrokerTerminal, terminal, envelope.SentAt)
			if err != nil {
				return err
			}
			terminalJSON, _ := json.Marshal(terminal)
			if _, err := tx.ExecContext(ctx, `UPDATE runtime_device_work_orders SET state = ?, last_sequence = ?, terminal_json = ?, updated_at = ? WHERE work_order_id = ?`, string(nextState), terminal.Sequence, terminalJSON, envelope.SentAt.UnixNano(), workOrderID); err != nil {
				return err
			}
			if err := deleteAllOutboxTx(ctx, tx, tenantID, deviceID, workOrderID); err != nil {
				return err
			}
			record.State, record.LastSequence, record.Terminal, record.UpdatedAt = nextState, terminal.Sequence, &terminal, envelope.SentAt
			result.Record = &record
			result.Event = &BrokerEvent{Cursor: cursor, MessageID: envelope.MessageID, Kind: BrokerTerminal, Terminal: &terminal, OccurredAt: envelope.SentAt}
		case MessageArtifactManifest:
			manifest := envelope.Payload.(ArtifactManifestPayload)
			if record.State == WorkOrderOffered {
				return &ProtocolError{Code: ProtocolSequenceGap, Message: "artifact manifest arrived before work order acceptance"}
			}
			if record.State == WorkOrderRejected {
				return &ProtocolError{Code: ProtocolTerminalConflict, Message: "artifact manifest arrived for rejected work order"}
			}
			cursor, err := appendEventTx(ctx, tx, record, envelope.MessageID, BrokerArtifact, manifest, envelope.SentAt)
			if err != nil {
				return err
			}
			result.Event = &BrokerEvent{Cursor: cursor, MessageID: envelope.MessageID, Kind: BrokerArtifact, Artifact: &manifest, OccurredAt: envelope.SentAt}
		}
		return nil
	})
	return result, err
}

func (r *SQLRepository) ListEvents(ctx context.Context, tenantID, deviceID, workOrderID string, afterCursor int64) ([]BrokerEvent, error) {
	rows, err := r.store.Query(ctx, `SELECT cursor, message_id, event_type, payload_json, occurred_at FROM runtime_device_work_order_events WHERE tenant_id = ? AND device_id = ? AND work_order_id = ? AND cursor > ? ORDER BY cursor ASC`, tenantID, deviceID, workOrderID, afterCursor)
	if err != nil {
		return nil, fmt.Errorf("list device work order events: %w", err)
	}
	defer rows.Close()
	var events []BrokerEvent
	for rows.Next() {
		var event BrokerEvent
		var kind string
		var payload []byte
		var occurredAt int64
		if err := rows.Scan(&event.Cursor, &event.MessageID, &kind, &payload, &occurredAt); err != nil {
			return nil, err
		}
		event.Kind, event.OccurredAt = BrokerEventKind(kind), time.Unix(0, occurredAt).UTC()
		if err := decodeBrokerEventPayload(&event, payload); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *SQLRepository) ListOutbound(ctx context.Context, tenantID, deviceID string) ([]queuedOutbound, error) {
	rows, err := r.store.Query(ctx, `SELECT ordinal, message_id, message_type, work_order_id, sent_at, payload_json FROM runtime_device_outbox WHERE tenant_id = ? AND device_id = ? ORDER BY ordinal ASC`, tenantID, deviceID)
	if err != nil {
		return nil, fmt.Errorf("list device outbound messages: %w", err)
	}
	defer rows.Close()
	var messages []queuedOutbound
	for rows.Next() {
		var message queuedOutbound
		var kind string
		var sentAt int64
		var payload []byte
		if err := rows.Scan(&message.Ordinal, &message.MessageID, &kind, &message.WorkOrderID, &sentAt, &payload); err != nil {
			return nil, err
		}
		message.Type, message.SentAt = MessageType(kind), time.Unix(0, sentAt).UTC()
		switch message.Type {
		case MessageWorkOrderOffer:
			var value WorkOrder
			if err := json.Unmarshal(payload, &value); err != nil {
				return nil, err
			}
			message.Payload = value
		case MessageWorkOrderCancel:
			var value WorkOrderCancelPayload
			if err := json.Unmarshal(payload, &value); err != nil {
				return nil, err
			}
			message.Payload = value
		default:
			return nil, fmt.Errorf("unsupported device outbox message type %q", message.Type)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (r *SQLRepository) LastCursor(ctx context.Context, tenantID, deviceID string) (int64, error) {
	var cursor sql.NullInt64
	err := r.store.QueryRow(ctx, `SELECT MAX(cursor) FROM runtime_device_work_order_events WHERE tenant_id = ? AND device_id = ?`, tenantID, deviceID).Scan(&cursor)
	if err != nil {
		return 0, err
	}
	if !cursor.Valid {
		return 0, nil
	}
	return cursor.Int64, nil
}

func getWorkOrderStore(ctx context.Context, store *db.Store, workOrderID string) (WorkOrderRecord, bool, error) {
	return scanWorkOrder(store.QueryRow(ctx, workOrderSelect+` WHERE work_order_id = ?`, workOrderID))
}

func getWorkOrderTx(ctx context.Context, tx db.Tx, workOrderID string) (WorkOrderRecord, bool, error) {
	return scanWorkOrder(tx.QueryRowContext(ctx, workOrderSelect+` WHERE work_order_id = ?`, workOrderID))
}

const workOrderSelect = `SELECT tenant_id, device_id, session_id, fingerprint, payload_json, state, last_sequence, terminal_json, created_at, updated_at FROM runtime_device_work_orders`

type rowScanner interface{ Scan(...any) error }

func scanWorkOrder(row rowScanner) (WorkOrderRecord, bool, error) {
	var record WorkOrderRecord
	var payload []byte
	var state string
	var terminal []byte
	var createdAt, updatedAt int64
	err := row.Scan(&record.TenantID, &record.DeviceID, &record.SessionID, &record.Fingerprint, &payload, &state, &record.LastSequence, &terminal, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkOrderRecord{}, false, nil
	}
	if err != nil {
		return WorkOrderRecord{}, false, fmt.Errorf("load device work order: %w", err)
	}
	if err := json.Unmarshal(payload, &record.Order); err != nil {
		return WorkOrderRecord{}, false, fmt.Errorf("decode device work order: %w", err)
	}
	if len(terminal) != 0 {
		var value WorkOrderTerminalPayload
		if err := json.Unmarshal(terminal, &value); err != nil {
			return WorkOrderRecord{}, false, err
		}
		record.Terminal = &value
	}
	record.State, record.CreatedAt, record.UpdatedAt = WorkOrderState(state), time.Unix(0, createdAt).UTC(), time.Unix(0, updatedAt).UTC()
	return record, true, nil
}

func queueOutboundTx(ctx context.Context, tx db.Tx, tenantID, deviceID string, outbound queuedOutbound) error {
	next, err := allocateDeviceCounterTx(ctx, tx, "runtime_device_outbox_cursors", "ordinal", tenantID, deviceID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(outbound.Payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_device_outbox (tenant_id, device_id, ordinal, message_id, message_type, work_order_id, sent_at, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, tenantID, deviceID, next, outbound.MessageID, string(outbound.Type), outbound.WorkOrderID, outbound.SentAt.UnixNano(), payload)
	return err
}

func appendEventTx(ctx context.Context, tx db.Tx, record WorkOrderRecord, messageID string, kind BrokerEventKind, payload any, occurredAt time.Time) (int64, error) {
	next, err := allocateDeviceCounterTx(ctx, tx, "runtime_device_event_cursors", "cursor", record.TenantID, record.DeviceID)
	if err != nil {
		return 0, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_device_work_order_events (tenant_id, device_id, work_order_id, cursor, message_id, event_type, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, record.TenantID, record.DeviceID, record.Order.WorkOrderID, next, messageID, string(kind), data, occurredAt.UnixNano())
	return next, err
}

func workOrderEventExistsTx(ctx context.Context, tx db.Tx, tenantID, deviceID, workOrderID string, kind BrokerEventKind) (bool, error) {
	var present int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM runtime_device_work_order_events WHERE tenant_id = ? AND device_id = ? AND work_order_id = ? AND event_type = ? LIMIT 1`, tenantID, deviceID, workOrderID, string(kind)).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func allocateDeviceCounterTx(ctx context.Context, tx db.Tx, table, column, tenantID, deviceID string) (int64, error) {
	if (table != "runtime_device_event_cursors" || column != "cursor") && (table != "runtime_device_outbox_cursors" || column != "ordinal") {
		return 0, errors.New("unsupported device counter")
	}
	query := fmt.Sprintf(`INSERT INTO %s (tenant_id, device_id, %s) VALUES (?, ?, 1)
ON CONFLICT(tenant_id, device_id) DO UPDATE SET %s = %s.%s + 1
RETURNING %s`, table, column, column, table, column, column)
	var next int64
	if err := tx.QueryRowContext(ctx, query, tenantID, deviceID).Scan(&next); err != nil {
		return 0, fmt.Errorf("allocate device %s: %w", column, err)
	}
	return next, nil
}

func deleteOutboxTx(ctx context.Context, tx db.Tx, tenantID, deviceID, workOrderID string, kind MessageType) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM runtime_device_outbox WHERE tenant_id = ? AND device_id = ? AND work_order_id = ? AND message_type = ?`, tenantID, deviceID, workOrderID, string(kind))
	return err
}

func deleteAllOutboxTx(ctx context.Context, tx db.Tx, tenantID, deviceID, workOrderID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM runtime_device_outbox WHERE tenant_id = ? AND device_id = ? AND work_order_id = ?`, tenantID, deviceID, workOrderID)
	return err
}

func envelopeWorkOrderID(envelope Envelope) (string, error) {
	switch payload := envelope.Payload.(type) {
	case WorkOrderAcceptedPayload:
		return payload.WorkOrderID, nil
	case WorkOrderRejectedPayload:
		return payload.WorkOrderID, nil
	case WorkOrderFramePayload:
		return payload.WorkOrderID, nil
	case WorkOrderTerminalPayload:
		return payload.WorkOrderID, nil
	case ArtifactManifestPayload:
		return payload.WorkOrderID, nil
	default:
		return "", &ProtocolError{Code: ProtocolInvalidSchema, Message: "device message has no work order identity"}
	}
}

func frameState(current WorkOrderState, frame RuntimeFrame) WorkOrderState {
	switch {
	case current == WorkOrderCancelling:
		return WorkOrderCancelling
	case frame.Kind == "permission" && frame.Phase == "waiting":
		return WorkOrderWaitingLocalApproval
	case frame.Kind == "detached":
		return WorkOrderDetached
	case frame.Kind == "permission" && frame.Phase == "resolved":
		return WorkOrderRunning
	default:
		return WorkOrderRunning
	}
}

func terminalState(status TerminalStatus) WorkOrderState {
	switch status {
	case TerminalSucceeded:
		return WorkOrderSucceeded
	case TerminalCancelled:
		return WorkOrderCancelled
	default:
		return WorkOrderFailed
	}
}

func decodeBrokerEventPayload(event *BrokerEvent, payload []byte) error {
	switch event.Kind {
	case BrokerAccepted:
		var value WorkOrderAcceptedPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		event.Accepted = &value
	case BrokerRejected:
		var value WorkOrderRejectedPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		event.Rejected = &value
	case BrokerFrame:
		var value WorkOrderFramePayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		event.Frame = &value
	case BrokerTerminal:
		var value WorkOrderTerminalPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		event.Terminal = &value
	case BrokerArtifact:
		var value ArtifactManifestPayload
		if err := json.Unmarshal(payload, &value); err != nil {
			return err
		}
		event.Artifact = &value
	default:
		return fmt.Errorf("unknown device broker event kind %q", event.Kind)
	}
	return nil
}

func requireAffected(result sql.Result, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s: no rows affected", action)
	}
	return nil
}

func deviceSchemaStatements(dialect db.Dialect) []string {
	bytesType, timeType := "BLOB", "INTEGER"
	if dialect == db.DialectPostgres {
		bytesType, timeType = "BYTEA", "BIGINT"
	}
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_work_orders (
 work_order_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, session_id TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, fingerprint TEXT NOT NULL, payload_json %s NOT NULL, state TEXT NOT NULL,
 last_sequence %[2]s NOT NULL DEFAULT 0, terminal_json %s, created_at %[2]s NOT NULL, updated_at %[2]s NOT NULL,
 UNIQUE (tenant_id, device_id, idempotency_key)
)`, bytesType, timeType, bytesType),
		`CREATE INDEX IF NOT EXISTS idx_runtime_device_work_orders_scope ON runtime_device_work_orders (tenant_id, device_id, state, updated_at)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_event_cursors (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, cursor %s NOT NULL,
 PRIMARY KEY (tenant_id, device_id)
)`, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_work_order_events (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, work_order_id TEXT NOT NULL, cursor %s NOT NULL,
 message_id TEXT NOT NULL, event_type TEXT NOT NULL, payload_json %s NOT NULL, occurred_at %[1]s NOT NULL,
 PRIMARY KEY (work_order_id, cursor), UNIQUE (tenant_id, device_id, cursor), UNIQUE (tenant_id, device_id, message_id)
)`, timeType, bytesType),
		`CREATE INDEX IF NOT EXISTS idx_runtime_device_events_scope ON runtime_device_work_order_events (tenant_id, device_id, work_order_id, cursor)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_inbound_messages (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, connection_epoch TEXT NOT NULL, message_id TEXT NOT NULL,
 message_type TEXT NOT NULL, payload_json %s NOT NULL, received_at %s NOT NULL,
 PRIMARY KEY (tenant_id, device_id, message_id)
)`, bytesType, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_outbox_cursors (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, ordinal %s NOT NULL,
 PRIMARY KEY (tenant_id, device_id)
)`, timeType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_outbox (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, ordinal %s NOT NULL, message_id TEXT NOT NULL UNIQUE,
 message_type TEXT NOT NULL, work_order_id TEXT NOT NULL, sent_at %[1]s NOT NULL, payload_json %s NOT NULL,
 PRIMARY KEY (tenant_id, device_id, ordinal)
)`, timeType, bytesType),
		`CREATE INDEX IF NOT EXISTS idx_runtime_device_outbox_scope ON runtime_device_outbox (tenant_id, device_id, ordinal)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS runtime_device_leases (
 tenant_id TEXT NOT NULL, device_id TEXT NOT NULL, connection_epoch TEXT NOT NULL, client_version TEXT NOT NULL,
 aioncore_version TEXT NOT NULL, capability_digest TEXT NOT NULL, active_work_orders_json %s NOT NULL,
 replay_cursor TEXT NOT NULL, last_seen_at %s NOT NULL, PRIMARY KEY (tenant_id, device_id)
)`, bytesType, timeType),
	}
}

func RegisterMigrations(coordinator *migration.Coordinator) error {
	if coordinator == nil {
		return nil
	}
	return coordinator.RegisterInitializer(db.RoleRuntime, migration.Migration{
		Version:     "0190_runtime_device_bridge",
		Description: "persist Device Runtime work orders, ordered events, replay outbox, dedupe, and leases",
		Statements:  deviceSchemaStatements(db.DialectSQLite),
		StatementsByDialect: map[db.Dialect][]string{
			db.DialectPostgres: deviceSchemaStatements(db.DialectPostgres),
		},
	})
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

var _ Repository = (*SQLRepository)(nil)
