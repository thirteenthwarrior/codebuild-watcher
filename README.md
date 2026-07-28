# codebuild-watcher

A lightweight CLI tool that polls AWS CodeBuild project status and prints a line whenever a build starts, finishes, or changes state.

## Features

- Watches multiple CodeBuild projects simultaneously
- Prints output only when build ID or status changes (no noise)
- Colour-coded status: green for success, red for failure/timeout, yellow for in-progress
- Merges a system-wide and a per-user config file
- Graceful shutdown on Ctrl-C or SIGTERM

## Installation

Download the latest binary for your platform from the [Releases](../../releases/latest) page.

**Linux:**
```sh
curl -L https://github.com/thirteenthwarrior/codebuild-watcher/releases/latest/download/codebuild-watcher-linux-amd64 \
  -o /usr/local/bin/codebuild-watcher
chmod +x /usr/local/bin/codebuild-watcher
```

**Windows:** download `codebuild-watcher-windows-amd64.exe` from the releases page.

### Build from source

Requires Go 1.22+.

```sh
git clone https://github.com/thirteenthwarrior/codebuild-watcher.git
cd codebuild-watcher
make release
# binaries written to dist/
```

## Configuration

Projects are loaded from two config files and merged (duplicates ignored). Either file may be absent.

| File | Purpose |
|------|---------|
| `/etc/codebuild-watcher.conf` | System-wide projects (all users) |
| `$HOME/.config/codebuild-watcher.conf` | Per-user projects |

Each file is plain text — one CodeBuild project name per line. Blank lines and lines starting with `#` are ignored.

```
# my-codebuild-watcher.conf

my-backend-project
my-frontend-project

# staging
my-staging-project
```

## AWS credentials

The tool uses the standard AWS SDK credential chain — no configuration needed if you already have credentials set up:

- Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`)
- `~/.aws/credentials` / `~/.aws/config`
- IAM instance profile / ECS task role / EC2 instance metadata

The IAM principal needs the following permissions:

```json
{
  "Effect": "Allow",
  "Action": [
    "codebuild:ListBuildsForProject",
    "codebuild:BatchGetBuilds"
  ],
  "Resource": "*"
}
```

## Usage

```sh
codebuild-watcher
```

Example output:

```
Watching 3 project(s) from [/etc/codebuild-watcher.conf, /home/alice/.config/codebuild-watcher.conf] — Ctrl-C to exit

[my-backend-project] IN_PROGRESS (started: 2024-03-15 09:01:22)
[my-backend-project] SUCCEEDED (ended: 2024-03-15 09:04:51)
[my-frontend-project] FAILED (ended: 2024-03-15 09:05:10)
```

## Releasing

Tag a commit to trigger a GitHub Actions release build:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Binaries for Linux and Windows (amd64) are built and attached to the GitHub release automatically.

## License

MIT
