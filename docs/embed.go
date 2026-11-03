// Package docs embeds the authored HelpDocs and their offline resources.
package docs

import "embed"

//go:embed help/*.md cli-spec.md invocation-diagnostics.md launch-agents.md design/server-status.md design/recipient-markdown-parity.md design/external-filesystem-channel.md assets asset-modes.json
var Content embed.FS
