// Package httpapi serves the OpenAPI-defined review API over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/store"
)

var statusByCode = map[string]int{
	review.CodeNotFound:           http.StatusNotFound,
	review.CodeInvalidArgument:    http.StatusBadRequest,
	review.CodeConflict:           http.StatusConflict,
	review.CodeCaptureFailed:      http.StatusUnprocessableEntity,
	review.CodePreconditionFailed: http.StatusPreconditionFailed,
	codePreconditionRequired:      http.StatusPreconditionRequired,
	"UNAUTHENTICATED":             http.StatusUnauthorized,
	review.CodePermissionDenied:   http.StatusForbidden,
	"INTERNAL":                    http.StatusInternalServerError,
}

const healthPath = "/v1/health"

const codePreconditionRequired = "PRECONDITION_REQUIRED"

// NewHandler returns the API handler: access check, request validation against the OpenAPI
// document, idempotent replay, then the generated router. Event streams end when lifetime is done,
// so that shutdown does not wait for them; other requests are left to finish.
func NewHandler(lifetime context.Context, db *store.DB, reviews *review.Service, sessionSvc *sessions.Service, auth *Authorizer, version string) (http.Handler, error) {
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	spec.Servers = nil // validate paths regardless of the host the server is reached through
	strict := api.NewStrictHandlerWithOptions(&handlers{lifetime: lifetime, db: db, svc: reviews, sessions: sessionSvc, version: version}, nil, api.StrictHTTPServerOptions{
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
	return auth.Middleware(validate((&idempotency{db: db}).middleware(router)), healthPath), nil
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
	lifetime context.Context
	db       *store.DB
	svc      *review.Service
	sessions *sessions.Service
	version  string
}

var _ api.StrictServerInterface = (*handlers)(nil)

func (h *handlers) GetHealth(context.Context, api.GetHealthRequestObject) (api.GetHealthResponseObject, error) {
	return api.GetHealth200JSONResponse{Status: api.Ok, Version: h.version}, nil
}

func (h *handlers) DeleteReview(ctx context.Context, req api.DeleteReviewRequestObject) (api.DeleteReviewResponseObject, error) {
	return api.DeleteReview204Response{}, h.svc.DeleteReview(ctx, req.ReviewId, callerFrom(ctx))
}

func (h *handlers) ResolveReview(ctx context.Context, req api.ResolveReviewRequestObject) (api.ResolveReviewResponseObject, error) {
	rv, err := h.svc.ResolveReview(ctx, req.Params.Path)
	return api.ResolveReview200JSONResponse(rv), err
}

func (h *handlers) ListReviews(ctx context.Context, _ api.ListReviewsRequestObject) (api.ListReviewsResponseObject, error) {
	reviews, err := h.svc.ListReviews(ctx)
	return api.ListReviews200JSONResponse(reviews), err
}

func (h *handlers) CreateReview(ctx context.Context, req api.CreateReviewRequestObject) (api.CreateReviewResponseObject, error) {
	rv, err := h.svc.CreateReview(ctx, *req.Body, callerFrom(ctx))
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
	input := review.CaptureRequest{}
	if req.Body != nil {
		input = *req.Body
	}
	revision, err := h.svc.CaptureRevision(ctx, req.ReviewId, input, callerFrom(ctx))
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
	result, err := h.svc.Import(ctx, req.ReviewId, *req.Body, callerFrom(ctx))
	return api.ImportManifest201JSONResponse(result), err
}

func (h *handlers) ListTopics(ctx context.Context, req api.ListTopicsRequestObject) (api.ListTopicsResponseObject, error) {
	topics, err := h.svc.ListTopics(ctx, req.ReviewId)
	return api.ListTopics200JSONResponse(topics), err
}

func (h *handlers) GetTopic(ctx context.Context, req api.GetTopicRequestObject) (api.GetTopicResponseObject, error) {
	topic, err := h.svc.GetTopic(ctx, req.TopicId)
	return api.GetTopic200JSONResponse{Body: topic, Headers: api.GetTopic200ResponseHeaders{ETag: etag(topic.Version)}}, err
}

func (h *handlers) CreateTopic(ctx context.Context, req api.CreateTopicRequestObject) (api.CreateTopicResponseObject, error) {
	topic, err := h.svc.CreateTopic(ctx, req.ReviewId, *req.Body, callerFrom(ctx))
	return api.CreateTopic201JSONResponse(topic), err
}

func (h *handlers) UpdateTopic(ctx context.Context, req api.UpdateTopicRequestObject) (api.UpdateTopicResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	topic, err := h.svc.UpdateTopic(ctx, req.TopicId, version, *req.Body, callerFrom(ctx))
	return api.UpdateTopic200JSONResponse{Body: topic, Headers: api.UpdateTopic200ResponseHeaders{ETag: etag(topic.Version)}}, err
}

func (h *handlers) MoveTopic(ctx context.Context, req api.MoveTopicRequestObject) (api.MoveTopicResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	topic, err := h.svc.MoveTopic(ctx, req.TopicId, version, *req.Body, callerFrom(ctx))
	return api.MoveTopic200JSONResponse{Body: topic, Headers: api.MoveTopic200ResponseHeaders{ETag: etag(topic.Version)}}, err
}

func (h *handlers) CreateRepresentation(ctx context.Context, req api.CreateRepresentationRequestObject) (api.CreateRepresentationResponseObject, error) {
	rep, err := h.svc.CreateRepresentation(ctx, req.TopicId, *req.Body, callerFrom(ctx))
	return api.CreateRepresentation201JSONResponse(rep), err
}

func (h *handlers) UpdateRepresentation(ctx context.Context, req api.UpdateRepresentationRequestObject) (api.UpdateRepresentationResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	rep, err := h.svc.UpdateRepresentation(ctx, req.RepresentationId, version, *req.Body, callerFrom(ctx))
	if err != nil {
		return nil, err
	}
	return api.UpdateRepresentation200JSONResponse{Body: rep, Headers: api.UpdateRepresentation200ResponseHeaders{ETag: etag(*rep.Version)}}, nil
}

func (h *handlers) ListDiscussions(ctx context.Context, req api.ListDiscussionsRequestObject) (api.ListDiscussionsResponseObject, error) {
	discussions, err := h.svc.ListDiscussions(ctx, req.ReviewId)
	return api.ListDiscussions200JSONResponse(discussions), err
}

func (h *handlers) CreateDiscussion(ctx context.Context, req api.CreateDiscussionRequestObject) (api.CreateDiscussionResponseObject, error) {
	discussion, err := h.svc.CreateDiscussion(ctx, req.ReviewId, *req.Body, callerFrom(ctx))
	return api.CreateDiscussion201JSONResponse(discussion), err
}

func (h *handlers) GetDiscussion(ctx context.Context, req api.GetDiscussionRequestObject) (api.GetDiscussionResponseObject, error) {
	discussion, err := h.svc.GetDiscussion(ctx, req.DiscussionId)
	return api.GetDiscussion200JSONResponse{Body: discussion, Headers: api.GetDiscussion200ResponseHeaders{ETag: etag(discussion.Version)}}, err
}

func (h *handlers) UpdateDiscussion(ctx context.Context, req api.UpdateDiscussionRequestObject) (api.UpdateDiscussionResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	discussion, err := h.svc.UpdateDiscussion(ctx, req.DiscussionId, version, *req.Body, callerFrom(ctx))
	return api.UpdateDiscussion200JSONResponse{Body: discussion, Headers: api.UpdateDiscussion200ResponseHeaders{ETag: etag(discussion.Version)}}, err
}

func (h *handlers) CreateMessage(ctx context.Context, req api.CreateMessageRequestObject) (api.CreateMessageResponseObject, error) {
	message, err := h.svc.Reply(ctx, req.DiscussionId, *req.Body, callerFrom(ctx))
	return api.CreateMessage201JSONResponse(message), err
}

func (h *handlers) ListDecisions(ctx context.Context, req api.ListDecisionsRequestObject) (api.ListDecisionsResponseObject, error) {
	decisions, err := h.svc.ListDecisions(ctx, req.ReviewId)
	return api.ListDecisions200JSONResponse(decisions), err
}

func (h *handlers) CreateDecision(ctx context.Context, req api.CreateDecisionRequestObject) (api.CreateDecisionResponseObject, error) {
	decision, err := h.svc.CreateDecision(ctx, req.ReviewId, *req.Body, callerFrom(ctx))
	return api.CreateDecision201JSONResponse(decision), err
}

func etag(version int) string { return `"` + strconv.Itoa(version) + `"` }

// expectedVersion reads the version a change is based on from If-Match.
func expectedVersion(ifMatch *string) (int, error) {
	if ifMatch == nil {
		return 0, &review.Error{Code: codePreconditionRequired, Message: "If-Match with the ETag of the version you edited is required"}
	}
	unquoted, err := strconv.Unquote(strings.TrimSpace(*ifMatch))
	version, atoiErr := strconv.Atoi(unquoted)
	if err != nil || atoiErr != nil {
		return 0, &review.Error{Code: review.CodePreconditionFailed, Message: fmt.Sprintf("If-Match %s is not an ETag of this server", *ifMatch)}
	}
	return version, nil
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

func (h *handlers) ListSessionBackends(ctx context.Context, _ api.ListSessionBackendsRequestObject) (api.ListSessionBackendsResponseObject, error) {
	backends, err := h.sessions.ListBackends(ctx)
	return api.ListSessionBackends200JSONResponse(backends), err
}

func (h *handlers) ListRuntimeSessions(ctx context.Context, req api.ListRuntimeSessionsRequestObject) (api.ListRuntimeSessionsResponseObject, error) {
	runtimes, err := h.sessions.ListRuntimeSessions(ctx, req.Params.ReviewId)
	return api.ListRuntimeSessions200JSONResponse(runtimes), err
}

func (h *handlers) GetRuntimeSession(ctx context.Context, req api.GetRuntimeSessionRequestObject) (api.GetRuntimeSessionResponseObject, error) {
	runtime, err := h.sessions.GetRuntimeSession(ctx, req.RuntimeSessionId)
	return api.GetRuntimeSession200JSONResponse(runtime), err
}

func (h *handlers) AssociateRuntimeSession(ctx context.Context, req api.AssociateRuntimeSessionRequestObject) (api.AssociateRuntimeSessionResponseObject, error) {
	runtime, err := h.sessions.Associate(ctx, req.ReviewId, req.RuntimeSessionId)
	return api.AssociateRuntimeSession200JSONResponse(runtime), err
}

func (h *handlers) DissociateRuntimeSession(ctx context.Context, req api.DissociateRuntimeSessionRequestObject) (api.DissociateRuntimeSessionResponseObject, error) {
	return api.DissociateRuntimeSession204Response{}, h.sessions.Dissociate(ctx, req.ReviewId, req.RuntimeSessionId)
}
