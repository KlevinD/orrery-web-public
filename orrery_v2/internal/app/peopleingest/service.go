package peopleingest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const defaultLeaseDuration = time.Minute

type EventConsumer interface {
	ConsumePeopleEvent(context.Context, DeliveryEvent) error
}

type Service struct {
	store         *Store
	consumer      EventConsumer
	now           func() time.Time
	leaseDuration time.Duration
	newLeaseToken func() (string, error)
}

type ServiceOptions struct {
	Store         *Store
	Consumer      EventConsumer
	Now           func() time.Time
	LeaseDuration time.Duration
	NewLeaseToken func() (string, error)
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || options.Consumer == nil {
		return nil, errors.New("People ingestion store and event consumer are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.NewLeaseToken == nil {
		options.NewLeaseToken = randomLeaseToken
	}
	return &Service{
		store: options.Store, consumer: options.Consumer, now: options.Now,
		leaseDuration: options.LeaseDuration, newLeaseToken: options.NewLeaseToken,
	}, nil
}

func (service *Service) Ingest(
	ctx context.Context,
	batch DeliveryBatch,
	requestSHA256 string,
) (ack DeliveryAcknowledgement, returnedErr error) {
	if service == nil || service.store == nil || service.consumer == nil {
		return DeliveryAcknowledgement{}, errors.New("People ingestion service is unavailable")
	}
	if err := batch.Validate(); err != nil {
		return DeliveryAcknowledgement{}, err
	}
	if len(requestSHA256) != sha256.Size*2 {
		return DeliveryAcknowledgement{}, invalidBatch("request SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(requestSHA256); err != nil {
		return DeliveryAcknowledgement{}, invalidBatch("request SHA-256 is invalid")
	}
	leaseToken, err := service.newLeaseToken()
	if err != nil || leaseToken == "" {
		return DeliveryAcknowledgement{}, errors.New("create People ingestion lease token")
	}
	claim, err := service.store.ClaimBatch(ctx, batch, requestSHA256, leaseToken, service.now(), service.leaseDuration)
	if err != nil {
		return DeliveryAcknowledgement{}, err
	}
	if claim.Completed {
		return acknowledgementFor(batch), nil
	}
	defer func() {
		if returnedErr != nil {
			_ = service.store.ReleaseBatch(context.WithoutCancel(ctx), claim, returnedErr, service.now())
		}
	}()

	eventJSON := make([][]byte, len(batch.Events))
	eventHashes := make([]string, len(batch.Events))
	for index, event := range batch.Events {
		encoded, err := json.Marshal(event)
		if err != nil {
			return DeliveryAcknowledgement{}, fmt.Errorf("encode People event %q: %w", event.EventID, err)
		}
		digest := sha256.Sum256(encoded)
		eventJSON[index] = encoded
		eventHashes[index] = hex.EncodeToString(digest[:])
	}
	for index := claim.RecordedCount; index < len(batch.Events); index++ {
		if err := service.store.RecordEvent(
			ctx, claim, index, batch.Events[index], eventJSON[index], eventHashes[index], service.now(), service.leaseDuration,
		); err != nil {
			return DeliveryAcknowledgement{}, err
		}
	}
	for index := claim.ProjectedCount; index < len(batch.Events); index++ {
		event := batch.Events[index]
		projected, err := service.store.EventProjected(ctx, event.EventID, event.TenantID, eventHashes[index])
		if err != nil {
			return DeliveryAcknowledgement{}, err
		}
		if !projected {
			if err := service.consumer.ConsumePeopleEvent(ctx, event); err != nil {
				return DeliveryAcknowledgement{}, fmt.Errorf("consume People event %q: %w", event.EventID, err)
			}
		}
		if err := service.store.MarkProjected(ctx, claim, index, event.TenantID, event.EventID, service.now(), service.leaseDuration); err != nil {
			return DeliveryAcknowledgement{}, err
		}
	}
	if err := service.store.CompleteBatch(ctx, claim, len(batch.Events), service.now()); err != nil {
		return DeliveryAcknowledgement{}, err
	}
	return acknowledgementFor(batch), nil
}

func acknowledgementFor(batch DeliveryBatch) DeliveryAcknowledgement {
	lag := int64(0)
	ids := make([]string, len(batch.Events))
	for index, event := range batch.Events {
		ids[index] = event.EventID
	}
	return DeliveryAcknowledgement{
		Stream: batch.Stream, TenantID: batch.TenantID, BatchID: batch.BatchID, LastCursor: batch.LastCursor,
		AcceptedEventIDs: ids, EventStoreAckCursor: batch.LastCursor, ProjectionCursor: batch.LastCursor,
		ProjectionLagSeconds: &lag,
	}
}

func randomLeaseToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
