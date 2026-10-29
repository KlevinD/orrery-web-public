package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	agentcore "orrery_v2/internal/agent/core"
	deviceprovider "orrery_v2/internal/agent/runtime/providers/device"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/device/delivery"
	devicedirectory "orrery_v2/internal/device/directory"
	deviceidentity "orrery_v2/internal/device/identity"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const deviceBrowserSessionCookie = "orrery_access_token"

func newProductionDeviceDeliveryHandler(
	ctx context.Context,
	rt *platform.Runtime,
	auth *userauth.Service,
	audit *operations.AuditStore,
	broker *deviceprovider.DurableBroker,
	agentDirectory delivery.AgentDirectory,
	next http.Handler,
) (http.Handler, error) {
	if rt == nil || rt.Databases == nil || auth == nil || audit == nil || broker == nil || agentDirectory == nil || next == nil {
		return nil, fmt.Errorf("production device delivery dependencies are incomplete")
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return nil, err
	}
	store, err := deviceidentity.NewSQLStore(ctx, core)
	if err != nil {
		return nil, err
	}
	identityService, err := deviceidentity.NewService(deviceidentity.Options{Store: store, Now: rt.Clock.Now})
	if err != nil {
		return nil, err
	}
	publicOrigin := rt.Config.DeviceDelivery.PublicOrigin
	defaultTenantID := rt.Config.DeviceDelivery.DefaultTenantID
	if strings.TrimSpace(defaultTenantID) == "" {
		defaultTenantID = "global"
	}
	runtimeStore, err := rt.Databases.Store(db.RoleRuntime)
	if err != nil {
		return nil, err
	}
	artifactUploads, err := newProductionDeviceArtifactUploads(runtimeStore, rt.FileStore, rt.Clock.Now, rt.Config.DeviceDelivery.MaxArtifactBytes)
	if err != nil {
		return nil, err
	}
	production := strings.EqualFold(strings.TrimSpace(rt.Config.Environment), "production") || strings.EqualFold(strings.TrimSpace(rt.Config.Environment), "prod")
	return delivery.NewHandler(delivery.Options{
		Identity: identityService,
		AuthenticateBrowser: func(ctx context.Context, request *http.Request) (deviceidentity.BrowserPrincipal, error) {
			cookie, err := request.Cookie(deviceBrowserSessionCookie)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			principal, err := auth.AuthenticatePrincipal(ctx, cookie.Value)
			if err != nil {
				return deviceidentity.BrowserPrincipal{}, deviceidentity.ErrUnauthorized
			}
			return deviceidentity.BrowserPrincipal{
				UserID:      firstNonBlankDevice(principal.UserID, principal.Username, principal.AgentProfileID),
				DisplayName: firstNonBlankDevice(principal.DisplayName, principal.Username),
				TenantID:    principal.TenantID,
			}, nil
		},
		AuthorizeTenant: func(_ context.Context, principal deviceidentity.BrowserPrincipal, tenantID string) error {
			tenantID = strings.TrimSpace(tenantID)
			principalTenant := strings.TrimSpace(principal.TenantID)
			if tenantID == "" || (principalTenant != "" && principalTenant != tenantID) || (principalTenant == "" && tenantID != strings.TrimSpace(defaultTenantID)) {
				return deviceidentity.ErrUnauthorized
			}
			return nil
		},
		Audit:              productionDeviceAuditRecorder{store: audit},
		Sessions:           productionDeviceSessionRunner{broker: broker, now: rt.Clock.Now},
		AgentDirectory:     agentDirectory,
		Artifacts:          artifactUploads,
		Next:               next,
		DefaultTenantID:    defaultTenantID,
		PublicOrigin:       publicOrigin,
		AllowRequestOrigin: !production,
		Now:                rt.Clock.Now,
	})
}

