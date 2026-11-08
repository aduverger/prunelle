package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aduverger/prunelle/server/api"
	"github.com/aduverger/prunelle/server/internal/config"
	"github.com/aduverger/prunelle/server/internal/fixture"
	"github.com/aduverger/prunelle/server/internal/review"
)

// clientFlags adds --server to a subcommand and returns a constructor for the API client.
func clientFlags(name string, cfg config.Config) (*flag.FlagSet, func() (*api.ClientWithResponses, error)) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	server := flags.String("server", "http://"+cfg.Listen, "server URL")
	return flags, func() (*api.ClientWithResponses, error) { return api.NewClientWithResponses(*server) }
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
		return errors.New("usage: prunelle workspace add|ls")
	}
	flags, client := clientFlags("workspace "+args[0], cfg)
	baseRef := flags.String("base-ref", "main", "base ref for every repository")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	switch args[0] {
	case "ls":
		return output(c.ListWorkspacesWithResponse(ctx))
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
		return output(c.CreateWorkspaceWithResponse(ctx, req))
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
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	rest := flags.Args()
	switch {
	case args[0] == "ls":
		return output(c.ListReviewsWithResponse(ctx, &api.ListReviewsParams{}))
	case args[0] == "create" && len(rest) == 2:
		return output(c.CreateReviewWithResponse(ctx, review.CreateReviewRequest{WorkspaceID: rest[0], Title: rest[1], Description: *description}))
	case args[0] == "capture" && len(rest) == 1:
		return output(c.CaptureRevisionWithResponse(ctx, rest[0]))
	case args[0] == "import" && len(rest) == 2:
		manifest, err := readManifest(rest[1])
		if err != nil {
			return err
		}
		req := review.ImportRequest{Manifest: manifest}
		if *revision != "" {
			req.RevisionID = revision
		}
		return output(c.ImportManifestWithResponse(ctx, rest[0], req))
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
	ws, err := c.CreateWorkspaceWithResponse(ctx, fixture.WorkspaceRequest(repos))
	if err := checked(ws, err); err != nil {
		return fmt.Errorf("register workspace (is %s inside allowed_roots?): %w", dir, err)
	}
	rv, err := c.CreateReviewWithResponse(ctx, review.CreateReviewRequest{WorkspaceID: ws.JSON201.ID, Title: fixture.ReviewTitle,
		Description: "Demo review generated by `prunelle fixture`."})
	if err := checked(rv, err); err != nil {
		return err
	}
	revision, err := c.CaptureRevisionWithResponse(ctx, rv.JSON201.ID)
	if err := checked(revision, err); err != nil {
		return err
	}
	return output(c.ImportManifestWithResponse(ctx, rv.JSON201.ID, review.ImportRequest{Manifest: fixture.Manifest()}))
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
