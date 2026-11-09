package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/config"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

// agentEnv names the agent running the CLI, e.g. pi:<session-id>; requests then act as that agent.
const agentEnv = "PRUNELLE_AGENT"

// clientFlags adds --server to a subcommand and returns a constructor for the API client.
func clientFlags(name string, cfg config.Config) (*flag.FlagSet, func() (*api.ClientWithResponses, error)) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	server := flags.String("server", "http://"+cfg.Listen, "server URL")
	return flags, func() (*api.ClientWithResponses, error) {
		return api.NewClientWithResponses(*server, api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			setAgent(req)
			return nil
		}))
	}
}

func setAgent(req *http.Request) {
	if agent := os.Getenv(agentEnv); agent != "" {
		req.Header.Set("Prunelle-Agent", agent)
	}
}

type apiResponse interface {
	StatusCode() int
	GetBody() []byte
}

// output prints a successful response as indented JSON, or returns the failure.
func output(resp apiResponse, err error) error {
	if err := checked(resp, err); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, resp.GetBody(), "", "  "); err != nil {
		return err
	}
	fmt.Println(pretty.String())
	return nil
}

// checked returns the transport error, else the API error carried by resp, if any.
func checked(resp apiResponse, err error) error {
	if err != nil {
		return err
	}
	return apiError(resp)
}

func apiError(resp apiResponse) error {
	if resp.StatusCode() < 300 {
		return nil
	}
	var envelope api.Error
	if json.Unmarshal(resp.GetBody(), &envelope) != nil || envelope.Error.Message == "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode(), resp.GetBody())
	}
	msg := fmt.Sprintf("%s: %s", envelope.Error.Code, envelope.Error.Message)
	for _, d := range envelope.Error.Details {
		msg += "\n  " + d
	}
	return errors.New(msg)
}

func workspaceCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: prunelle workspace add|ls|delete")
	}
	flags, client := clientFlags("workspace "+args[0], cfg)
	baseRef := flags.String("base-ref", "main", "base ref for every repository (add)")
	discover := flags.Bool("discover", false, "list discovered workspace folders without registering them (ls)")
	yes := flags.Bool("yes", false, "confirm permanent deletion of all Prunelle data for the workspace (delete)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "ls":
		if flags.NArg() != 0 {
			return errors.New("usage: prunelle workspace ls [--discover]")
		}
		if *discover {
			return output(c.DiscoverWorkspacesWithResponse(ctx))
		}
		return output(c.ListWorkspacesWithResponse(ctx))
	case "delete":
		if flags.NArg() != 1 {
			return errors.New("usage: prunelle workspace delete --yes WORKSPACE_ID")
		}
		if !*yes {
			return errors.New("this permanently deletes all Prunelle reviews, snapshots, discussions and decisions for the workspace, not project files; confirm with workspace delete --yes WORKSPACE_ID")
		}
		resp, err := c.DeleteWorkspaceWithResponse(ctx, flags.Arg(0))
		return checked(resp, err)
	case "add":
		rest := flags.Args()
		if len(rest) < 2 {
			return errors.New("usage: prunelle workspace add NAME LABEL=PATH...")
		}
		req := review.CreateWorkspaceRequest{Name: rest[0]}
		for _, spec := range rest[1:] {
			label, path, ok := strings.Cut(spec, "=")
			if !ok {
				return fmt.Errorf("repository %q must be LABEL=PATH", spec)
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			req.Repositories = append(req.Repositories, review.CreateRepositoryRequest{Label: label, Path: abs, BaseRef: *baseRef})
		}
		return output(c.CreateWorkspaceWithResponse(ctx, nil, req))
	}
	return fmt.Errorf("unknown workspace command %q", args[0])
}

func reviewCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: prunelle review create|ls|capture|import")
	}
	flags, client := clientFlags("review "+args[0], cfg)
	description := flags.String("description", "", "review description (create)")
	revision := flags.String("revision", "", "revision to import into (import; default current)")
	workspace := flags.String("workspace", "", "explicit workspace ID (create; default infer from cwd)")
	baseRef := flags.String("base-ref", "", "initial automatic workspace base ref (create; default main)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	rest := flags.Args()
	if args[0] != "create" && (*workspace != "" || *baseRef != "") {
		return errors.New("--workspace and --base-ref are only supported by review create")
	}
	switch {
	case args[0] == "ls":
		return output(c.ListReviewsWithResponse(ctx, &api.ListReviewsParams{}))
	case args[0] == "create" && len(rest) <= 2:
		req := review.CreateReviewRequest{WorkspaceID: *workspace, Description: *description, BaseRef: *baseRef}
		if len(rest) == 2 {
			if *workspace != "" {
				return errors.New("do not combine --workspace with the legacy positional workspace ID")
			}
			req.WorkspaceID, req.Title = rest[0], rest[1]
		} else if len(rest) == 1 {
			req.Title = rest[0]
		}
		if req.WorkspaceID == "" {
			req.WorkspacePath, err = os.Getwd()
			if err != nil {
				return err
			}
		}
		return output(c.CreateReviewWithResponse(ctx, nil, req))
	case args[0] == "capture" && len(rest) <= 1:
		id := ""
		if len(rest) == 1 {
			id = rest[0]
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			resolved, err := c.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: cwd})
			if err := checked(resolved, err); err != nil {
				return err
			}
			id = resolved.JSON200.ID
		}
		return output(c.CaptureRevisionWithResponse(ctx, id, nil))
	case args[0] == "import" && len(rest) == 2:
		manifest, err := readManifest(rest[1])
		if err != nil {
			return err
		}
		req := review.ImportRequest{Manifest: manifest}
		if *revision != "" {
			req.RevisionID = revision
		}
		return output(c.ImportManifestWithResponse(ctx, rest[0], nil, req))
	}
	return fmt.Errorf("unknown or incomplete review command %q", strings.Join(args, " "))
}

func readManifest(path string) (review.Manifest, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return review.Manifest{}, err
		}
		defer f.Close()
		r = f
	}
	var m review.Manifest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return review.Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return m, nil
}

// fixtureCommand builds the demo repositories and seeds a review through the API.
func fixtureCommand(ctx context.Context, cfg config.Config, args []string) error {
	flags, client := clientFlags("fixture", cfg)
	printManifest := flags.Bool("print-manifest", false, "print the demo manifest and exit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *printManifest {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(fixture.Manifest())
	}
	if flags.NArg() != 1 {
		return errors.New("usage: prunelle fixture DIR")
	}
	dir, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	repos, err := fixture.BuildRepos(dir)
	if err != nil {
		return err
	}
	ws, err := c.CreateWorkspaceWithResponse(ctx, nil, fixture.WorkspaceRequest(repos))
	if err := checked(ws, err); err != nil {
		return fmt.Errorf("register workspace (is %s inside workspace_root_paths?): %w", dir, err)
	}
	rv, err := c.CreateReviewWithResponse(ctx, nil, review.CreateReviewRequest{WorkspaceID: ws.JSON201.ID, Title: fixture.ReviewTitle,
		Description: "Demo review generated by `prunelle fixture`."})
	if err := checked(rv, err); err != nil {
		return err
	}
	revision, err := c.CaptureRevisionWithResponse(ctx, rv.JSON201.ID, nil)
	if err := checked(revision, err); err != nil {
		return err
	}
	return output(c.ImportManifestWithResponse(ctx, rv.JSON201.ID, nil, review.ImportRequest{Manifest: fixture.Manifest()}))
}

func sessionsCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: prunelle sessions backends|ls|link|unlink")
	}
	flags, client := clientFlags("sessions "+args[0], cfg)
	workspace := flags.String("workspace", "", "only sessions associated with this workspace (ls)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	rest := flags.Args()
	switch {
	case args[0] == "backends":
		return output(c.ListSessionBackendsWithResponse(ctx))
	case args[0] == "ls":
		params := &api.ListRuntimeSessionsParams{}
		if *workspace != "" {
			params.WorkspaceId = workspace
		}
		return output(c.ListRuntimeSessionsWithResponse(ctx, params))
	case args[0] == "link" && len(rest) == 2:
		return output(c.AssociateRuntimeSessionWithResponse(ctx, rest[1], rest[0]))
	case args[0] == "unlink" && len(rest) == 2:
		resp, err := c.DissociateRuntimeSessionWithResponse(ctx, rest[1], rest[0])
		if err != nil {
			return err
		}
		return apiError(resp)
	}
	return fmt.Errorf("unknown or incomplete sessions command %q", strings.Join(args, " "))
}

// eventsCommand prints the event stream, one JSON event per line, until interrupted.
func eventsCommand(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	server := flags.String("server", "http://"+cfg.Listen, "server URL")
	after := flags.Int64("after", -1, "print events after this sequence (default: from now)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	url := strings.TrimSuffix(*server, "/") + "/v1/events"
	if *after >= 0 {
		url += fmt.Sprintf("?after=%d", *after)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	setAgent(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	lines := bufio.NewScanner(resp.Body)
	event := ""
	for lines.Scan() {
		line := lines.Text()
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			event = name
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok && event != "ready" {
			fmt.Println(data)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return lines.Err()
}
