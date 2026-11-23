#!/bin/bash

# Install Git Hooks for Code Quality and Security Checks
# This script sets up pre-commit hooks to scan staged files for secrets with gitleaks (lint runs on GitHub)

set -e

echo "🔒 Setting up pre-commit hooks for code quality and security..."

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

# Create scripts directory if it doesn't exist
mkdir -p scripts

# Create the pre-commit hook script
HOOK_DIR="$(git rev-parse --path-format=absolute --git-path hooks)"
mkdir -p "$HOOK_DIR"
cat > "$HOOK_DIR/pre-commit" << 'EOF'
#!/bin/bash
# Secrets scan only. Lint and tests run on GitHub (ci.yml) on every push;
# running golangci-lint here slowed the owner's laptop on every commit.
set -e
REPO_ROOT="$(git rev-parse --show-toplevel)"
if ! command -v gitleaks &> /dev/null; then
    echo "⚠️  gitleaks not installed; secret scan skipped (brew install gitleaks)."
    exit 0
fi
CONFIG=()
[ -f "$REPO_ROOT/.gitleaks.toml" ] && CONFIG=(--config "$REPO_ROOT/.gitleaks.toml")
if gitleaks protect --staged "${CONFIG[@]}" --redact; then
    echo "✅ No secrets detected."
else
    echo "❌ Secrets detected! Commit blocked. Remove them and commit again."
    exit 1
fi
EOF

# Make the pre-commit hook executable
chmod +x "$HOOK_DIR/pre-commit"

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
    exit 1
fi
EOF

# Make the scan script executable
chmod +x scripts/scan-secrets.sh

# Test the installations
echo -e "${BLUE}🧪 Testing installations...${NC}"


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
echo "  • Every commit will be automatically scanned for secrets with gitleaks"
echo "  • Commits with secrets will be blocked"
echo "  • You'll get clear error messages if issues are detected"
echo ""
echo -e "${BLUE}Manual scanning:${NC}"
echo "  • Run './scripts/scan-secrets.sh' to scan the entire repository"
echo "  • Run './scripts/scan-secrets.sh path/to/file' to scan specific files"
echo ""
echo -e "${BLUE}Configuration:${NC}"
echo "  • Edit '.gitleaks.toml' to customize secret detection rules"
echo ""
echo -e "${GREEN}Your repository is now protected! 🔒${NC}"