func newProductionPersonalAssistantService(ctx context.Context, rt *platform.Runtime, broker *deviceprovider.DurableBroker, profiles *agentcore.PlatformProfileRepository, memory defaultMemoryProfileStore, knowledge defaultKnowledgeEnsurer, knowledgeBases personalAssistantKnowledgeBaseLookup, knowledgeRoot string) (*productionPersonalAssistantService, error) {
	if rt == nil || rt.Databases == nil || broker == nil || profiles == nil {
		return nil, fmt.Errorf("production personal assistant dependencies are incomplete")
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return nil, err
	}
	store, err := devicedirectory.NewSQLStore(ctx, core)
	if err != nil {
		return nil, err
	}
	defaultTenantID := strings.TrimSpace(rt.Config.DeviceDelivery.DefaultTenantID)
	if defaultTenantID == "" {
		defaultTenantID = "global"
	}
	directory, err := devicedirectory.NewService(devicedirectory.Options{
		Store: store, Now: rt.Clock.Now, DeviceConnected: broker.DeviceConnected, DefaultTenantID: defaultTenantID,
	})
	if err != nil {
		return nil, err
	}
	service := &productionPersonalAssistantService{
		directory: directory, profiles: profiles, memory: memory, knowledge: knowledge, knowledgeBases: knowledgeBases, knowledgeRoot: strings.TrimSpace(knowledgeRoot), config: rt.Config, defaultTenantID: defaultTenantID, now: rt.Clock.Now,
	}
	if err := service.reconcileExistingProfiles(ctx); err != nil {
		return nil, fmt.Errorf("reconcile personal assistant resources: %w", err)
	}
	return service, nil
}

type productionDeviceAuditRecorder struct {
	store *operations.AuditStore
}

func (recorder productionDeviceAuditRecorder) RecordDeviceAudit(ctx context.Context, event delivery.AuditEvent) error {
	if recorder.store == nil {
		return fmt.Errorf("device audit store is unavailable")
	}
	entityID := strings.TrimSpace(event.DeviceID)
	if entityID == "" {
		entityID = "pairing"
	}
	return recorder.store.RecordAuditEvent(ctx, operations.AuditEventDTO{
		TenantID: event.TenantID, ActorID: event.ActorID, EntityType: "device", EntityID: entityID,
		Action: event.Action, Result: event.Result, Severity: "info", OccurredAt: event.OccurredAt,
	})
}

type productionDeviceSessionRunner struct {
	broker *deviceprovider.DurableBroker
	now    func() time.Time
}

func (runner productionDeviceSessionRunner) RevokeDeviceConnection(deviceID string) {
	if runner.broker != nil {
		runner.broker.RevokeDeviceConnection(deviceID)
	}
}

func (runner productionDeviceSessionRunner) RunDeviceSession(ctx context.Context, connection *websocket.Conn, identity deviceidentity.ConnectionIdentity, epoch string) error {
	if runner.broker == nil || connection == nil {
		return errors.New("device session dependencies are unavailable")
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	link, err := runner.broker.AttachOwnedDevice(identity.TenantID, identity.DeviceID, identity.OwnerID, epoch)
	if err != nil {
		return err
	}
	defer link.Close()
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- runner.writeDeviceFrames(connection, link)
		_ = connection.Close()
	}()
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			select {
			case writerErr := <-writerDone:
				if writerErr != nil && !errors.Is(writerErr, context.Canceled) {
					return writerErr
				}
			default:
			}
			return err
		}
		if messageType != websocket.TextMessage {
			_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4400, "text frames required"), time.Now().Add(time.Second))
			return errors.New("device sent a non-text protocol frame")
		}
		if err := link.Receive(ctx, payload); err != nil {
			_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4400, "invalid device protocol frame"), time.Now().Add(time.Second))
			return err
		}
	}
}

func (runner productionDeviceSessionRunner) writeDeviceFrames(connection *websocket.Conn, link *deviceprovider.DeviceLink) error {
	for envelope := range link.Outbound() {
		payload, err := deviceprovider.MarshalEnvelope(envelope, deviceprovider.CloudToDevice, runner.now().UTC())
		if err != nil {
			return err
		}
		if err := connection.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return err
		}
		if err := connection.WriteMessage(websocket.TextMessage, payload); err != nil {
			return err
		}
	}
	return nil
}

func firstNonBlankDevice(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
