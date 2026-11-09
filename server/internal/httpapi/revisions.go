package httpapi

import (
	"context"

	"github.com/aduverger/prunelle/server/api"
)

func (h *handlers) GetRevisionView(ctx context.Context, req api.GetRevisionViewRequestObject) (api.GetRevisionViewResponseObject, error) {
	view, err := h.svc.GetRevisionView(ctx, req.ReviewId, req.Params.RevisionId)
	return api.GetRevisionView200JSONResponse(view), err
}

func (h *handlers) CompareRevisions(ctx context.Context, req api.CompareRevisionsRequestObject) (api.CompareRevisionsResponseObject, error) {
	comparison, err := h.svc.CompareRevisions(ctx, req.ReviewId, req.Params.FromRevisionId, req.Params.ToRevisionId)
	return api.CompareRevisions200JSONResponse(comparison), err
}

func (h *handlers) GetDiscussionAnchorStatuses(ctx context.Context, req api.GetDiscussionAnchorStatusesRequestObject) (api.GetDiscussionAnchorStatusesResponseObject, error) {
	statuses, err := h.svc.DiscussionAnchorStatuses(ctx, req.ReviewId, req.Params.RevisionId)
	return api.GetDiscussionAnchorStatuses200JSONResponse(statuses), err
}

func (h *handlers) UpdateFinding(ctx context.Context, req api.UpdateFindingRequestObject) (api.UpdateFindingResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	finding, err := h.svc.UpdateFinding(ctx, req.FindingId, version, *req.Body, callerFrom(ctx))
	return api.UpdateFinding200JSONResponse{Body: finding, Headers: api.UpdateFinding200ResponseHeaders{ETag: etag(finding.Version)}}, err
}

func (h *handlers) UpdateSourceTopics(ctx context.Context, req api.UpdateSourceTopicsRequestObject) (api.UpdateSourceTopicsResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	file, err := h.svc.UpdateSourceTopics(ctx, req.RevisionId, version, *req.Body, callerFrom(ctx))
	return api.UpdateSourceTopics200JSONResponse{Body: file, Headers: api.UpdateSourceTopics200ResponseHeaders{ETag: etag(file.AssignmentVersion)}}, err
}
