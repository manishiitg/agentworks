# Local CI Testing with Act

This guide explains how to test GitHub Actions workflows locally using [act](https://github.com/nektos/act).

## ⚠️ Important Notes

- **Act can be slow**: First run may take several minutes to pull Docker images
- **Output delay**: Act may not show output immediately - be patient
- **Docker required**: Make sure Docker is running before using act
- **Not 100% accurate**: Some GitHub Actions features may not work exactly as on GitHub

## Prerequisites

- Docker installed and running
- Go 1.24.4 or later
- `act` tool (install with `make install-act`)

## Quick Start

1. **Install act:**
   ```bash
   make install-act
   ```

2. **Set up secrets (optional):**
   ```bash
   cp .secrets.example .secrets
   # Edit .secrets if needed (placeholder values work for replay mode)
   ```

3. **List available jobs:**
   ```bash
   make list-ci-jobs
   ```

4. **Run all CI jobs:**
   ```bash
   make test-ci
   ```
   ⚠️ **Note**: This can take 5-10 minutes on first run while Docker images are downloaded.

5. **Run a specific job:**
   ```bash
   make test-ci-job JOB=lint
   make test-ci-job JOB=build
   ```

## Available Jobs

- `lint` - Run golangci-lint
- `security-scan` - Run gitleaks secret scanning
- `test` - Run Go unit tests
- `build` - Build all binaries

## Troubleshooting

### Act Appears Stuck / No Output

Act can be slow, especially on first run. Here's what to check:

1. **Check Docker is running:**
   ```bash
   docker ps
   ```

2. **Check if Docker images are being pulled:**
   ```bash
   docker images | grep act
   ```

3. **Run with more verbose output:**
   ```bash
   act push -j lint --verbose
   ```

4. **Check Docker logs:**
   ```bash
   docker ps -a  # See all containers
   docker logs <container-id>  # View logs
   ```

### First Run is Very Slow

The first time you run act, it needs to:
- Pull the Docker image (~500MB)
- Set up the container environment
- Install dependencies

This can take 5-10 minutes. Subsequent runs are much faster.

### Apple Silicon (M1/M2/M3) Issues

If you're on Apple Silicon and encounter issues:

1. The `.actrc` file already includes `--container-architecture linux/amd64`
2. Make sure Docker Desktop is configured for emulation
3. Try running with explicit architecture:
   ```bash
   act push -j lint --container-architecture linux/amd64
   ```

### Missing Secrets

The test suite requires API keys (even in replay mode). Create `.secrets` file:
```bash
cp .secrets.example .secrets
```

For replay mode, placeholder values are sufficient:
```
VERTEX_API_KEY=placeholder-key-for-replay-mode
GOOGLE_API_KEY=placeholder-key-for-replay-mode
```

### Act Not Found

Install act manually:
- **macOS:** `brew install act`
- **Linux:** `curl -s https://raw.githubusercontent.com/nektos/act/master/install.sh | sudo bash`
- **Windows:** See [act installation guide](https://github.com/nektos/act#installation)

## Manual Act Commands

If you prefer to use act directly:

```bash
# List all workflows
act -l

# Run a specific workflow
act push -W .github/workflows/ci.yml

# Run a specific job
act push -j lint

# Run with verbose output (shows more details)
act push -j lint --verbose

# Run with secrets from file
act push --secret-file .secrets

# Run with specific architecture (for Apple Silicon)
act push -j lint --container-architecture linux/amd64
```

## Alternative: Test Without Act

If act is too slow or problematic, you can test CI steps manually:

```bash
# Test linting
make lint

# Test building
make build

# Test unit tests
make test

```

## Configuration

The `.actrc` file configures act with:
- Ubuntu latest runner image
- Verbose output enabled
- Secrets file location
- Linux/amd64 architecture for Apple Silicon compatibility

## Differences from GitHub Actions

Note that local testing with act may have some differences:
- Some actions may not work exactly as on GitHub
- File permissions might differ
- Network access may be limited
- Some secrets/environment variables may need to be set manually
- Output may not appear in real-time

For production CI, always verify workflows run successfully on GitHub Actions.

## Resources

- [Act Documentation](https://github.com/nektos/act)
- [Act Issues](https://github.com/nektos/act/issues) - If you encounter problems
- [GitHub Actions Documentation](https://docs.github.com/en/actions)
