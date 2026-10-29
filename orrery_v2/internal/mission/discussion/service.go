package discussion

import (
	"context"
	"time"

	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/uow"
)

type DiscussionService interface {
	CreateGroup(ctx context.Context, missionID missioncore.MissionID, goal string) (DiscussionGroup, error)
	AddMember(ctx context.Context, groupID DiscussionGroupID, member DiscussionMember) error
	ListMembers(ctx context.Context, groupID DiscussionGroupID) ([]DiscussionMember, error)
	PostMessage(ctx context.Context, cmd PostMessageCommand) (Message, error)
	Reply(ctx context.Context, cmd ReplyCommand) (Message, error)
	BuildDigest(ctx context.Context, groupID DiscussionGroupID, viewer ViewerRef) (DiscussionDigest, error)
}

type InboxService interface {
	ListInbox(ctx context.Context, recipient ViewerRef, q InboxQuery) (InboxPage, error)
	MarkRead(ctx context.Context, itemID InboxItemID, viewer ViewerRef) error
	CloseWithReply(ctx context.Context, itemID InboxItemID, viewer ViewerRef, reply MessageID) (InboxItem, error)
	Expire(ctx context.Context, now time.Time) error
}

type MentionDispatcher interface {
	DispatchMention(ctx context.Context, item InboxItem) error
}

type PostMessageCommand struct {
	TenantID          string
	MissionID         missioncore.MissionID
	DiscussionGroupID DiscussionGroupID
	ReplyToMessageID  MessageID
	Author            ViewerRef
	Content           string
	Summary           string
	MessageKind       MessageKind
	Visibility        MessageVisibility
	RelatedTaskID     missioncore.TaskID
	RelatedEvidenceID string
	SourceRefs        []string
	RequiredResponse  bool
	DueAt             time.Time
	IdempotencyKey    string
}

type ReplyCommand struct {
	TenantID          string
	MissionID         missioncore.MissionID
	DiscussionGroupID DiscussionGroupID
	ReplyToMessageID  MessageID
	Author            ViewerRef
	Content           string
	Summary           string
	InboxItemID       InboxItemID
	IdempotencyKey    string
}

type ServiceOptions struct {
	UnitOfWork  *uow.UnitOfWork
	Repository  Repository
	EventSink   *EventSink
	Dispatcher  MentionDispatcher
	Parser      MentionParser
	Policy      MessagePolicy
	Clock       clock.Clock
	IDGenerator idgen.ErrorGenerator
	Logger      logging.Logger
}

type Service struct {
	unit       *uow.UnitOfWork
	repo       Repository
	events     *EventSink
	dispatcher MentionDispatcher
	parser     MentionParser
	policy     MessagePolicy
	clock      clock.Clock
	ids        idgen.ErrorGenerator
	logger     logging.Logger
}

func NewService(opts ServiceOptions) *Service {
	repo := opts.Repository
	if repo == nil {
		repo = NewPlatformRepository()
	}
	events := opts.EventSink
	if events == nil {
		events = NewEventSink()
	}
	return &Service{
		unit:       opts.UnitOfWork,
		repo:       repo,
		events:     events,
		dispatcher: opts.Dispatcher,
		parser:     opts.Parser,
		policy:     opts.Policy,
		clock:      opts.Clock,
		ids:        opts.IDGenerator,
		logger:     opts.Logger,
	}
}
