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
  sessions backends                       list session backends and their health
  sessions ls                             list runtime sessions (--review ID)
  sessions link RUNTIME_ID REVIEW_ID      associate a runtime session with a review
  sessions unlink RUNTIME_ID REVIEW_ID    remove that association
  events                                  print review changes as they happen (--after SEQUENCE)
  fixture DIR                             build the demo repositories in DIR and seed a review
  fixture --print-manifest                print the demo review manifest

Client commands reach the server at --server (default: the configured listen address).
An agent sets PRUNELLE_AGENT (e.g. pi:<session-id>) so its changes are attributed to it.
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
	case "sessions":
		return sessionsCommand(ctx, cfg, rest[1:])
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
