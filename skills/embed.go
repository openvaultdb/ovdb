// Package skills embeds the Agent Skills `ovdb skills install` installs
// (spec/features/ai-agent-skills): each directory is one skill, named as it
// is installed into an AI agent's skills directory.
package skills

import "embed"

// FS holds openvaultdb/ (the OpenVaultDB storage skill) and
// openvaultdb-todo-demo/ (the TODO demo skill).
//
//go:embed all:openvaultdb all:openvaultdb-todo-demo
var FS embed.FS
