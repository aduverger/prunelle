package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/agents"
)

func (h *handlers) ListAgentSessions(ctx context.Context, _ api.ListAgentSessionsRequestObject) (api.ListAgentSessionsResponseObject, error) {
	sessions, err := h.agents.ListSessions(ctx)
	return api.ListAgentSessions200JSONResponse(sessions), err
}
func (h *handlers) RegisterAgentSession(ctx context.Context, req api.RegisterAgentSessionRequestObject) (api.RegisterAgentSessionResponseObject, error) {
	session, err := h.agents.Register(ctx, *req.Body, localConnection(ctx), callerFrom(ctx))
	return api.RegisterAgentSession200JSONResponse(session), err
}
func (h *handlers) UpdateAgentSessionState(ctx context.Context, req api.UpdateAgentSessionStateRequestObject) (api.UpdateAgentSessionStateResponseObject, error) {
	session, err := h.agents.UpdateState(ctx, req.AgentSessionId, *req.Body, callerFrom(ctx))
	return api.UpdateAgentSessionState200JSONResponse(session), err
}
func (h *handlers) GetAgentBinding(ctx context.Context, req api.GetAgentBindingRequestObject) (api.GetAgentBindingResponseObject, error) {
	binding, err := h.agents.GetBinding(ctx, req.ReviewId)
	return api.GetAgentBinding200JSONResponse{Body: binding, Headers: api.GetAgentBinding200ResponseHeaders{ETag: etag(binding.Version)}}, err
}
func (h *handlers) SetAgentBinding(ctx context.Context, req api.SetAgentBindingRequestObject) (api.SetAgentBindingResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	binding, err := h.agents.SetBinding(ctx, req.ReviewId, version, *req.Body, callerFrom(ctx))
	return api.SetAgentBinding200JSONResponse{Body: binding, Headers: api.SetAgentBinding200ResponseHeaders{ETag: etag(binding.Version)}}, err
}
func (h *handlers) CreateAgentInvocation(ctx context.Context, req api.CreateAgentInvocationRequestObject) (api.CreateAgentInvocationResponseObject, error) {
	invocation, err := h.agents.CreateInvocation(ctx, req.ReviewId, *req.Body, callerFrom(ctx))
	return api.CreateAgentInvocation202JSONResponse(invocation), err
}
func (h *handlers) GetAgentInvocation(ctx context.Context, req api.GetAgentInvocationRequestObject) (api.GetAgentInvocationResponseObject, error) {
	invocation, err := h.agents.GetInvocation(ctx, req.InvocationId)
	return api.GetAgentInvocation200JSONResponse(invocation), err
}
func (h *handlers) ListAgentInvocations(ctx context.Context, req api.ListAgentInvocationsRequestObject) (api.ListAgentInvocationsResponseObject, error) {
	invocations, err := h.agents.ListInvocations(ctx, req.ReviewId)
	return api.ListAgentInvocations200JSONResponse(invocations), err
}
func (h *handlers) GetAgentInvocationContext(ctx context.Context, req api.GetAgentInvocationContextRequestObject) (api.GetAgentInvocationContextResponseObject, error) {
	result, err := h.svc.AgentInvocationContext(ctx, req.InvocationId)
	return api.GetAgentInvocationContext200JSONResponse(result), err
}
func (h *handlers) UpdateAgentInvocation(ctx context.Context, req api.UpdateAgentInvocationRequestObject) (api.UpdateAgentInvocationResponseObject, error) {
	invocation, err := h.agents.UpdateInvocation(ctx, req.InvocationId, *req.Body, callerFrom(ctx))
	return api.UpdateAgentInvocation200JSONResponse(invocation), err
}
func (h *handlers) CompleteAgentInvocation(ctx context.Context, req api.CompleteAgentInvocationRequestObject) (api.CompleteAgentInvocationResponseObject, error) {
	invocation, err := h.agents.CompleteInvocation(ctx, req.InvocationId, *req.Body, callerFrom(ctx))
	return api.CompleteAgentInvocation200JSONResponse(invocation), err
}
func (h *handlers) ListAgentQueue(ctx context.Context, req api.ListAgentQueueRequestObject) (api.ListAgentQueueResponseObject, error) {
	queue, err := h.agents.ListQueue(ctx, req.AgentSessionId)
	return api.ListAgentQueue200JSONResponse(queue), err
}
func (h *handlers) ResumeAgentQueue(ctx context.Context, req api.ResumeAgentQueueRequestObject) (api.ResumeAgentQueueResponseObject, error) {
	version, err := expectedVersion(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	session, err := h.agents.ResumeQueue(ctx, req.AgentSessionId, version, callerFrom(ctx))
	return api.ResumeAgentQueue200JSONResponse(session), err
}
func (h *handlers) CancelAgentInvocation(ctx context.Context, req api.CancelAgentInvocationRequestObject) (api.CancelAgentInvocationResponseObject, error) {
	invocation, err := h.agents.CancelInvocation(ctx, req.InvocationId, callerFrom(ctx))
	return api.CancelAgentInvocation200JSONResponse(invocation), err
}
func (h *handlers) ConnectAgentSession(ctx context.Context, req api.ConnectAgentSessionRequestObject) (api.ConnectAgentSessionResponseObject, error) {
	connection, err := h.agents.Connect(ctx, req.AgentSessionId, callerFrom(ctx))
	if err != nil {
		return nil, err
	}
	return agentConnectionStream{ctx: ctx, serverDone: h.lifetime.Done(), service: h.agents, connection: connection}, nil
}

type agentConnectionStream struct {
	ctx        context.Context
	serverDone <-chan struct{}
	service    *agents.Service
	connection *agents.Connection
}

func (s agentConnectionStream) VisitConnectAgentSessionResponse(w http.ResponseWriter) error {
	defer s.service.Disconnect(s.connection)
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := writeAgentFrame(w, "ready", map[string]string{"connection_id": s.connection.ID}); err != nil {
		return nil
	}
	if err := controller.Flush(); err != nil {
		return nil
	}
	if err := s.service.ResolveReviews(s.ctx, callerFrom(s.ctx)); err != nil {
		slog.Warn("automatic Pi selection unavailable", "err", err)
	}
	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return nil
		case <-s.serverDone:
			return nil
		case <-s.connection.Done:
			return nil
		case invocation := <-s.connection.Requests:
			if err := writeAgentFrame(w, "request", invocation); err != nil {
				return nil
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return nil
			}
		}
		if err := controller.Flush(); err != nil {
			return nil
		}
	}
}
func writeAgentFrame(w io.Writer, event string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
	return err
}
