package knowledgeadmin

import (
	"encoding/json"
	pathpkg "path"
	"strconv"
	"strings"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

func mapBase(base core.KnowledgeBase) KnowledgeBaseDTO {
	return KnowledgeBaseDTO{
		ID:                  string(base.ID),
		Name:                base.Name,
		Description:         base.Description,
		OwnerType:           string(base.OwnerType),
		OwnerID:             base.OwnerID,
		IsDefault:           base.IsDefault,
		Visibility:          string(base.Visibility),
		Provider:            string(base.EffectiveProvider()),
		ProviderResourceID:  base.ProviderResourceID,
		ProviderBindingID:   base.ProviderMetadata["binding_id"],
		ProviderDisplayName: base.ProviderMetadata["provider_display_name"],
		VectorBackend:       string(base.VectorBackend),
		VectorStorePath:     base.VectorStorePath,
		EmbeddingProfileRef: base.EmbeddingProfileRef,
		EmbeddingModel:      base.EmbeddingModel,
		EmbeddingDimensions: base.EmbeddingDimensions,
		ChunkPolicy:         base.ChunkPolicy,
		Status:              string(base.Status),
	}
}

func mapBinding(binding core.KnowledgeBinding) KnowledgeBindingDTO {
	return KnowledgeBindingDTO{
		ID:              string(binding.ID),
		KnowledgeBaseID: string(binding.KnowledgeBaseID),
		BoundToType:     string(binding.BoundToType),
		BoundToID:       binding.BoundToID,
		Access:          string(binding.Access),
	}
}

func mapDocument(doc core.KnowledgeDocument) KnowledgeDocumentDTO {
	return KnowledgeDocumentDTO{
		ID:                  string(doc.ID),
		KnowledgeBaseID:     string(doc.KnowledgeBaseID),
		ArtifactID:          doc.ArtifactID,
		ProviderDocumentID:  doc.ProviderDocumentID,
		Filename:            doc.Filename,
		FolderPath:          documentFolderPath(doc),
		ContentType:         doc.ContentType,
		SizeBytes:           doc.SizeBytes,
		Checksum:            doc.Checksum,
		Status:              string(doc.Status),
		ErrorMessage:        doc.ErrorMessage,
		JobID:               doc.ProviderMetadata["pageindex_job_id"],
		ProcessingProgress:  documentProcessingProgress(doc),
		ProcessingStage:     doc.ProviderMetadata["pageindex_job_stage"],
		ProcessingJobStatus: doc.ProviderMetadata["pageindex_job_status"],
	}
}

func documentFolderPath(doc core.KnowledgeDocument) string {
	for _, key := range []string{"folder_path", "directory", "relative_path", "pageindex_upload_path"} {
		if folder := safeDocumentFolderPath(doc.ProviderMetadata[key], doc.Filename); folder != "" {
			return folder
		}
	}

	var external map[string]any
	if raw := strings.TrimSpace(doc.ProviderMetadata["external_metadata"]); raw != "" && json.Unmarshal([]byte(raw), &external) == nil {
		for _, key := range []string{"folder_path", "folderPath", "directory", "relative_path", "relativePath", "path"} {
			if value, ok := external[key].(string); ok {
				if folder := safeDocumentFolderPath(value, doc.Filename); folder != "" {
					return folder
				}
			}
		}
	}

	return safeDocumentFolderPath(doc.Filename, doc.Filename)
}

func safeDocumentFolderPath(value, filename string) string {
	raw := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "://") || strings.Contains(raw, ":/") {
		return ""
	}
	cleaned := pathpkg.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	name := pathpkg.Base(strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/")))
	if name != "" && (cleaned == name || strings.HasSuffix(cleaned, "/"+name)) {
		cleaned = pathpkg.Dir(cleaned)
	}
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.Trim(cleaned, "/")
}

func mapJob(job core.KnowledgeIngestJob) KnowledgeJobDTO {
	return KnowledgeJobDTO{
		ID:              string(job.ID),
		KnowledgeBaseID: string(job.KnowledgeBaseID),
		DocumentID:      string(job.DocumentID),
		Topic:           job.Topic,
		Status:          string(job.Status),
		ErrorMessage:    job.ErrorMessage,
	}
}

func mapProcessingResult(doc core.KnowledgeDocument, result provider.DocumentProcessingResult) KnowledgeDocumentProcessingResultDTO {
	dto := KnowledgeDocumentProcessingResultDTO{
		Document:       mapDocument(doc),
		Status:         string(result.Status),
		Progress:       result.Progress,
		Stage:          result.Stage,
		ErrorMessage:   result.ErrorMessage,
		Structure:      cloneAnyMapSlice(result.Structure),
		Pages:          cloneAnyMapSlice(result.Pages),
		StructureNodes: mapDocumentStructureNodes(result.StructureNodes),
		PagePreviews:   mapDocumentPagePreviews(result.PagePreviews),
	}
	if result.Job != nil {
		dto.Job = &DocumentProcessingJobDTO{
			ID:           result.Job.JobID,
			DocumentID:   result.Job.DocumentID,
			Topic:        result.Job.Topic,
			Status:       result.Job.Status,
			Progress:     result.Job.Progress,
			Stage:        result.Job.Stage,
			ErrorMessage: result.Job.ErrorMessage,
		}
	}
	return dto
}

func mapDocumentStructureNodes(src []provider.DocumentStructureNode) []DocumentStructureNodeDTO {
	if len(src) == 0 {
		return nil
	}
	out := make([]DocumentStructureNodeDTO, 0, len(src))
	for _, item := range src {
		out = append(out, DocumentStructureNodeDTO{
			ProviderNodeID: item.ProviderNodeID,
			Text:           item.Text,
			PageNumber:     item.PageNumber,
			HeadingPath:    append([]string(nil), item.HeadingPath...),
			StructurePath:  append([]string(nil), item.StructurePath...),
			BBox:           cloneAnyMap(item.BBox),
			Children:       mapDocumentStructureNodes(item.Children),
		})
	}
	return out
}

func mapDocumentPagePreviews(src []provider.DocumentPagePreview) []DocumentPagePreviewDTO {
	if len(src) == 0 {
		return nil
	}
	out := make([]DocumentPagePreviewDTO, 0, len(src))
	for _, item := range src {
		out = append(out, DocumentPagePreviewDTO{
			PageNumber:     item.PageNumber,
			ProviderNodeID: item.ProviderNodeID,
			HeadingPath:    append([]string(nil), item.HeadingPath...),
			StructurePath:  append([]string(nil), item.StructurePath...),
			BBox:           cloneAnyMap(item.BBox),
			ImageRef:       item.ImageRef,
			Text:           item.Text,
			ContentHash:    item.ContentHash,
			SnippetHash:    item.SnippetHash,
			Raw:            cloneAnyMap(item.Raw),
		})
	}
	return out
}

func cloneAnyMapSlice(src []map[string]any) []map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(src))
	for _, item := range src {
		out = append(out, cloneAnyMap(item))
	}
	return out
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func documentProcessingProgress(doc core.KnowledgeDocument) int {
	if raw := doc.ProviderMetadata["pageindex_job_progress"]; raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil {
			if parsed < 0 {
				return 0
			}
			if parsed > 100 {
				return 100
			}
			return parsed
		}
	}
	if doc.Status == core.DocumentIndexed {
		return 100
	}
	return 0
}
