// Package httpapi serves the OpenAPI-defined review API over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3filter"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/review"
)

var statusByCode = map[string]int{
	review.CodeNotFound:        http.StatusNotFound,
	review.CodeInvalidArgument: http.StatusBadRequest,
	review.CodeConflict:        http.StatusConflict,
	review.CodeCaptureFailed:   http.StatusUnprocessableEntity,
	"UNAUTHENTICATED":          http.StatusUnauthorized,
	"PERMISSION_DENIED":        http.StatusForbidden,
	"INTERNAL":                 http.StatusInternalServerError,
}

const healthPath = "/v1/health"

// NewHandler returns the API handler: access check, request validation against the OpenAPI
// document, then the generated router.
func NewHandler(svc *review.Service, auth *Authorizer, version string) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // validate paths regardless of the host the server is reached through
	strict := api.NewStrictHandlerWithOptions(&handlers{svc: svc, version: version}, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  writeRequestError,
		ResponseErrorHandlerFunc: writeResponseError,
	})
	router := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{ErrorHandlerFunc: writeRequestError})
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options: openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, opts middleware.ErrorHandlerOpts) {
			code := review.CodeInvalidArgument
			if opts.StatusCode == http.StatusNotFound {
				code = review.CodeNotFound
			}
			writeError(w, opts.StatusCode, code, err.Error(), nil)
		},
	})
	return auth.Middleware(validate(router), healthPath), nil
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details"`
}

func writeError(w http.ResponseWriter, status int, code, message string, details []string) {
	if details == nil {
		details = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorBody{Error: errorDetail{Code: code, Message: message, Details: details}})
}

func writeRequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeError(w, http.StatusBadRequest, review.CodeInvalidArgument, err.Error(), nil)
}

func writeResponseError(w http.ResponseWriter, r *http.Request, err error) {
	var reviewErr *review.Error
	if errors.As(err, &reviewErr) {
		writeError(w, statusByCode[reviewErr.Code], reviewErr.Code, reviewErr.Message, reviewErr.Details)
		return
	}
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}

type handlers struct {
	svc     *review.Service
	version string
}

var _ api.StrictServerInterface = (*handlers)(nil)

func (h *handlers) GetHealth(context.Context, api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return api.GetHealth200JSONResponse{Status: api.Ok, Version: h.version}, nil
}

func (h *handlers) ListWorkspaces(ctx context.Context, _ api.ListWorkspacesRequestObject) (api.ListWorkspacesResponseObject, error) {
	ws, err := h.svc.ListWorkspaces(ctx)
	return api.ListWorkspaces200JSONResponse(ws), err
}

func (h *handlers) CreateWorkspace(ctx context.Context, req api.CreateWorkspaceRequestObject) (api.CreateWorkspaceResponseObject, error) {
	ws, err := h.svc.CreateWorkspace(ctx, *req.Body)
	return api.CreateWorkspace201JSONResponse(ws), err
}

func (h *handlers) GetWorkspace(ctx context.Context, req api.GetWorkspaceRequestObject) (api.GetWorkspaceResponseObject, error) {
	ws, err := h.svc.GetWorkspace(ctx, req.WorkspaceId)
	return api.GetWorkspace200JSONResponse(ws), err
}

func (h *handlers) ListReviews(ctx context.Context, req api.ListReviewsRequestObject) (api.ListReviewsResponseObject, error) {
	reviews, err := h.svc.ListReviews(ctx, req.Params.WorkspaceId)
	return api.ListReviews200JSONResponse(reviews), err
}

func (h *handlers) CreateReview(ctx context.Context, req api.CreateReviewRequestObject) (api.CreateReviewResponseObject, error) {
	rv, err := h.svc.CreateReview(ctx, *req.Body)
	return api.CreateReview201JSONResponse(rv), err
}

func (h *handlers) GetReview(ctx context.Context, req api.GetReviewRequestObject) (api.GetReviewResponseObject, error) {
	rv, err := h.svc.GetReview(ctx, req.ReviewId)
	return api.GetReview200JSONResponse(rv), err
}

func (h *handlers) ListRevisions(ctx context.Context, req api.ListRevisionsRequestObject) (api.ListRevisionsResponseObject, error) {
	revisions, err := h.svc.ListRevisions(ctx, req.ReviewId)
	return api.ListRevisions200JSONResponse(revisions), err
}

