package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestDurableBrokerOfferAcceptanceFramesCancelTerminalAndIsolation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	offer := receiveOutbound(t, link)
	if offer.Type != MessageWorkOrderOffer || offer.Payload.(WorkOrder).WorkOrderID != record.Order.WorkOrderID {
		t.Fatalf("offer = %#v", offer)
	}

	sendDeviceEnvelope(t, link, now, "msg_accept_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now})
	accepted := receiveBrokerEvent(t, subscription)
	if accepted.Kind != BrokerAccepted {
		t.Fatalf("accepted event = %#v", accepted)
	}
	// Exact message replay is idempotent and does not publish a second event.
	sendDeviceEnvelope(t, link, now, "msg_accept_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now})

	permission := RuntimeFrame{Kind: "permission", Phase: "waiting", CallID: "call_permission_001", Category: "shell"}
	sendDeviceEnvelope(t, link, now, "msg_frame_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 1, Frame: permission})
	if event := receiveBrokerEvent(t, subscription); event.Kind != BrokerFrame || event.Frame.Sequence != 1 {
		t.Fatalf("permission event = %#v", event)
	}
	if err := broker.Cancel(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID, "operator cancel"); err != nil {
		t.Fatal(err)
	}
	cancel := receiveOutbound(t, link)
	if cancel.Type != MessageWorkOrderCancel {
		t.Fatalf("cancel = %#v", cancel)
	}

	terminal := WorkOrderTerminalPayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 2, Status: TerminalCancelled, ReasonCode: TerminalCancelledByCloud}
	sendDeviceEnvelope(t, link, now, "msg_terminal_001", MessageWorkOrderTerminal, terminal)
	if event := receiveBrokerEvent(t, subscription); event.Kind != BrokerTerminal || event.Terminal.Status != TerminalCancelled {
		t.Fatalf("terminal event = %#v", event)
	}
	stored, found, err := repository.GetWorkOrder(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID)
	if err != nil || !found || stored.State != WorkOrderCancelled || stored.LastSequence != 2 {
		t.Fatalf("stored = %#v/%v/%v", stored, found, err)
	}

	if err := receiveDeviceEnvelope(link, now, "msg_late_001", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 3, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: "late"}}); !IsProtocolError(err, ProtocolTerminalConflict) {
		t.Fatalf("late frame error = %v", err)
	}
	other := attachWelcomedDevice(t, broker, "tenant-b", "device_test_002", "conn_test_002", now)
	if err := receiveDeviceEnvelope(other, now, "msg_cross_001", MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 3, Status: TerminalFailed}); !IsProtocolError(err, ProtocolDeviceMismatch) {
		t.Fatalf("cross-scope error = %v", err)
	}
}

func TestDurableBrokerCancelBeforeAcceptanceStillPublishesAdmissionAndTerminal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	_ = receiveOutbound(t, link)
	if err := broker.Cancel(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID, "operator cancel"); err != nil {
		t.Fatal(err)
	}
	if cancel := receiveOutbound(t, link); cancel.Type != MessageWorkOrderCancel {
		t.Fatalf("cancel = %#v", cancel)
	}
	sendDeviceEnvelope(t, link, now, "msg_accept_cancel_race_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now})
	if event := receiveBrokerEvent(t, subscription); event.Kind != BrokerAccepted {
		t.Fatalf("accepted after cancel = %#v", event)
	}
	stored, found, err := repository.GetWorkOrder(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID)
	if err != nil || !found || stored.State != WorkOrderCancelling {
		t.Fatalf("cancelling record = %#v, found = %v, error = %v", stored, found, err)
	}
	sendDeviceEnvelope(t, link, now, "msg_terminal_cancel_race_001", MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 1, Status: TerminalCancelled, ReasonCode: TerminalCancelledByCloud})
	if event := receiveBrokerEvent(t, subscription); event.Kind != BrokerTerminal || event.Terminal.Status != TerminalCancelled {
		t.Fatalf("cancel race terminal = %#v", event)
	}
}

