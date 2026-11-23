package skillassets

import "embed"

const DelegationSkillName = "delegate-coding-agent"

// Files contains the portable skill copied into project-local host directories.
//
//go:embed delegate-coding-agent/SKILL.md
var Files embed.FS