func (h *handlers) CaptureRevision(ctx context.Context, req api.CaptureRevisionRequestObject) (api.CaptureRevisionResponseObject, error) {
	revision, err := h.svc.CaptureRevision(ctx, req.ReviewId)
	return api.CaptureRevision201JSONResponse(revision), err
}

func (h *handlers) GetRevision(ctx context.Context, req api.GetRevisionRequestObject) (api.GetRevisionResponseObject, error) {
	revision, err := h.svc.GetRevision(ctx, req.RevisionId)
	return api.GetRevision200JSONResponse(revision), err
}

func (h *handlers) GetSourceChanges(ctx context.Context, req api.GetSourceChangesRequestObject) (api.GetSourceChangesResponseObject, error) {
	changes, err := h.svc.SourceChanges(ctx, req.RevisionId)
	return api.GetSourceChanges200JSONResponse(changes), err
}

func (h *handlers) GetFile(ctx context.Context, req api.GetFileRequestObject) (api.GetFileResponseObject, error) {
	file, err := h.svc.GetFile(ctx, req.RevisionId, req.Params.RepositoryId, req.Params.Path)
	return api.GetFile200JSONResponse(file), err
}

func (h *handlers) ListImports(ctx context.Context, req api.ListImportsRequestObject) (api.ListImportsResponseObject, error) {
	imports, err := h.svc.ListImports(ctx, req.ReviewId)
	return api.ListImports200JSONResponse(imports), err
}

func (h *handlers) ImportManifest(ctx context.Context, req api.ImportManifestRequestObject) (api.ImportManifestResponseObject, error) {
	result, err := h.svc.Import(ctx, req.ReviewId, *req.Body)
	return api.ImportManifest201JSONResponse(result), err
}

func (h *handlers) ListTopics(ctx context.Context, req api.ListTopicsRequestObject) (api.ListTopicsResponseObject, error) {
	topics, err := h.svc.ListTopics(ctx, req.ReviewId)
	return api.ListTopics200JSONResponse(topics), err
}

func (h *handlers) GetTopic(ctx context.Context, req api.GetTopicRequestObject) (api.GetTopicResponseObject, error) {
	topic, err := h.svc.GetTopic(ctx, req.TopicId)
	return api.GetTopic200JSONResponse(topic), err
}

func (h *handlers) ListTopicRepresentations(ctx context.Context, req api.ListTopicRepresentationsRequestObject) (api.ListTopicRepresentationsResponseObject, error) {
	reps, err := h.svc.TopicRepresentations(ctx, req.TopicId)
	return api.ListTopicRepresentations200JSONResponse(reps), err
}

func (h *handlers) ListIntents(ctx context.Context, req api.ListIntentsRequestObject) (api.ListIntentsResponseObject, error) {
	intents, err := h.svc.ListIntents(ctx, req.ReviewId)
	return api.ListIntents200JSONResponse(intents), err
}

func (h *handlers) ListChanges(ctx context.Context, req api.ListChangesRequestObject) (api.ListChangesResponseObject, error) {
	changes, err := h.svc.ListChanges(ctx, req.ReviewId, req.Params.RevisionId)
	return api.ListChanges200JSONResponse(changes), err
}

func (h *handlers) ListFindings(ctx context.Context, req api.ListFindingsRequestObject) (api.ListFindingsResponseObject, error) {
	findings, err := h.svc.ListFindings(ctx, req.ReviewId)
	return api.ListFindings200JSONResponse(findings), err
}

func (h *handlers) ListEvidence(ctx context.Context, req api.ListEvidenceRequestObject) (api.ListEvidenceResponseObject, error) {
	evidence, err := h.svc.ListEvidence(ctx, req.ReviewId)
	return api.ListEvidence200JSONResponse(evidence), err
}

func (h *handlers) GetEvidence(ctx context.Context, req api.GetEvidenceRequestObject) (api.GetEvidenceResponseObject, error) {
	evidence, err := h.svc.GetEvidence(ctx, req.EvidenceId)
	return api.GetEvidence200JSONResponse(evidence), err
}

func (h *handlers) ListAttention(ctx context.Context, req api.ListAttentionRequestObject) (api.ListAttentionResponseObject, error) {
	signals, err := h.svc.ListAttention(ctx, req.ReviewId)
	return api.ListAttention200JSONResponse(signals), err
}
