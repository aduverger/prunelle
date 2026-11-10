// Command prunelle runs the review server and talks to it from the command line.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

const usage = `usage: prunelle [--config PATH] COMMAND [ARGS]

Commands:
  serve                                   run the review server
  review create [TITLE]                    create an independent review (--path DIR or repeated --repo LABEL=PATH)
  review ls                               list reviews
  review capture [REVIEW_ID]               capture source (--base-ref REF, --repo-base-ref ID=REF; default: cwd's sole review)
  review delete --yes REVIEW_ID           permanently delete review data, never project files
  review import REVIEW_ID MANIFEST.json   import a review manifest ("-" reads stdin; --revision)
  agent ls                                list connected/known Pi conversations
  agent binding                           inspect review attachment (--review ID; default cwd's sole review)
  agent select PI_ID                      explicitly attach Pi (--review ID)
  agent auto|disconnect                   enable unique-cwd attachment or intentionally detach (--review ID)
  agent prepare                           generate topics/explanations for captured changes (--review ID, --revision ID)
  agent ask DISCUSSION_ID MESSAGE_ID       ask Pi about a saved question (--review ID, --revision ID)
  agent implement DECISION_ID              preview a saved REQUEST_CHANGES handoff; --yes --agent-session ID sends it
  agent requests                          list review requests (--review ID)
  agent get INVOCATION_ID                  inspect a request; no execution is replayed
  events                                  print review changes as they happen (--after SEQUENCE)
  fixture DIR                             build the demo repositories in DIR and seed a review
  fixture --print-manifest                print the demo review manifest

Client commands reach the server at --server (default: the configured listen address).
An agent sets PRUNELLE_AGENT (e.g. pi:<session-id>) so its changes are attributed to it.
Only humans dispatch Pi work. Dispatch prints a --key for an explicit unchanged retry; never repost the saved question.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "prunelle:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("prunelle", flag.ContinueOnError)
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configPath := global.String("config", "", "config file (default <user config dir>/prunelle/config.toml)")
	if err := global.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return flag.ErrHelp
	}
	switch rest[0] {
	case "serve":
		return serve(ctx, cfg, rest[1:])
	case "workspace":
		return errors.New("workspace commands were retired; use review create, capture or delete with a review ID")
	case "review":
		return reviewCommand(ctx, cfg, rest[1:])
	case "agent":
		return agentCommand(ctx, cfg, rest[1:])
	case "events":
		return eventsCommand(ctx, cfg, rest[1:])
	case "fixture":
		return fixtureCommand(ctx, cfg, rest[1:])
	case "version":
		fmt.Println(version)
		return nil
	}
	global.Usage()
	return fmt.Errorf("unknown command %q", rest[0])
}
