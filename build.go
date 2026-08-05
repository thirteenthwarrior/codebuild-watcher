package main

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/codebuild/types"
)

const pollInterval = 10 * time.Second

// ANSI colour codes.
const (
	green  = "\033[92m"
	red    = "\033[91m"
	yellow = "\033[93m"
	reset  = "\033[0m"
)

// buildInfo holds the fields we care about for a single build.
type buildInfo struct {
	id        string
	status    types.StatusType
	start     *time.Time
	end       *time.Time
	logGroup  string
	logStream string
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
	info := &buildInfo{
		id:     *b.Id,
		status: b.BuildStatus,
		start:  b.StartTime,
		end:    b.EndTime,
	}
	if b.Logs != nil {
		if b.Logs.GroupName != nil {
			info.logGroup = *b.Logs.GroupName
		}
		if b.Logs.StreamName != nil {
			info.logStream = *b.Logs.StreamName
		}
	}
	return info, nil
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

func isFailure(status types.StatusType) bool {
	switch status {
	case types.StatusTypeFailed, types.StatusTypeFault, types.StatusTypeTimedOut:
		return true
	}
	return false
}
