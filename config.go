package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultRegion = "us-east-1"

var confFiles = []string{
	"/etc/codebuild-watcher.conf",
	filepath.Join(os.Getenv("HOME"), ".config", "codebuild-watcher.conf"),
}

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
