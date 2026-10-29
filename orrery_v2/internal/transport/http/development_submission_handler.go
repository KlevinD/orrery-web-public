package httptransport

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"orrery_v2/internal/app/developmentproject"
)

func (r *Router) handleTodoDevelopmentArtifacts(w http.ResponseWriter, req *http.Request, parts []string) bool {
	if r.developmentProjects == nil || len(parts) < 4 {
		return false
	}
	executions, ok := r.developmentProjects.(DevelopmentExecutionService)
	if !ok {
		return false
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeMappedError(w, req, err)
		return true
	}
	projectViewer := developmentProjectViewer(viewer)
	todoID := strings.TrimSpace(parts[2])
	if len(parts) == 4 && parts[3] == "commits" && req.Method == http.MethodGet {
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
		page, err := executions.ListTodoCommits(req.Context(), projectViewer, todoID, developmentproject.ListExecutionArtifactsQuery{Limit: limit, Offset: offset})
		if err != nil {
			writeMappedError(w, req, err)
		} else {
			writeJSON(w, http.StatusOK, page)
		}
		return true
	}
	if len(parts) == 6 && parts[3] == "execution-runs" && parts[5] == "commits" && req.Method == http.MethodPost {
		key, ok := artifactIdempotencyKey(w, req)
		if !ok {
			return true
		}
		version, err := requiredExpectedVersion(req)
		if err != nil {
			writeInvalidVersion(w, req)
			return true
		}
		var body struct {
			Summary     string `json:"summary"`
			AuthorName  string `json:"author_name"`
			AuthorEmail string `json:"author_email"`
		}
		if decodeStrictJSON(req, &body) != nil {
			writeInvalidBody(w, req)
			return true
		}
		record, err := executions.CreateControlledCommit(req.Context(), projectViewer, todoID, developmentproject.CreateCommitCommand{IdempotencyKey: key, ExecutionRunID: strings.TrimSpace(parts[4]), ExpectedRunVersion: version, Summary: body.Summary, AuthorName: body.AuthorName, AuthorEmail: body.AuthorEmail})
		if err != nil {
			writeMappedError(w, req, err)
		} else {
			writeJSON(w, http.StatusCreated, record)
		}
		return true
	}
	if parts[3] != "code-submissions" {
		return false
	}
	if len(parts) == 7 && parts[5] == "exports" && req.Method == http.MethodGet {
		download, err := executions.ExportTodoSubmission(req.Context(), projectViewer, todoID, strings.TrimSpace(parts[4]), strings.TrimSpace(parts[6]))
		if err != nil {
			writeMappedError(w, req, err)
			return true
		}
		defer download.Reader.Close()
		w.Header().Set("Content-Type", firstNonEmptyHTTPValue(download.ContentType, "application/octet-stream"))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", download.Name))
		if download.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(download.Size, 10))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, download.Reader)
		return true
	}
	if len(parts) == 4 {
		switch req.Method {
		case http.MethodGet:
			limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
			offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
			page, err := executions.ListTodoSubmissions(req.Context(), projectViewer, todoID, developmentproject.ListExecutionArtifactsQuery{Status: strings.TrimSpace(req.URL.Query().Get("status")), Limit: limit, Offset: offset})
			if err != nil {
				writeMappedError(w, req, err)
			} else {
				writeJSON(w, http.StatusOK, page)
			}
		case http.MethodPost:
			key, ok := artifactIdempotencyKey(w, req)
			if !ok {
				return true
			}
			var body struct {
				ExecutionRunID string `json:"execution_run_id"`
				HeadCommit     string `json:"head_commit"`
				MergeStrategy  string `json:"merge_strategy"`
			}
			if decodeStrictJSON(req, &body) != nil {
				writeInvalidBody(w, req)
				return true
			}
			submission, err := executions.CreateCodeSubmission(req.Context(), projectViewer, developmentproject.CreateSubmissionCommand{IdempotencyKey: key, TodoID: todoID, ExecutionRunID: body.ExecutionRunID, HeadCommit: body.HeadCommit, MergeStrategy: body.MergeStrategy})
			if err != nil {
				writeMappedError(w, req, err)
			} else {
				writeJSON(w, http.StatusCreated, submission)
			}
		default:
			return false
		}
		return true
	}
	if len(parts) == 5 && req.Method == http.MethodGet {
		submission, err := executions.GetTodoSubmission(req.Context(), projectViewer, todoID, strings.TrimSpace(parts[4]))
		if err != nil {
			writeMappedError(w, req, err)
		} else {
			writeJSON(w, http.StatusOK, submission)
		}
		return true
	}
	if len(parts) != 6 || req.Method != http.MethodPost {
		return false
	}
	key, ok := artifactIdempotencyKey(w, req)
	if !ok {
		return true
	}
	version, err := requiredExpectedVersion(req)
	if err != nil {
		writeInvalidVersion(w, req)
		return true
	}
	var body struct {
		ExpectedRevision   int    `json:"expected_revision"`
		ExpectedHeadCommit string `json:"expected_head_commit"`
		Comment            string `json:"comment"`
		ExpectedBaseHead   string `json:"expected_base_head"`
		ExpectedTreeSHA    string `json:"expected_tree_sha"`
		DiffDigest         string `json:"diff_digest"`
		ChecksDigest       string `json:"checks_digest"`
	}
	if decodeStrictJSON(req, &body) != nil {
		writeInvalidBody(w, req)
		return true
	}
	base := developmentproject.SubmissionCommand{IdempotencyKey: key, TodoID: todoID, SubmissionID: strings.TrimSpace(parts[4]), ExpectedVersion: version, ExpectedRevision: body.ExpectedRevision, ExpectedHeadCommit: body.ExpectedHeadCommit}
	var submission developmentproject.TodoCodeSubmission
	switch parts[5] {
	case "push":
		submission, err = executions.PushCodeSubmission(req.Context(), projectViewer, base)
	case "request-changes":
		submission, err = executions.RequestSubmissionChanges(req.Context(), projectViewer, developmentproject.RequestChangesCommand{SubmissionCommand: base, Comment: body.Comment})
	case "merge-preview":
		submission, err = executions.PreviewCodeSubmissionMerge(req.Context(), projectViewer, base)
	case "merge-approval":
		submission, err = executions.ApproveCodeSubmissionMerge(req.Context(), projectViewer, developmentproject.MergeApprovalCommand{SubmissionCommand: base, ExpectedBaseHead: body.ExpectedBaseHead, ExpectedTreeSHA: body.ExpectedTreeSHA, DiffDigest: body.DiffDigest, ChecksDigest: body.ChecksDigest})
	case "merge":
		submission, err = executions.MergeCodeSubmission(req.Context(), projectViewer, base)
	case "close":
		submission, err = executions.CloseCodeSubmission(req.Context(), projectViewer, base)
	case "pull-request":
		err = developmentproject.ErrMergeForbidden
	default:
		return false
	}
	if err != nil {
		writeMappedError(w, req, err)
	} else {
		writeJSON(w, http.StatusOK, submission)
	}
	return true
}

func firstNonEmptyHTTPValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func artifactIdempotencyKey(w http.ResponseWriter, req *http.Request) (string, bool) {
	key, err := requiredIdempotencyKey(req)
	if err != nil {
		writeInvalidIdempotencyKey(w, req)
		return "", false
	}
	return key, true
}
