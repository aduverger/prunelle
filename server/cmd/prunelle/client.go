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

func reviewCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: prunelle review create|ls|capture|import|delete")
	}
	flags, client := clientFlags("review "+args[0], cfg)
	description := flags.String("description", "", "review description (create)")
	revision := flags.String("revision", "", "current revision to import into (default current; older graphs are read-only)")
	path := flags.String("path", "", "explicit source folder (create; default infer from cwd)")
	baseRef := flags.String("base-ref", "", "capture-wide base ref (capture; default server [snapshot] setting)")
	yes := flags.Bool("yes", false, "confirm permanent deletion of this review's Prunelle data")
	var repositorySpecs, refSpecs []string
	flags.Func("repo", "explicit LABEL=PATH repository (create; repeatable)", func(value string) error { repositorySpecs = append(repositorySpecs, value); return nil })
	flags.Func("repo-base-ref", "REPOSITORY_ID=REF override (capture; repeatable)", func(value string) error { refSpecs = append(refSpecs, value); return nil })
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	rest := flags.Args()
	if args[0] != "capture" && (*baseRef != "" || len(refSpecs) > 0) {
		return errors.New("base refs are capture inputs; use review capture --base-ref or --repo-base-ref")
	}
	if args[0] != "create" && (*path != "" || len(repositorySpecs) > 0) {
		return errors.New("--path and --repo are only supported by review create")
	}
	switch {
	case args[0] == "ls" && len(rest) == 0:
		return output(c.ListReviewsWithResponse(ctx))
	case args[0] == "create" && len(rest) <= 1:
		req := review.CreateReviewRequest{Description: *description}
		if len(rest) == 1 {
			req.Title = rest[0]
		}
		if len(repositorySpecs) > 0 {
			if *path != "" {
				return errors.New("choose --path or --repo, not both")
			}
			for _, spec := range repositorySpecs {
				label, path, ok := strings.Cut(spec, "=")
				if !ok || path == "" {
					return fmt.Errorf("repository %q must be LABEL=PATH", spec)
				}
				abs, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				req.Repositories = append(req.Repositories, review.CreateRepositoryRequest{Label: label, Path: abs})
			}
		} else if *path != "" {
			req.Path, err = filepath.Abs(*path)
		} else {
			req.Path, err = os.Getwd()
			req.InferContext = true
		}
		if err != nil {
			return err
		}
		return output(c.CreateReviewWithResponse(ctx, nil, req))
	case args[0] == "capture" && len(rest) <= 1:
		reviewID := ""
		if len(rest) == 1 {
			reviewID = rest[0]
		}
		reviewID, err = resolveReviewID(ctx, c, reviewID)
		if err != nil {
			return err
		}
		req := review.CaptureRequest{BaseRef: *baseRef}
		for _, spec := range refSpecs {
			id, ref, ok := strings.Cut(spec, "=")
			if !ok || id == "" || ref == "" {
				return fmt.Errorf("base-ref override %q must be REPOSITORY_ID=REF", spec)
			}
			req.RepositoryBaseRefs = append(req.RepositoryBaseRefs, review.RepositoryBaseRef{RepositoryID: id, BaseRef: ref})
		}
		return output(c.CaptureRevisionWithResponse(ctx, reviewID, nil, req))
	case args[0] == "delete" && len(rest) == 1:
		if !*yes {
			return errors.New("this permanently deletes the review's Prunelle data, never project files; confirm with review delete --yes REVIEW_ID")
		}
		response, err := c.DeleteReviewWithResponse(ctx, rest[0])
		return checked(response, err)
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
	return fmt.Errorf("unknown or incomplete review command %q; review create takes only an optional title, not a workspace ID", strings.Join(args, " "))
}

func resolveReviewID(ctx context.Context, client *api.ClientWithResponses, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	resolved, err := client.ResolveReviewWithResponse(ctx, &api.ResolveReviewParams{Path: cwd})
	if err := checked(resolved, err); err != nil {
		return "", err
	}
	return resolved.JSON200.ID, nil
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
	rv, err := c.CreateReviewWithResponse(ctx, nil, fixture.ReviewRequest(repos))
	if err := checked(rv, err); err != nil {
		return err
	}
	revision, err := c.CaptureRevisionWithResponse(ctx, rv.JSON201.ID, nil, review.CaptureRequest{BaseRef: fixture.BaseBranch})
	if err := checked(revision, err); err != nil {
		return err
	}
	return output(c.ImportManifestWithResponse(ctx, rv.JSON201.ID, nil, review.ImportRequest{Manifest: fixture.Manifest()}))
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
