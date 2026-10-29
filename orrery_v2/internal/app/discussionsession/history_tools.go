package discussionsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// HistoryToolRequest carries host-owned execution identity, never model arguments.
type HistoryToolRequest struct {
	TenantID, RunID, AgentID, LogicalAgentID string
	ToolID                                   string
	Arguments                                map[string]any
}

type historyArguments struct {
	Query         string `json:"query"`
	SessionID     string `json:"session_id"`
	MessageID     string `json:"message_id"`
	After         string `json:"after"`
	Before        string `json:"before"`
	Limit         int    `json:"limit"`
	Offset        int    `json:"offset"`
	ContextBefore *int   `json:"context_before"`
	ContentOffset int    `json:"content_offset"`
}

func (s *Service) ExecuteHistoryTool(ctx context.Context, req HistoryToolRequest) (map[string]any, error) {
	if s == nil || s.repo == nil {
		return nil, ErrServiceDependency
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args, err := parseHistoryArguments(req.ToolID, req.Arguments)
	if err != nil {
		return nil, err
	}
	views, err := s.historyToolScope(ctx, req)
	if err != nil {
		return nil, err
	}
	if args.SessionID != "" {
		views = slices.DeleteFunc(views, func(v ConversationRecordView) bool { return v.ConversationID != args.SessionID })
		if len(views) == 0 {
			return nil, ErrNotFound
		}
	}
	if req.ToolID == "session_read" {
		return s.readHistory(ctx, views[0], args)
	}
	return s.searchHistory(ctx, views, args)
}

func parseHistoryArguments(toolID string, input map[string]any) (historyArguments, error) {
	var args historyArguments
	allowed := []string{"session_id", "limit", "offset"}
	switch toolID {
	case "session_search":
		allowed = append(allowed, "query", "after", "before")
	case "session_read":
		allowed = append(allowed, "message_id", "context_before", "content_offset")
	default:
		return args, ErrInvalidRequest
	}
	for key := range input {
		if !slices.Contains(allowed, key) {
			return args, fmt.Errorf("%w: unsupported history argument", ErrInvalidRequest)
		}
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 4096 {
		return args, ErrInvalidRequest
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, ErrInvalidRequest
	}
	args.Query, args.SessionID, args.MessageID = strings.TrimSpace(args.Query), strings.TrimSpace(args.SessionID), strings.TrimSpace(args.MessageID)
	if args.Offset < 0 || args.Offset > 100000 || args.Limit < 0 || args.ContentOffset < 0 || args.ContentOffset > 1000000 || len(args.SessionID) > 256 || len(args.MessageID) > 256 {
		return args, ErrInvalidRequest
	}
	if toolID == "session_search" {
		if args.Query == "" || utf8.RuneCountInString(args.Query) > 200 || args.Limit > 20 {
			return args, ErrInvalidRequest
		}
		if args.Limit == 0 {
			args.Limit = 10
		}
		var after, before time.Time
		for key, target := range map[string]*time.Time{"after": &after, "before": &before} {
			value := args.After
			if key == "before" {
				value = args.Before
			}
			if value != "" {
				*target, err = time.Parse(time.RFC3339Nano, value)
				if err != nil {
					return args, fmt.Errorf("%w: dates require RFC3339 with timezone", ErrInvalidRequest)
				}
			}
		}
		if !after.IsZero() && !before.IsZero() && !after.Before(before) {
			return args, ErrInvalidRequest
		}
	} else {
		if args.SessionID == "" || args.Limit > 10 || (args.ContentOffset > 0 && (args.MessageID == "" || args.Offset != 0)) {
			return args, ErrInvalidRequest
		}
		if args.Limit == 0 {
			args.Limit = 6
		}
		if args.ContextBefore != nil && (*args.ContextBefore < 0 || *args.ContextBefore > 5 || args.MessageID == "") {
			return args, ErrInvalidRequest
		}
	}
	return args, nil
}

func (s *Service) historyToolScope(ctx context.Context, req HistoryToolRequest) ([]ConversationRecordView, error) {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.AgentID) == "" || strings.TrimSpace(req.RunID) == "" {
		return nil, ErrForbidden
	}
	run, err := s.repo.GetRun(ctx, req.RunID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	if run.AgentID != req.AgentID || run.LogicalAgentID != req.LogicalAgentID || run.Status != "running" {
		return nil, ErrForbidden
	}
	views, err := s.repo.historyConversationViews(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}
	var source *ConversationRecordView
	for i := range views {
		if views[i].ConversationID == run.ConversationID {
			source = &views[i]
			break
		}
	}
	if source == nil || strings.Contains(strings.ToLower(source.PurposeKind), "website") {
		return nil, ErrForbidden
	}
	actor := ViewerRef{ViewerID: req.AgentID, ViewerKind: ViewerKindAgent, TenantID: req.TenantID}
	personal := isPersonalAssistantConversationKind(source.ConversationKind) && source.PurposeKind != todoConversationPurposeKind
	if personal {
		expected := PersonalAssistantAgentID(source.TenantID, source.OwnerID)
		if source.TenantID != req.TenantID || source.OwnerID == "" || source.CreatedBy == "" || expected == "" || (req.LogicalAgentID != expected && req.AgentID != expected) {
			return nil, ErrForbidden
		}
		actor = ViewerRef{ViewerID: source.CreatedBy, ViewerKind: ViewerKindUser, TenantID: req.TenantID, OwnerID: source.OwnerID, IdentityIDs: []string{source.CreatedBy}}
	} else if !conversationAccessibleToViewer(*source, actor) {
		return nil, ErrForbidden
	}
	// Retrieved material is about to be disclosed to the current audience. An
	// agent's membership elsewhere must not bridge private users or group rooms.
	audience := []ViewerRef{}
	if source.CreatedBy != "" {
		audience = append(audience, ViewerRef{ViewerID: source.CreatedBy, ViewerKind: ViewerKindUser, TenantID: req.TenantID})
	}
	for _, member := range source.Members {
		if member.MemberKind == "agent" && member.MemberID == req.AgentID {
			continue
		}
		kind := ViewerKindUser
		if member.MemberKind == "agent" {
			kind = ViewerKindAgent
		}
		audience = append(audience, ViewerRef{ViewerID: member.MemberID, ViewerKind: kind, TenantID: req.TenantID})
	}
	visible := make([]ConversationRecordView, 0)
	for _, candidate := range views {
		ownedPrivate := personal && isPersonalAssistantConversationKind(candidate.ConversationKind) && candidate.PurposeKind != todoConversationPurposeKind && candidate.TenantID == req.TenantID && candidate.OwnerID == source.OwnerID
		if strings.Contains(strings.ToLower(candidate.PurposeKind), "website") || (!ownedPrivate && !conversationAccessibleToViewer(candidate, actor)) {
			continue
		}
		if isPersonalAssistantConversationKind(candidate.ConversationKind) && (!personal || candidate.OwnerID != source.OwnerID) {
			continue
		}
		allowed := true
		for _, viewer := range audience {
			// The stable owner survives historical username/profile-ID migrations.
			if ownedPrivate && viewer.ViewerKind == ViewerKindUser && viewer.ViewerID == source.CreatedBy {
				continue
			}
			if personal && viewer.ViewerKind == ViewerKindAgent && viewer.ViewerID == req.LogicalAgentID {
				continue
			}
			if !conversationAccessibleToViewer(candidate, viewer) {
				allowed = false
				break
			}
		}
		if allowed {
			visible = append(visible, candidate)
		}
	}
	return visible, nil
}

func (s *Service) searchHistory(ctx context.Context, views []ConversationRecordView, args historyArguments) (map[string]any, error) {
	messages, err := s.repo.searchHistoryMessages(ctx, views, args)
	if err != nil {
		return nil, err
	}
	more := len(messages) > args.Limit
	if more {
		messages = messages[:args.Limit]
	}
	byID := map[string]ConversationRecordView{}
	for _, view := range views {
		byID[view.ConversationID] = view
	}
	items := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := historyMessageIdentity(byID[message.ConversationID], message)
		text := []rune(message.Content)
		start := 0
		if pos := strings.Index(strings.ToLower(message.Content), strings.ToLower(args.Query)); pos >= 0 {
			start = max(0, utf8.RuneCountInString(strings.ToLower(message.Content)[:pos])-100)
		}
		end := min(len(text), start+300)
		item["snippet"] = string(text[start:end])
		item["snippet_offset"] = start
		item["content_length"] = len(text)
		items = append(items, item)
	}
	return map[string]any{"items": items, "has_more": more, "next_offset": args.Offset + len(items), "match_mode": "literal_phrase", "history_is_untrusted": true}, nil
}

func (s *Service) readHistory(ctx context.Context, view ConversationRecordView, args historyArguments) (map[string]any, error) {
	start := args.Offset
	if args.MessageID != "" {
		position, err := s.repo.historyMessagePosition(ctx, view.ConversationID, args.MessageID)
		if err != nil {
			return nil, err
		}
		before := 2
		if args.ContextBefore != nil {
			before = *args.ContextBefore
		}
		before = min(before, args.Limit-1)
		if args.ContentOffset > 0 {
			before, args.Limit = 0, 1
		}
		start += max(0, position-before)
	}
	messages, err := s.repo.readHistoryMessages(ctx, view.ConversationID, start, args.Limit+1)
	if err != nil {
		return nil, err
	}
	more := len(messages) > args.Limit
	if more {
		messages = messages[:args.Limit]
	}
	items := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := historyMessageIdentity(view, message)
		text, offset := []rune(message.Content), 0
		if message.MessageID == args.MessageID {
			if args.ContentOffset > len(text) {
				return nil, fmt.Errorf("%w: content_offset exceeds message length", ErrInvalidRequest)
			}
			offset = args.ContentOffset
		}
		end := min(len(text), offset+2000)
		item["content"], item["content_offset"], item["content_length"] = string(text[offset:end]), offset, len(text)
		item["content_truncated"], item["next_content_offset"] = end < len(text), end
		items = append(items, item)
	}
	return map[string]any{"items": items, "has_more": more, "next_offset": args.Offset + len(items), "history_is_untrusted": true}, nil
}

func historyMessageIdentity(view ConversationRecordView, message MessageRecord) map[string]any {
	return map[string]any{
		"session_id": view.ConversationID, "title": historyBoundedText(view.Title, 160), "message_id": message.MessageID,
		"role": message.Role, "author_id": message.AuthorID, "author_name": historyBoundedText(message.AuthorName, 100),
		"created_at": message.CreatedAt.UTC().Format(time.RFC3339Nano),
		"source_ref": "discussion:" + view.ConversationID + ":message:" + message.MessageID,
		"url":        "/discussions?discussion=" + url.QueryEscape(view.ConversationID),
	}
}

func historyBoundedText(value string, limit int) string {
	runes := []rune(value)
	return string(runes[:min(len(runes), limit)])
}