func TestDurableBrokerRejectsArtifactBeforeAcceptance(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, _ := NewSQLRepository(ctx, testkit.NewTestDB(t))
	broker, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	defer broker.Close()
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	_ = receiveOutbound(t, link)
	manifest := ArtifactManifestPayload{WorkOrderID: record.Order.WorkOrderID, ArtifactID: "artifact_test_001", Class: ArtifactSummary, DisplayName: "summary.txt", MediaType: "text/plain", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := receiveDeviceEnvelope(link, now, "msg_artifact_early_001", MessageArtifactManifest, manifest); !IsProtocolError(err, ProtocolSequenceGap) {
		t.Fatalf("early artifact error = %v", err)
	}
}

func TestDurableBrokerRemoteExecutionKillSwitchStopsNewAndPendingOffers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	first, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }, RemoteExecution: func(string, string) bool { return enabled }})
	request := OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)}
	_, subscription, err := first.Offer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	subscription.Close()
	first.Close()

	enabled = false
	broker, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }, RemoteExecution: func(string, string) bool { return enabled }})
	defer broker.Close()
	link, err := broker.AttachDevice("tenant-a", "device_test_001", "conn_test_001")
	if err != nil {
		t.Fatal(err)
	}
	hello := DeviceHelloPayload{DeviceID: "device_test_001", ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, ClientVersion: "0.1.0", AionCoreVersion: "0.1.52", CapabilityDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActiveWorkOrders: []ActiveWorkOrder{}}
	sendDeviceEnvelope(t, link, now, "msg_hello_disabled_001", MessageDeviceHello, hello)
	welcome := receiveOutbound(t, link)
	if welcome.Type != MessageDeviceWelcome || welcome.Payload.(DeviceWelcomePayload).RemoteExecutionEnabled == nil || *welcome.Payload.(DeviceWelcomePayload).RemoteExecutionEnabled {
		t.Fatalf("disabled welcome = %#v", welcome)
	}
	select {
	case outbound := <-link.Outbound():
		t.Fatalf("disabled link replayed pending offer: %#v", outbound)
	case <-time.After(100 * time.Millisecond):
	}
	if _, subscription, err := broker.Offer(ctx, request); subscription != nil || !errors.Is(err, ErrRemoteExecutionDisabled) {
		t.Fatalf("disabled offer = subscription %T error %v", subscription, err)
	}
}

func TestDeviceLinkLeaseGenerationFencesStaleExpiryAndClosesCurrentLink(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, _ := NewSQLRepository(ctx, testkit.NewTestDB(t))
	broker, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	defer broker.Close()
	link, err := broker.AttachDevice("tenant-a", "device_test_001", "conn_test_001")
	if err != nil {
		t.Fatal(err)
	}
	broker.mu.Lock()
	staleGeneration := link.leaseGeneration
	link.armLeaseLocked()
	currentGeneration := link.leaseGeneration
	broker.mu.Unlock()
	link.expireLease(staleGeneration)
	broker.mu.Lock()
	current := broker.links[deviceKey("tenant-a", "device_test_001")]
	broker.mu.Unlock()
	if current != link {
		t.Fatal("stale lease generation closed current device link")
	}
	link.expireLease(currentGeneration)
	broker.mu.Lock()
	current = broker.links[deviceKey("tenant-a", "device_test_001")]
	broker.mu.Unlock()
	if current != nil {
		t.Fatal("current lease expiry left device link attached")
	}
	if _, ok := <-link.Outbound(); ok {
		t.Fatal("expired device link outbound channel remained open")
	}
}

func TestDurableBrokerDuplicateOfferReplaysStoredMessageAndRejectsConflicts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	request := OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)}
	_, firstSubscription, err := broker.Offer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer firstSubscription.Close()
	first := receiveOutbound(t, link)
	_, duplicateSubscription, err := broker.Offer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer duplicateSubscription.Close()
	duplicate := receiveOutbound(t, link)
	if duplicate.Type != MessageWorkOrderOffer || duplicate.MessageID != first.MessageID {
		t.Fatalf("duplicate offer = %#v, want stored message ID %s", duplicate, first.MessageID)
	}

	changed := request
	changed.Order.Task.Instruction = "Different content"
	if _, subscription, err := broker.Offer(ctx, changed); subscription != nil || !IsProtocolError(err, ProtocolDuplicateConflict) {
		t.Fatalf("changed work order = subscription %T error %v", subscription, err)
	}
	otherSession := request
	otherSession.SessionID = "session-b"
	if _, subscription, err := broker.Offer(ctx, otherSession); subscription != nil || !IsProtocolError(err, ProtocolDuplicateConflict) {
		t.Fatalf("cross-session work order = subscription %T error %v", subscription, err)
	}
	otherID := request
	otherID.Order.WorkOrderID = "wo_test_002"
	if _, subscription, err := broker.Offer(ctx, otherID); subscription != nil || !IsProtocolError(err, ProtocolDuplicateConflict) {
		t.Fatalf("reused idempotency key = subscription %T error %v", subscription, err)
	}
}

