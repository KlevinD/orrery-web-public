package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/db"
)

type workRecordQuery struct {
	dialect db.Dialect
	where   []string
	args    []any
}

func (r *Repository) QueryTodos(ctx context.Context, viewer ViewerRef, query ListTodosQuery) (TodoPage, error) {
	if query.authorizationFiltered && len(query.authorizedIDs) == 0 {
		limit, offset := normalizePageBounds(query.Limit, query.Offset)
		return TodoPage{Todos: []TodoItem{}, Limit: limit, Offset: offset}, nil
	}
	store, table, builder, err := r.workQuery(todoTable, viewer.TenantID)
	if err != nil {
		return TodoPage{}, err
	}
	status := recordTextExpr(builder.dialect, "status")
	priority := recordTextExpr(builder.dialect, "priority")
	assigneeID := recordTextExpr(builder.dialect, "assignee_id")
	createdBy := recordTextExpr(builder.dialect, "created_by")
	if query.authorizationFiltered {
		builder.addTextSetFilter("todo_id", query.authorizedIDs)
	}

	if query.Status != "" {
		builder.add(status+" = ?", string(query.Status))
	} else if !query.IncludeCompleted {
		if query.IncludePendingDelivery {
			builder.add("("+status+" NOT IN (?, ?) OR "+recordTextExpr(builder.dialect, "source_notice_pending")+" = ?)", string(TodoStatusDone), string(TodoStatusCanceled), "true")
		} else {
			builder.add(status+" NOT IN (?, ?)", string(TodoStatusDone), string(TodoStatusCanceled))
		}
	}
	if query.Priority != "" {
		builder.add(priority+" = ?", string(query.Priority))
	}
	builder.addTextFilter("organization_id", query.OrganizationID)
	builder.addTextFilter("department_id", query.DepartmentID)
	builder.addTextFilter("project_id", query.ProjectID)
	builder.addTextFilter("development_project_id", query.DevelopmentProjectID)
	builder.addTextFilter("source_idea_id", query.SourceIdeaID)
	builder.addTextFilter("assignee_id", query.AssigneeID)
	builder.addTextFilter("created_by", query.CreatedBy)
	if query.Mine {
		builder.add("("+assigneeID+" IN (?, ?) OR ("+assigneeID+" = '' AND "+createdBy+" = ?))", viewer.ViewerID, firstNonEmptyText(viewer.AuthorizationSubjectID, viewer.ViewerID), viewer.ViewerID)
	}
	if query.Unassigned {
		builder.add(assigneeID + " = ''")
	}
	if !query.DueBefore.IsZero() {
		builder.add(recordTimeExpr(builder.dialect, "due_at")+" < "+timeArgumentExpr(builder.dialect), formatTime(query.DueBefore))
	}
	builder.addSearch(query.Q)

	limit, offset := normalizePageBounds(query.Limit, query.Offset)
	updatedAt := recordTimeExpr(builder.dialect, "updated_at")
	orderBy := "CASE WHEN " + recordTextExpr(builder.dialect, "updated_at") + " = '' THEN 1 ELSE 0 END, " + updatedAt + " DESC, " + recordTextExpr(builder.dialect, "todo_id") + " ASC"
	records, total, err := queryWorkRecords(ctx, store, table, builder, orderBy, limit, offset)
	if err != nil {
		return TodoPage{}, err
	}
	items := make([]TodoItem, 0, len(records))
	for _, record := range records {
		item, err := todoFromRecord(record, viewer.TenantID, "")
		if err != nil {
			return TodoPage{}, err
		}
		items = append(items, item)
	}
	items, err = r.attachTodoAutomations(ctx, viewer.TenantID, items)
	if err != nil {
		return TodoPage{}, err
	}
	for index := range items {
		items[index], err = r.attachTodoExecution(ctx, items[index])
		if err != nil {
			return TodoPage{}, err
		}
	}
	return TodoPage{Todos: items, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total}, nil
}

