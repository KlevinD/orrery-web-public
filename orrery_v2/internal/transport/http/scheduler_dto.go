package httptransport

import (
	"encoding/json"
	"path"
	"strings"
	"time"
	"unicode"

	"orrery_v2/internal/app/scheduler"
)

type scheduledRunAlias scheduler.ScheduledRun

type scheduledRunDTO struct {
	scheduledRunAlias
	Result         *scheduledRunResultDTO `json:"result,omitempty"`
	DeliveryStatus string                 `json:"delivery_status,omitempty"`
}

func (dto scheduledRunDTO) MarshalJSON() ([]byte, error) {
	type rawScheduledRunDTO scheduledRunDTO
	raw, err := json.Marshal(rawScheduledRunDTO(dto))
	if err != nil {
		return nil, err
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(raw, &public); err != nil {
		return nil, err
	}
	for _, field := range []string{
		"configuration_snapshot_version", "configuration_snapshot", "tenant_id", "scheduled_task_title", "result_contract", "delivery", "runtime_run_id",
		"trace_available", "trace_status", "delivery_error", "external_message_id",
	} {
		delete(public, field)
	}
	return json.Marshal(public)
}

type scheduledRunResultDTO struct {
	Summary   string                    `json:"summary,omitempty"`
	Artifacts []scheduledRunArtifactDTO `json:"artifacts,omitempty"`
}

type scheduledRunArtifactDTO struct {
	ID                         string `json:"id"`
	Kind                       string `json:"kind"`
	FileName                   string `json:"file_name"`
	SizeBytes                  *int64 `json:"size_bytes,omitempty"`
	EvidenceID                 string `json:"evidence_id,omitempty"`
	Previewable                bool   `json:"previewable"`
	ContentAvailable           bool   `json:"content_available"`
	ContentAvailabilityChecked bool   `json:"content_availability_checked"`
}

type scheduledRunPageDTO struct {
	Runs   []scheduledRunDTO `json:"runs"`
	Total  int               `json:"total"`
	Limit  int               `json:"limit,omitempty"`
	Offset int               `json:"offset,omitempty"`
}

type scheduledRunInboxItemDTO struct {
	Run               scheduledRunDTO `json:"run"`
	TaskTitle         string          `json:"task_title"`
	IsRead            bool            `json:"is_read"`
	ReadAt            string          `json:"read_at,omitempty"`
	AttentionRequired bool            `json:"attention_required"`
}

type scheduledRunInboxPageDTO struct {
	Items          []scheduledRunInboxItemDTO `json:"items"`
	Total          int                        `json:"total"`
	UnreadTotal    int                        `json:"unread_total"`
	AttentionTotal int                        `json:"attention_total"`
	Limit          int                        `json:"limit"`
	Offset         int                        `json:"offset"`
}

type scheduledRunReadStateDTO struct {
	ScheduledRunID string `json:"scheduled_run_id"`
	IsRead         bool   `json:"is_read"`
	ReadAt         string `json:"read_at,omitempty"`
}

func toScheduledRunDTO(run scheduler.ScheduledRun) scheduledRunDTO {
	dto := scheduledRunDTO{
		scheduledRunAlias: scheduledRunAlias(run),
		DeliveryStatus:    publicScheduledRunDeliveryStatus(run.DeliveryStatus),
	}
	if strings.TrimSpace(run.Result.Summary) == "" && len(run.Result.Artifacts) == 0 {
		return dto
	}
	result := &scheduledRunResultDTO{Summary: run.Result.Summary}
	for index, artifact := range run.Result.Artifacts {
		fileName := scheduledArtifactFileName(artifact)
		var sizeBytes *int64
		if artifact.SizeBytes > 0 || strings.TrimSpace(artifact.SHA256) != "" {
			size := artifact.SizeBytes
			sizeBytes = &size
		}
		result.Artifacts = append(result.Artifacts, scheduledRunArtifactDTO{
			ID:                         scheduler.PublicRunArtifactID(run.ScheduledRunID, index, artifact),
			Kind:                       artifact.Kind,
			FileName:                   fileName,
			SizeBytes:                  sizeBytes,
			EvidenceID:                 artifact.EvidenceID,
			Previewable:                scheduledArtifactPreviewable(fileName, artifact.Kind, artifact.SizeBytes),
			ContentAvailable:           artifact.ContentAvailable,
			ContentAvailabilityChecked: artifact.ContentAvailabilityChecked,
		})
	}
	dto.Result = result
	return dto
}

func publicScheduledRunDeliveryStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "not_requested", "pending", "sending", "retrying", "delivered", "failed", "dead_letter", "not_recovered":
		return strings.TrimSpace(status)
	default:
		return ""
	}
}

func toScheduledRunPageDTO(page scheduler.ScheduledRunPage) scheduledRunPageDTO {
	runs := make([]scheduledRunDTO, 0, len(page.Runs))
	for _, run := range page.Runs {
		runs = append(runs, toScheduledRunDTO(run))
	}
	return scheduledRunPageDTO{Runs: runs, Total: page.Total, Limit: page.Limit, Offset: page.Offset}
}

func toScheduledRunInboxPageDTO(page scheduler.ScheduledRunInboxPage) scheduledRunInboxPageDTO {
	items := make([]scheduledRunInboxItemDTO, 0, len(page.Items))
	for _, item := range page.Items {
		readAt := ""
		if !item.ReadAt.IsZero() {
			readAt = item.ReadAt.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, scheduledRunInboxItemDTO{
			Run: toScheduledRunDTO(item.Run), TaskTitle: item.TaskTitle, IsRead: item.IsRead,
			ReadAt: readAt, AttentionRequired: item.AttentionRequired,
		})
	}
	return scheduledRunInboxPageDTO{
		Items: items, Total: page.Total, UnreadTotal: page.UnreadTotal, AttentionTotal: page.AttentionTotal,
		Limit: page.Limit, Offset: page.Offset,
	}
}

func toScheduledRunReadStateDTO(state scheduler.ScheduledRunReadState) scheduledRunReadStateDTO {
	readAt := ""
	if !state.ReadAt.IsZero() {
		readAt = state.ReadAt.UTC().Format(time.RFC3339Nano)
	}
	return scheduledRunReadStateDTO{ScheduledRunID: state.ScheduledRunID, IsRead: state.IsRead, ReadAt: readAt}
}

func scheduledArtifactFileName(artifact scheduler.RunArtifact) string {
	source := strings.TrimSpace(artifact.FileName)
	if source == "" {
		source = artifact.URI
	}
	fileName := path.Base(strings.ReplaceAll(strings.TrimSpace(source), "\\", "/"))
	fileName = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, fileName)
	if fileName == "" || fileName == "." || fileName == "/" {
		fileName = strings.TrimSpace(artifact.ID)
	}
	if fileName == "" {
		return "artifact"
	}
	return fileName
}

func scheduledArtifactPreviewable(fileName, kind string, sizeBytes int64) bool {
	if sizeBytes > 8<<20 {
		return false
	}
	extension := strings.ToLower(path.Ext(fileName))
	switch extension {
	case ".md", ".markdown", ".txt", ".log", ".json", ".csv", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf":
		return true
	case ".html", ".htm", ".svg", ".xml":
		return false
	}
	if extension != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "report", "log", "text", "json":
		return true
	default:
		return false
	}
}
