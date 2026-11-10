package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/agents"
	"github.com/aduverger/prunelle/server/internal/config"
	"github.com/aduverger/prunelle/server/internal/review"
)

func agentCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: prunelle agent ls|binding|select|auto|disconnect|requests|get|prepare|ask|implement")
	}
	flags, client := clientFlags("agent "+args[0], cfg)
	reviewID := flags.String("review", "", "review ID (default cwd's sole review)")
	revisionID := flags.String("revision", "", "captured source context (prepare/ask; default current)")
	targetID := flags.String("agent-session", "", "exact Pi target; required with implement --yes")
	key := flags.String("key", "", "reuse the printed key only to retry an unchanged request")
	yes := flags.Bool("yes", false, "send the previewed saved implementation request")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	explicitTarget := *targetID != ""
	c, err := client()
	if err != nil {
		return err
	}
	rest := flags.Args()
	if args[0] == "ls" && len(rest) == 0 {
		return output(c.ListAgentSessionsWithResponse(ctx))
	}
	if args[0] == "get" && len(rest) == 1 {
		return output(c.GetAgentInvocationWithResponse(ctx, rest[0]))
	}
	valid := (args[0] == "binding" || args[0] == "auto" || args[0] == "disconnect" || args[0] == "requests" || args[0] == "prepare") && len(rest) == 0 ||
		(args[0] == "select" || args[0] == "implement") && len(rest) == 1 || args[0] == "ask" && len(rest) == 2
	if !valid {
		return fmt.Errorf("unknown or incomplete agent command %q", args[0])
	}
	*reviewID, err = resolveReviewID(ctx, c, *reviewID)
	if err != nil {
		return err
	}
	if args[0] == "requests" {
		return output(c.ListAgentInvocationsWithResponse(ctx, *reviewID))
	}
	binding, err := c.GetAgentBindingWithResponse(ctx, *reviewID)
	if err := checked(binding, err); err != nil {
		return err
	}
	if args[0] == "binding" {
		return output(binding, nil)
	}
	if args[0] == "select" || args[0] == "auto" || args[0] == "disconnect" {
		request := agents.BindingRequest{Mode: "AUTO"}
		if args[0] == "select" {
			request.Mode, request.AgentSessionID = "MANUAL", &rest[0]
		}
		if args[0] == "disconnect" {
			request.Mode = "DISCONNECTED"
		}
		etag := fmt.Sprintf(`"%d"`, binding.JSON200.Version)
		return output(c.SetAgentBindingWithResponse(ctx, *reviewID, &api.SetAgentBindingParams{IfMatch: &etag}, request))
	}
	if *targetID == "" && binding.JSON200.AgentSessionID != nil {
		*targetID = *binding.JSON200.AgentSessionID
	}
	if *targetID == "" {
		return errors.New("no attached Pi; connect the extension and use agent select or agent auto")
	}
	request := review.CreateAgentInvocationRequest{AgentSessionID: *targetID, RevisionID: *revisionID}
	switch args[0] {
	case "prepare", "ask":
		if request.RevisionID == "" {
			rv, err := c.GetReviewWithResponse(ctx, *reviewID)
			if err := checked(rv, err); err != nil {
				return err
			}
			if rv.JSON200.CurrentRevisionID == nil {
				return errors.New("capture a revision before requesting Pi work")
			}
			request.RevisionID = *rv.JSON200.CurrentRevisionID
		}
		request.Intent = "PREPARE_REVIEW"
		if args[0] == "ask" {
			request.Intent, request.DiscussionID, request.MessageID = "DISCUSS", &rest[0], &rest[1]
		}
	case "implement":
		decisions, err := c.ListDecisionsWithResponse(ctx, *reviewID)
		if err := checked(decisions, err); err != nil {
			return err
		}
		var saved *review.Decision
		for _, decision := range *decisions.JSON200 {
			if decision.ID == rest[0] {
				saved = &decision
				break
			}
		}
		if saved == nil || saved.Decision != "REQUEST_CHANGES" {
			return errors.New("implementation requires a saved REQUEST_CHANGES decision from this review")
		}
		if request.RevisionID != "" && request.RevisionID != saved.RevisionID {
			return errors.New("implementation must use the saved decision's exact revision")
		}
		request.RevisionID, request.Intent, request.DecisionID = saved.RevisionID, "IMPLEMENT", &saved.ID
		originals := []map[string]any{}
		for _, id := range saved.DiscussionIDs {
			discussion, err := c.GetDiscussionWithResponse(ctx, id)
			if err := checked(discussion, err); err != nil {
				return err
			}
			originals = append(originals, map[string]any{"discussion_id": id, "revision_id": discussion.JSON200.RevisionID, "anchor": discussion.JSON200.Anchor})
		}
		preview := map[string]any{"request": request, "decision": saved, "original_discussion_anchors": originals,
			"warning": "Implementation uses Pi's live working environment, which may differ from captured source. No commit, publication, deployment, resolution or capture is authorized."}
		writer := os.Stdout
		if *yes {
			writer = os.Stderr
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(preview); err != nil {
			return err
		}
		if !*yes {
			fmt.Fprintln(os.Stderr, "Preview only. To send, repeat with --yes --agent-session EXACT_PI_ID before DECISION_ID.")
			return nil
		}
		if !explicitTarget {
			return errors.New("sending implementation requires --yes and --agent-session EXACT_PI_ID; inspect the preview first")
		}
	}
	if *key == "" {
		*key = rand.Text()
	}
	fmt.Fprintf(os.Stderr, "Pi target: %s; captured context: %s; request key: %s (retry unchanged with --key, --agent-session and --revision; no automatic write retry)\n", request.AgentSessionID, request.RevisionID, *key)
	return output(c.CreateAgentInvocationWithResponse(ctx, *reviewID, &api.CreateAgentInvocationParams{IdempotencyKey: key}, request))
}