// ListProjectExecutionTodoSnapshots is a trusted projection query used only
// after DevelopmentProject authorization succeeds. It returns the minimum
// Todo fields needed to build a project execution summary; it is not an
// authorization bypass for Todo DTOs.
func (r *Repository) ListProjectExecutionTodoSnapshots(ctx context.Context, tenantID, developmentProjectID string) ([]ProjectExecutionTodoSnapshot, error) {
	tenantID = strings.TrimSpace(tenantID)
	developmentProjectID = strings.TrimSpace(developmentProjectID)
	if tenantID == "" || developmentProjectID == "" {
		return nil, ErrInvalidRequest
	}
	tenantID = normalizedTenantID(tenantID)
	store, table, builder, err := r.workQuery(todoTable, tenantID)
	if err != nil {
		return nil, err
	}
	builder.addTextFilter("development_project_id", developmentProjectID)
	const pageSize = 200
	updatedAt := recordTimeExpr(builder.dialect, "updated_at")
	orderBy := "CASE WHEN " + recordTextExpr(builder.dialect, "updated_at") + " = '' THEN 1 ELSE 0 END, " + updatedAt + " DESC, " + recordTextExpr(builder.dialect, "todo_id") + " ASC"
	items := make([]TodoItem, 0)
	for offset := 0; ; offset += pageSize {
		records, total, queryErr := queryWorkRecords(ctx, store, table, builder, orderBy, pageSize, offset)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, record := range records {
			item, decodeErr := todoFromRecord(record, tenantID, "")
			if decodeErr != nil {
				return nil, decodeErr
			}
			if item.TenantID != tenantID || item.DevelopmentProjectID != developmentProjectID {
				return nil, ErrConflict
			}
			items = append(items, item)
		}
		if offset+len(records) >= total || len(records) == 0 {
			break
		}
	}
	items, err = r.attachTodoAutomations(ctx, tenantID, items)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectExecutionTodoSnapshot, 0, len(items))
	for _, item := range items {
		item, err = r.attachTodoExecution(ctx, item)
		if err != nil {
			return nil, err
		}
		snapshot := ProjectExecutionTodoSnapshot{
			TenantID: item.TenantID, DevelopmentProjectID: item.DevelopmentProjectID,
			TodoID: item.TodoID, Title: item.Title, Status: item.Status,
			AutomationMode: item.AutomationMode, AssigneeID: item.AssigneeID, AssigneeName: item.AssigneeName,
			CreatedBy: item.CreatedBy, CreatedBySubjectID: item.CreatedBySubjectID, ReviewerID: item.ReviewerID, UpdatedAt: item.UpdatedAt,
		}
		if item.Automation != nil {
			snapshot.AutomationStatus = item.Automation.Status
		}
		if item.Execution != nil && item.Execution.Current != nil {
			snapshot.MissionID = item.Execution.Current.MissionID
			snapshot.ExecutionAttempt = item.Execution.Current.ExecutionAttempt
			snapshot.ConversationID = item.Execution.Current.ConversationID
		}
		out = append(out, snapshot)
	}
	return out, nil
}

