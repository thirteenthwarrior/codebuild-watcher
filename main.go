// codebuild-watcher — poll CodeBuild project status and print on change.
//
// Projects are merged from /etc/codebuild-watcher.conf and
// $HOME/.config/codebuild-watcher.conf (one project name per line;
// blank lines and lines starting with # are ignored; missing files are skipped).
//
// Usage:
//
//	codebuild-watcher          # poll all projects from conf file
//
// Press Ctrl-C to exit.
package main

import (
	"bufio"
	"context"
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

const (
	awsRegion    = "us-east-1"
	pollInterval = 10 * time.Second
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

// loadProjectsFromFile reads project names from a single file.
// Returns nil, nil if the file does not exist.
func loadProjectsFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	var projects []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		projects = append(projects, line)
	}
	return projects, scanner.Err()
}

// loadProjects merges project names from all conf files, deduplicating.
// Silently skips files that do not exist. Errors if no projects are found.
func loadProjects(paths []string) ([]string, error) {
	seen := make(map[string]bool)
	var projects []string
	for _, path := range paths {
		entries, err := loadProjectsFromFile(path)
		if err != nil {
			return nil, err
		}
		for _, p := range entries {
			if !seen[p] {
				seen[p] = true
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
	return t.Format("2006-01-02 15:04:05")
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
	projects, err := loadProjects(confFiles)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(awsRegion))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error loading AWS config:", err)
		os.Exit(1)
	}
	cb := codebuild.NewFromConfig(cfg)

	lastBuild := make(map[string]string)
	lastStatus := make(map[string]types.StatusType)

	fmt.Printf("Watching %d project(s) from [%s] — Ctrl-C to exit\n\n", len(projects), strings.Join(confFiles, ", "))

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Poll immediately on startup, then on each tick.
	poll := func() {
		for _, project := range projects {
			info, err := getLatestBuild(ctx, cb, project)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading %s: %v\n", project, err)
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

			if lastBuild[project] != info.id || lastStatus[project] != info.status {
				lastBuild[project] = info.id
				lastStatus[project] = info.status
				fmt.Printf("[%s] %s (%s: %s)\n", project, colorize(info.status), label, ts)
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
