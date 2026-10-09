#!/bin/bash

# Install Git Hooks for Gitleaks Secret Scanning
# This script sets up pre-commit hooks to automatically scan for secrets and run linting

set -e

echo "🔒 Setting up pre-commit hooks (Gitleaks secret scanning)..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo -e "${RED}❌ Error: Not in a git repository${NC}"
    echo "Please run this script from the root of your git repository."
    exit 1
fi

# Check if gitleaks is installed
if ! command -v gitleaks &> /dev/null; then
    echo -e "${YELLOW}⚠️  Gitleaks not found. Installing...${NC}"
    
    # Detect OS and install gitleaks
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        if command -v brew &> /dev/null; then
            echo "Installing gitleaks via Homebrew..."
            brew install gitleaks
        else
            echo -e "${RED}❌ Homebrew not found. Please install gitleaks manually:${NC}"
            echo "Visit: https://github.com/gitleaks/gitleaks#installation"
            exit 1
        fi
    elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
        # Linux
        echo "Installing gitleaks via curl..."
        curl -sSfL https://github.com/gitleaks/gitleaks/releases/latest/download/gitleaks_8.18.0_linux_x64.tar.gz | tar -xz -C /tmp
        sudo mv /tmp/gitleaks /usr/local/bin/
    else
        echo -e "${RED}❌ Unsupported OS. Please install gitleaks manually:${NC}"
        echo "Visit: https://github.com/gitleaks/gitleaks#installation"
        exit 1
    fi
fi

# Verify gitleaks installation
if ! command -v gitleaks &> /dev/null; then
    echo -e "${RED}❌ Failed to install gitleaks${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Gitleaks installed successfully${NC}"

# Install the tracked hook, including in linked worktrees or custom hook paths.
REPO_ROOT="$(git rev-parse --show-toplevel)"
HOOK_DIR="$(git rev-parse --path-format=absolute --git-path hooks)"
mkdir -p "$HOOK_DIR"
for hook in pre-commit commit-msg post-commit; do
    cp "$REPO_ROOT/scripts/hooks/$hook" "$HOOK_DIR/$hook"
    chmod +x "$HOOK_DIR/$hook"
done

# Create a manual scan script
cat > scripts/scan-secrets.sh << 'EOF'
#!/bin/bash

# Manual Secret Scanning Script
# Run this to scan for secrets in your repository

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔒 Scanning repository for secrets...${NC}"

# Check if gitleaks is available
if ! command -v gitleaks &> /dev/null; then
    echo -e "${RED}❌ Gitleaks not found. Please install it first:${NC}"
    echo "Run './scripts/install-git-hooks.sh' to install gitleaks."
    exit 1
fi

# Default scan path
SCAN_PATH="${1:-.}"

echo "Scanning path: $SCAN_PATH"
echo ""

# Run gitleaks scan
if gitleaks detect --source "$SCAN_PATH" --config .gitleaks.toml --verbose --report-format json --report-path gitleaks-report.json; then
    echo -e "${GREEN}✅ No secrets detected in $SCAN_PATH${NC}"
    rm -f gitleaks-report.json
else
    echo -e "${RED}❌ Secrets detected in $SCAN_PATH${NC}"
    echo ""
    echo "Report saved to: gitleaks-report.json"
    echo ""
    echo "Please review and remove the detected secrets:"
    echo "  • Use environment variables instead of hardcoded secrets"
    echo "  • Move secrets to .env files (not tracked by git)"
    echo "  • Use placeholder values in example files"
    echo ""
    echo "For more information, see agent_go/SECURITY.md"
    exit 1
fi
EOF

# Make the scan script executable
chmod +x scripts/scan-secrets.sh

# Test the installation
echo -e "${BLUE}🧪 Testing gitleaks installation...${NC}"
if gitleaks version &> /dev/null; then
    echo -e "${GREEN}✅ Gitleaks is working correctly${NC}"
else
    echo -e "${RED}❌ Gitleaks test failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}🎉 Pre-commit hooks installed successfully!${NC}"
echo ""
echo -e "${BLUE}What happens now:${NC}"
echo "  • Every commit automatically bumps and stages the product patch version"
echo "  • Every commit will be automatically scanned for secrets (gitleaks) and sensitive file patterns"
echo "  • Commits with secrets or sensitive files will be blocked"
echo "  • You'll get clear error messages if issues are detected"
echo "  • golangci-lint / go build / frontend build are NOT run on commit anymore — CI (desktop-release.yml)"
echo "    already rebuilds everything on every push to main, so this hook stays fast. Run them yourself"
echo "    before committing if you want that signal early: 'cd agent_go && golangci-lint run ./... && go build ./...',"
echo "    'cd frontend && npm run build'"
echo ""
echo -e "${BLUE}Manual scanning:${NC}"
echo "  • Run './scripts/scan-secrets.sh' to scan the entire repository"
echo "  • Run './scripts/scan-secrets.sh path/to/file' to scan specific files"
echo "  • Run 'cd agent_go && make lint' to run golangci-lint manually"
echo ""
echo -e "${BLUE}Configuration:${NC}"
echo "  • Edit '.gitleaks.toml' to customize secret detection rules"
echo "  • Edit 'agent_go/.golangci.yml' to customize linting rules"
echo "  • See 'agent_go/SECURITY.md' for security best practices"
echo ""
echo -e "${GREEN}Your repository is now protected against accidental secret commits! 🔒${NC}"