func (r *Repository) QueryIdeas(ctx context.Context, viewer ViewerRef, query ListIdeasQuery) (IdeaPage, error) {
	store, table, builder, err := r.workQuery(ideaTable, viewer.TenantID)
	if err != nil {
		return IdeaPage{}, err
	}
	builder.addIdeaVisibility(viewer)
	if query.Status != "" {
		builder.add(recordTextExpr(builder.dialect, "status")+" = ?", string(query.Status))
	}
	if query.Visibility != "" {
		builder.add(ideaVisibilityExpr(builder.dialect)+" = ?", string(query.Visibility))
	}
	builder.addTextFilter("organization_id", query.OrganizationID)
	builder.addTextFilter("department_id", query.DepartmentID)
	builder.addTextFilter("owner_id", query.OwnerID)
	builder.addTextFilter("reviewer_id", query.ReviewerID)
	builder.addSearch(query.Q)

	limit, offset := normalizePageBounds(query.Limit, query.Offset)
	updatedAt := recordTimeExpr(builder.dialect, "updated_at")
	orderBy := "CASE WHEN " + recordTextExpr(builder.dialect, "updated_at") + " = '' THEN 1 ELSE 0 END, " + updatedAt + " DESC, " + recordTextExpr(builder.dialect, "idea_id") + " ASC"
	records, total, err := queryWorkRecords(ctx, store, table, builder, orderBy, limit, offset)
	if err != nil {
		return IdeaPage{}, err
	}
	items := make([]IdeaItem, 0, len(records))
	for _, record := range records {
		var item IdeaItem
		if err := decodePayload(record, &item); err != nil {
			return IdeaPage{}, err
		}
		item.VoterIDs = recordStrings(record, "voter_ids")
		items = append(items, ideaForViewer(normalizeIdeaItem(item), viewer))
	}
	return IdeaPage{Ideas: items, Total: total, Limit: limit, Offset: offset, HasMore: offset+len(items) < total}, nil
}

func (r *Repository) QueryWorkHubSummary(ctx context.Context, viewer ViewerRef, now, todayStart, tomorrow time.Time) (WorkHubSummary, error) {
	store, _, tasks, err := r.workQuery(todoTable, viewer.TenantID)
	if err != nil {
		return WorkHubSummary{}, err
	}
	status := recordTextExpr(tasks.dialect, "status")
	assigneeID := recordTextExpr(tasks.dialect, "assignee_id")
	createdBy := recordTextExpr(tasks.dialect, "created_by")
	dueAt := recordTimeExpr(tasks.dialect, "due_at")
	open := status + " NOT IN (?, ?)"
	mine := "(" + assigneeID + " = ? OR (" + assigneeID + " = '' AND " + createdBy + " = ?))"
	myOpen := "(" + open + " AND " + mine + ")"

	taskSQL := `SELECT
	COALESCE(SUM(CASE WHEN ` + myOpen + ` THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + myOpen + ` AND ` + dueAt + ` >= ` + timeArgumentExpr(tasks.dialect) + ` AND ` + dueAt + ` < ` + timeArgumentExpr(tasks.dialect) + ` THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + myOpen + ` AND ` + dueAt + ` < ` + timeArgumentExpr(tasks.dialect) + ` THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + status + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + status + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + status + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + status + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + status + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + open + ` AND ` + assigneeID + ` = '' THEN 1 ELSE 0 END), 0)
FROM platform_records WHERE ` + strings.Join(tasks.where, " AND ")
	taskArgs := []any{
		string(TodoStatusDone), string(TodoStatusCanceled), viewer.ViewerID, viewer.ViewerID,
		string(TodoStatusDone), string(TodoStatusCanceled), viewer.ViewerID, viewer.ViewerID, formatTime(todayStart), formatTime(tomorrow),
		string(TodoStatusDone), string(TodoStatusCanceled), viewer.ViewerID, viewer.ViewerID, formatTime(now),
		string(TodoStatusBlocked), string(TodoStatusReview), string(TodoStatusInbox), string(TodoStatusPlanned), string(TodoStatusInProgress),
		string(TodoStatusDone), string(TodoStatusCanceled),
	}
	taskArgs = append(taskArgs, tasks.args...)
	var summary WorkHubSummary
	if err := store.QueryRow(ctx, taskSQL, taskArgs...).Scan(
		&summary.MyOpenTasks,
		&summary.DueToday,
		&summary.Overdue,
		&summary.Blocked,
		&summary.AwaitingTaskReview,
		&summary.TasksInbox,
		&summary.TasksPlanned,
		&summary.TasksInProgress,
		&summary.UnassignedTeamTasks,
	); err != nil {
		return WorkHubSummary{}, fmt.Errorf("query work hub task summary: %w", err)
	}

	ideaStore, ideaTableName, ideas, err := r.workQuery(ideaTable, viewer.TenantID)
	if err != nil {
		return WorkHubSummary{}, err
	}
	if ideaStore != store {
		store = ideaStore
	}
	ideas.addIdeaVisibility(viewer)
	ideaStatus := recordTextExpr(ideas.dialect, "status")
	ideaSQL := `SELECT
	COALESCE(SUM(CASE WHEN ` + ideaStatus + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + ideaStatus + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + ideaStatus + ` = ? THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN ` + ideaStatus + ` = ? THEN 1 ELSE 0 END), 0)
