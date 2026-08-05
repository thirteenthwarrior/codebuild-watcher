package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

const (
	logRetryAttempts = 4
	logRetryDelay    = 5 * time.Second
)

// fetchLogs retrieves CloudWatch logs for a failed build and writes them to
// /tmp/<sanitized-build-id>.log. Retries with a fixed delay to handle the lag
// between build completion and log availability.
func fetchLogs(ctx context.Context, cw *cloudwatchlogs.Client, info *buildInfo) {
	if info.logGroup == "" || info.logStream == "" {
		fmt.Fprintf(os.Stderr, "no CloudWatch log info for build %s\n", info.id)
		return
	}

	path := logPath(info.id)

	var events []string
	var lastErr error
	for attempt := 1; attempt <= logRetryAttempts; attempt++ {
		events, lastErr = getLogEvents(ctx, cw, info.logGroup, info.logStream)
		if lastErr == nil && len(events) > 0 {
			break
		}
		if attempt < logRetryAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(logRetryDelay):
			}
		}
	}

	if lastErr != nil {
		fmt.Fprintf(os.Stderr, "error fetching logs for %s: %v\n", info.id, lastErr)
		return
	}
	if len(events) == 0 {
		fmt.Fprintf(os.Stderr, "no log events found for build %s\n", info.id)
		return
	}

	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating log file %s: %v\n", path, err)
		return
	}
	defer f.Close()

	for _, line := range events {
		fmt.Fprintln(f, line)
	}
	fmt.Printf("logs written to %s\n", path)
}

// getLogEvents pages through all events in a CloudWatch log stream.
func getLogEvents(ctx context.Context, cw *cloudwatchlogs.Client, group, stream string) ([]string, error) {
	var lines []string
	var nextToken *string

	for {
		out, err := cw.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
			LogGroupName:  &group,
			LogStreamName: &stream,
			StartFromHead: aws.Bool(true),
			NextToken:     nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, e := range out.Events {
			if e.Message != nil {
				lines = append(lines, strings.TrimRight(*e.Message, "\r\n"))
			}
		}

		// GetLogEvents returns the same token when there are no more pages.
		if nextToken != nil && out.NextForwardToken != nil && *out.NextForwardToken == *nextToken {
			break
		}
		if out.NextForwardToken == nil {
			break
		}
		nextToken = out.NextForwardToken
	}

	return lines, nil
}

// logPath returns the /tmp path for a build's log file.
// Colons in the build ID (e.g. "project:abc123") are replaced with hyphens.
func logPath(buildID string) string {
	safe := strings.ReplaceAll(buildID, ":", "-")
	return fmt.Sprintf("/tmp/%s.log", safe)
}
