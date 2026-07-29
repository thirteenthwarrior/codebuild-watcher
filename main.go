// codebuild-watcher polls AWS CodeBuild project status and prints on change.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/codebuild/types"
)

var version = "dev"

const (
	defaultRegion = "us-east-1"
	pollInterval  = 10 * time.Second
)

var confFiles = []string{
	"/etc/codebuild-watcher.conf",
	filepath.Join(os.Getenv("HOME"), ".config", "codebuild-watcher.conf"),
}

// ANSI colour codes.
const (
	green  = "\033[92m"
	red    = "\033[91m"
	yellow = "\033[93m"
	reset  = "\033[0m"
)

// project pairs a CodeBuild project name with its AWS region.
type project struct {
	region string
	name   string
}

// parseProjectLine parses a config line into a project.
// Accepts "region:name" or plain "name" (defaults to us-east-1).
func parseProjectLine(line string) project {
	if i := strings.IndexByte(line, ':'); i > 0 {
		return project{region: line[:i], name: line[i+1:]}
	}
	return project{region: defaultRegion, name: line}
}

// loadProjectsFromFile reads projects from a single file.
// Returns nil, nil if the file does not exist.
func loadProjectsFromFile(path string) ([]project, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var projects []project
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		projects = append(projects, parseProjectLine(line))
	}
	return projects, scanner.Err()
}

// loadProjects merges projects from all conf files, deduplicating by region+name.
// Silently skips files that do not exist. Errors if no projects are found.
func loadProjects(paths []string) ([]project, error) {
	type key struct{ region, name string }
	seen := make(map[key]bool)
	var projects []project
	for _, path := range paths {
		entries, err := loadProjectsFromFile(path)
		if err != nil {
			return nil, err
		}
		for _, p := range entries {
			k := key{p.region, p.name}
			if !seen[k] {
				seen[k] = true
				projects = append(projects, p)
			}
		}
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects found in: %s", strings.Join(paths, ", "))
	}
	return projects, nil
}

// buildInfo holds the fields we care about for a single build.
type buildInfo struct {
	id     string
	status types.StatusType
	start  *time.Time
	end    *time.Time
}

func getLatestBuild(ctx context.Context, cb *codebuild.Client, project string) (*buildInfo, error) {
	listOut, err := cb.ListBuildsForProject(ctx, &codebuild.ListBuildsForProjectInput{
		ProjectName: &project,
		SortOrder:   types.SortOrderTypeDescending,
	})
	if err != nil {
		return nil, err
	}
	if len(listOut.Ids) == 0 {
		return nil, nil
	}

	batchOut, err := cb.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{
		Ids: []string{listOut.Ids[0]},
	})
	if err != nil {
		return nil, err
	}
	if len(batchOut.Builds) == 0 {
		return nil, nil
	}

	b := batchOut.Builds[0]
	return &buildInfo{
		id:     *b.Id,
		status: b.BuildStatus,
		start:  b.StartTime,
		end:    b.EndTime,
	}, nil
}

func prettyTime(t *time.Time) string {
	if t == nil {
		return "unknown"
	}
	return t.In(time.Local).Format("2006-01-02 15:04:05 MST")
}

func colorize(status types.StatusType) string {
	switch status {
	case types.StatusTypeSucceeded:
		return green + string(status) + reset
	case types.StatusTypeFailed, types.StatusTypeFault, types.StatusTypeTimedOut:
		return red + string(status) + reset
	case types.StatusTypeInProgress:
		return yellow + string(status) + reset
	default:
		return string(status)
	}
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: codebuild-watcher [options]

Polls AWS CodeBuild project status and prints a line whenever a build
starts, finishes, or changes state. Polls every %s.

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

	// Build one CodeBuild client per region.
	clients := make(map[string]*codebuild.Client)
	for _, p := range projects {
		if _, ok := clients[p.region]; ok {
			continue
		}
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(p.region))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading AWS config for region %s: %v\n", p.region, err)
			os.Exit(1)
		}
		clients[p.region] = codebuild.NewFromConfig(cfg)
	}

	type stateKey struct{ region, name string }
	lastBuild := make(map[stateKey]string)
	lastStatus := make(map[stateKey]types.StatusType)

	fmt.Printf("Watching %d project(s) from [%s] — Ctrl-C to exit\n\n", len(projects), strings.Join(confFiles, ", "))

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Poll immediately on startup, then on each tick.
	poll := func() {
		for _, p := range projects {
			info, err := getLatestBuild(ctx, clients[p.region], p.name)
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
