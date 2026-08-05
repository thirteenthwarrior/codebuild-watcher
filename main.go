// codebuild-watcher polls AWS CodeBuild project status and prints on change.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/codebuild/types"
)

var version = "dev"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: codebuild-watcher [options]

Polls AWS CodeBuild project status and prints a line whenever a build
starts, finishes, or changes state. Polls every %s.

On failure, logs are fetched from CloudWatch and written to:
  /tmp/<build-id>.log

Configuration:
  Projects are merged from the following files (missing files are skipped):
    /etc/codebuild-watcher.conf
    $HOME/.config/codebuild-watcher.conf

  One entry per line. Blank lines and lines starting with # are ignored.
  Entries may optionally include an AWS region prefix:
    my-project              (defaults to us-east-1)
    us-west-2:my-project

Output:
  [region/project] STATUS (started|ended: YYYY-MM-DD HH:MM:SS TZ)

Options:
`, pollInterval)
		flag.PrintDefaults()
	}

	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("codebuild-watcher %s\n", version)
		os.Exit(0)
	}

	projects, err := loadProjects(confFiles)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Build one CodeBuild and one CloudWatch Logs client per region.
	cbClients := make(map[string]*codebuild.Client)
	cwClients := make(map[string]*cloudwatchlogs.Client)
	for _, p := range projects {
		if _, ok := cbClients[p.region]; ok {
			continue
		}
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(p.region))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading AWS config for region %s: %v\n", p.region, err)
			os.Exit(1)
		}
		cbClients[p.region] = codebuild.NewFromConfig(cfg)
		cwClients[p.region] = cloudwatchlogs.NewFromConfig(cfg)
	}

	type stateKey struct{ region, name string }
	lastBuild := make(map[stateKey]string)
	lastStatus := make(map[stateKey]types.StatusType)

	fmt.Printf("Watching %d project(s) from [%s] — Ctrl-C to exit\n\n", len(projects), strings.Join(confFiles, ", "))

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	poll := func() {
		for _, p := range projects {
			info, err := getLatestBuild(ctx, cbClients[p.region], p.name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading %s/%s: %v\n", p.region, p.name, err)
				continue
			}
			if info == nil {
				continue
			}

			var ts, label string
			if info.status == types.StatusTypeInProgress {
				ts = prettyTime(info.start)
				label = "started"
			} else {
				if info.end != nil {
					ts = prettyTime(info.end)
				} else {
					ts = prettyTime(info.start)
				}
				label = "ended"
			}

			k := stateKey{p.region, p.name}
			if lastBuild[k] != info.id || lastStatus[k] != info.status {
				lastBuild[k] = info.id
				lastStatus[k] = info.status
				fmt.Printf("[%s/%s] %s (%s: %s)\n", p.region, p.name, colorize(info.status), label, ts)

				if isFailure(info.status) {
					go fetchLogs(ctx, cwClients[p.region], info)
				}
			}
		}
	}

	poll()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nexiting.")
			return
		case <-ticker.C:
			poll()
		}
	}
}