func TestDurableBrokerDeduplicatesInboundReplayAcrossConnectionEpochs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	firstLink := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	_ = receiveOutbound(t, firstLink)
	accepted := WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now}
	sendDeviceEnvelope(t, firstLink, now, "msg_accept_replay_001", MessageWorkOrderAccepted, accepted)
	_ = receiveBrokerEvent(t, subscription)
	firstLink.Close()

	secondLink := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_002", now.Add(time.Second))
	if err := receiveDeviceEnvelope(secondLink, now.Add(time.Second), "msg_accept_replay_001", MessageWorkOrderAccepted, accepted); err != nil {
		t.Fatalf("cross-epoch replay: %v", err)
	}
	select {
	case event := <-subscription.Events():
		t.Fatalf("cross-epoch replay published duplicate event: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
	changed := accepted
	changed.AcceptedAt = now.Add(time.Second)
	if err := receiveDeviceEnvelope(secondLink, now.Add(time.Second), "msg_accept_replay_001", MessageWorkOrderAccepted, changed); !IsProtocolError(err, ProtocolDuplicateConflict) {
		t.Fatalf("cross-epoch changed replay error = %v", err)
	}
}

type listOutboundFailureRepository struct {
	Repository
}

func (listOutboundFailureRepository) ListOutbound(context.Context, string, string) ([]queuedOutbound, error) {
	return nil, errors.New("list outbound failed")
}

func TestDurableBrokerDuplicateOfferCleansSubscriptionWhenReplayLookupFails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	request := OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)}
	seed, err := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_, subscription, err := seed.Offer(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	subscription.Close()

	broker, err := NewDurableBroker(BrokerOptions{Repository: listOutboundFailureRepository{Repository: repository}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if _, subscription, err := broker.Offer(ctx, request); subscription != nil || err == nil {
		t.Fatalf("duplicate offer = subscription %T error %v", subscription, err)
	}
	if len(broker.subscribers) != 0 {
		t.Fatalf("leaked broker subscriptions: %#v", broker.subscribers)
	}
}

func TestDurableBrokerRejectsSequenceGapMessageConflictAndTerminalConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, _ := NewSQLRepository(ctx, testkit.NewTestDB(t))
	broker, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	defer broker.Close()
	link := attachWelcomedDevice(t, broker, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := broker.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	_ = receiveOutbound(t, link)
	sendDeviceEnvelope(t, link, now, "msg_accept_001", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now})
	_ = receiveBrokerEvent(t, subscription)
	gap := WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 2, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: "gap"}}
	if err := receiveDeviceEnvelope(link, now, "msg_gap_001", MessageWorkOrderFrame, gap); !IsProtocolError(err, ProtocolSequenceGap) {
		t.Fatalf("gap error = %v", err)
	}
	first := WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 1, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: "first"}}
	sendDeviceEnvelope(t, link, now, "msg_frame_001", MessageWorkOrderFrame, first)
	_ = receiveBrokerEvent(t, subscription)
	conflict := WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 2, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: "changed"}}
	if err := receiveDeviceEnvelope(link, now, "msg_frame_001", MessageWorkOrderFrame, conflict); !IsProtocolError(err, ProtocolDuplicateConflict) {
		t.Fatalf("message conflict error = %v", err)
	}
	sendDeviceEnvelope(t, link, now, "msg_terminal_001", MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 2, Status: TerminalSucceeded})
	_ = receiveBrokerEvent(t, subscription)
	if err := receiveDeviceEnvelope(link, now, "msg_terminal_002", MessageWorkOrderTerminal, WorkOrderTerminalPayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 3, Status: TerminalFailed}); !IsProtocolError(err, ProtocolTerminalConflict) {
		t.Fatalf("terminal conflict error = %v", err)
	}
}