FROM platform_records WHERE ` + strings.Join(ideas.where, " AND ")
	ideaArgs := []any{string(IdeaStatusSubmitted), string(IdeaStatusUnderReview), string(IdeaStatusExperiment), string(IdeaStatusConverted)}
	ideaArgs = append(ideaArgs, ideas.args...)
	if err := store.QueryRow(ctx, ideaSQL, ideaArgs...).Scan(
		&summary.IdeasSubmitted,
		&summary.IdeasUnderReview,
		&summary.IdeasInExperiment,
		&summary.IdeasConverted,
	); err != nil {
		return WorkHubSummary{}, fmt.Errorf("query work hub idea summary from %s: %w", ideaTableName, err)
	}
	summary.IdeasAwaitingReview = summary.IdeasSubmitted + summary.IdeasUnderReview
	return summary, nil
}

func (r *Repository) workQuery(logicalTable, tenantID string) (*db.Store, string, workRecordQuery, error) {
	if r == nil || r.router == nil {
		return nil, "", workRecordQuery{}, ErrServiceDependency
	}
	store, table, err := r.router.StoreForTable(logicalTable)
	if err != nil {
		return nil, "", workRecordQuery{}, err
	}
	tenantID = normalizedTenantID(tenantID)
	prefix := tenantID + "::"
	builder := workRecordQuery{dialect: store.Dialect()}
	builder.add("table_name = ?", table)
	builder.add("record_key LIKE ? ESCAPE '!'", escapeLikePattern(prefix)+"%")
	builder.add(recordTextExpr(builder.dialect, "tenant_id")+" = ?", tenantID)
	return store, table, builder, nil
}

func (q *workRecordQuery) add(condition string, args ...any) {
	q.where = append(q.where, condition)
	q.args = append(q.args, args...)
}

func (q *workRecordQuery) addTextFilter(field, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		q.add(recordTextExpr(q.dialect, field)+" = ?", value)
	}
}

func (q *workRecordQuery) addTextSetFilter(field string, values []string) {
	values = normalizeExactTextSet(values)
	if len(values) == 0 {
		q.add("1 = 0")
		return
	}
	expression := recordTextExpr(q.dialect, field)
	payload, _ := json.Marshal(values)
	if q.dialect == db.DialectPostgres {
		q.add(expression+" IN (SELECT jsonb_array_elements_text(CAST(? AS jsonb)))", string(payload))
		return
	}
	q.add(expression+" IN (SELECT value FROM json_each(?))", string(payload))
}

func normalizeExactTextSet(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (q *workRecordQuery) addSearch(value string) {
	searchText := "LOWER(" + recordSearchExpr(q.dialect) + ")"
	for _, term := range strings.Fields(strings.ToLower(strings.TrimSpace(value))) {
		q.add(searchText+" LIKE ? ESCAPE '!'", "%"+escapeLikePattern(term)+"%")
	}
}

func (q *workRecordQuery) addIdeaVisibility(viewer ViewerRef) {
	createdBy := recordTextExpr(q.dialect, "created_by")
	ownerID := recordTextExpr(q.dialect, "owner_id")
	reviewerID := recordTextExpr(q.dialect, "reviewer_id")
	visibility := ideaVisibilityExpr(q.dialect)
	departmentID := recordTextExpr(q.dialect, "department_id")
	organizationID := recordTextExpr(q.dialect, "organization_id")
	q.add(
		"("+createdBy+" = ? OR "+ownerID+" = ? OR "+reviewerID+" = ? OR ("+visibility+" = ? AND ("+organizationID+" = '' OR (? <> '' AND "+organizationID+" = ?))) OR ("+visibility+" = ? AND ? <> '' AND "+departmentID+" = ? AND ("+organizationID+" = '' OR (? <> '' AND "+organizationID+" = ?))))",
		viewer.ViewerID,
		viewer.ViewerID,
		viewer.ViewerID,
		string(IdeaVisibilityOrganization),
		viewer.OrganizationID,
		viewer.OrganizationID,
		string(IdeaVisibilityDepartment),
		viewer.DepartmentID,
		viewer.DepartmentID,
		viewer.OrganizationID,
		viewer.OrganizationID,
	)
}

func queryWorkRecords(ctx context.Context, store *db.Store, table string, query workRecordQuery, orderBy string, limit, offset int) ([]db.Record, int, error) {
	where := strings.Join(query.where, " AND ")
	var total int
	if err := store.QueryRow(ctx, "SELECT COUNT(*) FROM platform_records WHERE "+where, query.args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count %s work records: %w", table, err)
	}
	args := append(append([]any(nil), query.args...), limit, offset)
	rows, err := store.Query(ctx, "SELECT payload_json FROM platform_records WHERE "+where+" ORDER BY "+orderBy+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query %s work records: %w", table, err)
	}
	defer rows.Close()
	records := make([]db.Record, 0, limit)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, 0, fmt.Errorf("scan %s work record: %w", table, err)
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, 0, fmt.Errorf("decode %s work record: %w", table, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate %s work records: %w", table, err)
	}
	return records, total, nil
}

func normalizePageBounds(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func recordTextExpr(dialect db.Dialect, field string) string {
	if dialect == db.DialectPostgres {
		return "COALESCE(NULLIF(payload_json::jsonb ->> '" + field + "', ''), NULLIF(((NULLIF(payload_json::jsonb ->> 'payload', ''))::jsonb ->> '" + field + "'), ''), '')"
	}
	return "COALESCE(NULLIF(CAST(json_extract(payload_json, '$." + field + "') AS TEXT), ''), NULLIF(CAST(json_extract(json_extract(payload_json, '$.payload'), '$." + field + "') AS TEXT), ''), '')"
}

func recordSearchExpr(dialect db.Dialect) string {
	if dialect == db.DialectPostgres {
		return "COALESCE(NULLIF(payload_json::jsonb ->> 'search_text', ''), payload_json::jsonb ->> 'payload', '')"
	}
	return "COALESCE(NULLIF(CAST(json_extract(payload_json, '$.search_text') AS TEXT), ''), CAST(json_extract(payload_json, '$.payload') AS TEXT), '')"
}

func ideaVisibilityExpr(dialect db.Dialect) string {
	visibility := recordTextExpr(dialect, "visibility")
	return "CASE WHEN " + visibility + " = '' THEN '" + string(IdeaVisibilityOrganization) + "' ELSE " + visibility + " END"
}

func recordTimeExpr(dialect db.Dialect, field string) string {
	value := recordTextExpr(dialect, field)
	normalized := "NULLIF(CASE WHEN " + value + " LIKE '0001-01-01%' THEN '' ELSE " + value + " END, '')"
	if dialect == db.DialectPostgres {
		return "CAST(" + normalized + " AS timestamptz)"
	}
	return "julianday(" + normalized + ")"
}

func timeArgumentExpr(dialect db.Dialect) string {
	if dialect == db.DialectPostgres {
		return "CAST(? AS timestamptz)"
	}
	return "julianday(?)"
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}