func TestDurableBrokerRestartReplaysOfferAndWorkOrderCursor(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	repository, err := NewSQLRepository(ctx, testkit.NewTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now }})
	link := attachWelcomedDevice(t, first, "tenant-a", "device_test_001", "conn_test_001", now)
	record, subscription, err := first.Offer(ctx, OfferRequest{TenantID: "tenant-a", DeviceID: "device_test_001", SessionID: "session-a", Order: testWorkOrder(now)})
	if err != nil {
		t.Fatal(err)
	}
	_ = receiveOutbound(t, link)
	subscription.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now.Add(time.Second) }})
	defer second.Close()
	reconnected := attachWelcomedDevice(t, second, "tenant-a", "device_test_001", "conn_test_002", now.Add(time.Second))
	replayed := receiveOutbound(t, reconnected)
	if replayed.Type != MessageWorkOrderOffer || replayed.Payload.(WorkOrder).WorkOrderID != record.Order.WorkOrderID {
		t.Fatalf("replayed offer = %#v", replayed)
	}
	recovered, events, err := second.Subscribe(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID, 0)
	if err != nil || recovered.State != WorkOrderOffered {
		t.Fatalf("recovered = %#v/%v", recovered, err)
	}
	defer events.Close()
	sendDeviceEnvelope(t, reconnected, now.Add(time.Second), "msg_accept_002", MessageWorkOrderAccepted, WorkOrderAcceptedPayload{WorkOrderID: record.Order.WorkOrderID, AcceptedAt: now.Add(time.Second)})
	if event := receiveBrokerEvent(t, events); event.Cursor != 1 || event.Kind != BrokerAccepted {
		t.Fatalf("recovered event = %#v", event)
	}
	sendDeviceEnvelope(t, reconnected, now.Add(time.Second), "msg_frame_002", MessageWorkOrderFrame, WorkOrderFramePayload{WorkOrderID: record.Order.WorkOrderID, Sequence: 1, Frame: RuntimeFrame{Kind: "text", Channel: "output", Content: "recovered"}})
	if event := receiveBrokerEvent(t, events); event.Cursor != 2 || event.Frame.Sequence != 1 {
		t.Fatalf("cursor replay event = %#v", event)
	}

	third, _ := NewDurableBroker(BrokerOptions{Repository: repository, Now: func() time.Time { return now.Add(2 * time.Second) }})
	defer third.Close()
	_, replay, err := third.Subscribe(ctx, "tenant-a", "device_test_001", record.Order.WorkOrderID, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Close()
	if event := receiveBrokerEvent(t, replay); event.Cursor != 2 || event.Frame.Frame.Content != "recovered" {
		t.Fatalf("durable cursor replay = %#v", event)
	}
}

func testWorkOrder(now time.Time) WorkOrder {
	return WorkOrder{WorkOrderID: "wo_test_001", IdempotencyKey: "idem-test-001", ProjectID: "project_test_001", OriginActorID: "actor_test_001", Task: WorkOrderTask{Kind: "agent_turn", Instruction: "Implement the change"}, WorkspaceGrantID: "grant_test_001", RequiredCapabilities: []Capability{CapabilityWorkspaceRead, CapabilityWorkspaceWrite}, RequestedArtifactClasses: []ArtifactClass{ArtifactPatch}, Deadline: now.Add(30 * time.Minute)}
}

func attachWelcomedDevice(t *testing.T, broker *DurableBroker, tenantID, deviceID, epoch string, now time.Time) *DeviceLink {
	t.Helper()
	link, err := broker.AttachDevice(tenantID, deviceID, epoch)
	if err != nil {
		t.Fatal(err)
	}
	hello := DeviceHelloPayload{DeviceID: deviceID, ProtocolMin: ProtocolVersion, ProtocolMax: ProtocolVersion, ClientVersion: "0.1.0", AionCoreVersion: "0.1.52", CapabilityDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ActiveWorkOrders: []ActiveWorkOrder{}}
	sendDeviceEnvelope(t, link, now, "msg_hello_"+epoch, MessageDeviceHello, hello)
	if welcome := receiveOutbound(t, link); welcome.Type != MessageDeviceWelcome {
		t.Fatalf("welcome = %#v", welcome)
	}
	return link
}

func sendDeviceEnvelope(t *testing.T, link *DeviceLink, now time.Time, messageID string, kind MessageType, payload any) {
	t.Helper()
	if err := receiveDeviceEnvelope(link, now, messageID, kind, payload); err != nil {
		t.Fatal(err)
	}
}

func receiveDeviceEnvelope(link *DeviceLink, now time.Time, messageID string, kind MessageType, payload any) error {
	data, err := MarshalEnvelope(Envelope{ProtocolVersion: ProtocolVersion, MessageID: messageID, ConnectionEpoch: link.epoch, Type: kind, SentAt: now, Payload: payload}, DeviceToCloud, now)
	if err != nil {
		return err
	}
	return link.Receive(context.Background(), data)
}

func receiveOutbound(t *testing.T, link *DeviceLink) Envelope {
	t.Helper()
	select {
	case envelope, ok := <-link.Outbound():
		if !ok {
			t.Fatal("device link closed")
		}
		return envelope
	case <-time.After(3 * time.Second):
		t.Fatal("device outbound timeout")
	}
	return Envelope{}
}

func receiveBrokerEvent(t *testing.T, subscription WorkOrderSubscription) BrokerEvent {
	t.Helper()
	select {
	case event, ok := <-subscription.Events():
		if !ok {
			t.Fatal("broker subscription closed")
		}
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("broker event timeout")
	}
	return BrokerEvent{}
}
